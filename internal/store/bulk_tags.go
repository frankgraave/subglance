package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// MaxTagOperationIDs bounds explicit selections. Global renames have no row cap.
const MaxTagOperationIDs = 10000

var ErrInvalidTagOperation = errors.New("invalid tag operation")
var ErrTagPreviewChanged = errors.New("tags changed since the preview; review a new preview before saving")

// TagOperation describes intent, never a stale replacement of a monitor's tags.
// IDs are required for apply/remove and forbidden for global renames.
type TagOperation struct {
	Action     string  `json:"action"`
	MonitorIDs []int64 `json:"monitor_ids,omitempty"`
	Key        string  `json:"key"`
	Value      string  `json:"value,omitempty"`
	NewKey     string  `json:"new_key,omitempty"`
	NewValue   string  `json:"new_value,omitempty"`
}

// TagOperationResult counts monitors, not individual tag rows. Collisions counts
// keys whose existing value would be replaced (apply) or retained (rename_key).
type TagOperationResult struct {
	Total      int `json:"total"`
	Changed    int `json:"changed"`
	Unchanged  int `json:"unchanged"`
	Collisions int `json:"collisions"`
}

func (op TagOperation) normalised() (TagOperation, error) {
	bad := func(message string) (TagOperation, error) {
		return TagOperation{}, fmt.Errorf("%w: %s", ErrInvalidTagOperation, message)
	}
	if op.Action != "apply" && op.Action != "remove" && op.Action != "rename_key" && op.Action != "rename_value" {
		return bad("unknown action")
	}
	global := op.Action == "rename_key" || op.Action == "rename_value"
	if global && len(op.MonitorIDs) != 0 {
		return bad("global renames do not accept monitor_ids")
	}
	if !global && (len(op.MonitorIDs) == 0 || len(op.MonitorIDs) > MaxTagOperationIDs) {
		return bad("monitor_ids must contain between 1 and 10000 IDs")
	}
	op.MonitorIDs = slices.Clone(op.MonitorIDs)
	slices.Sort(op.MonitorIDs)
	for i, id := range op.MonitorIDs {
		if id <= 0 || (i > 0 && op.MonitorIDs[i-1] == id) {
			return bad("monitor_ids must be positive and unique")
		}
	}
	if (op.Action != "rename_value" && op.NewValue != "") || (op.Action != "rename_key" && op.NewKey != "") || (op.Action == "rename_key" && op.Value != "") {
		return bad("unexpected value, new_key or new_value for this action")
	}
	value := op.Value
	if op.Action == "rename_key" {
		value = "key"
	}
	tags, err := NormaliseTags(map[string]string{op.Key: value})
	if err != nil {
		return bad(err.Error())
	}
	for key, value := range tags {
		op.Key = key
		if op.Action != "rename_key" {
			op.Value = value
		}
	}
	if op.Action == "rename_key" {
		next, err := NormaliseTags(map[string]string{op.NewKey: "key"})
		if err != nil {
			return bad(err.Error())
		}
		for key := range next {
			op.NewKey = key
		}
		if op.NewKey == op.Key {
			return bad("new_key must differ from key")
		}
	}
	if op.Action == "rename_value" {
		next, err := NormaliseTags(map[string]string{op.Key: op.NewValue})
		if err != nil {
			return bad(err.Error())
		}
		op.NewValue = next[op.Key]
		if op.NewValue == op.Value {
			return bad("new_value must differ from value")
		}
	}
	return op, nil
}

type tagOperationRow struct {
	ID   int64
	Tags map[string]string
}

// TransformTags reads and transforms CURRENT tags on the writer connection in
// one transaction. It never writes other configuration columns. The preview
// validator covers intent, target IDs and relevant key values; an unrelated tag
// or configuration change need not block a safe transform of a different key.
func (db *DB) TransformTags(ctx context.Context, raw TagOperation, expected string, preview bool) (TagOperationResult, string, error) {
	var empty TagOperationResult
	op, err := raw.normalised()
	if err != nil {
		return empty, "", err
	}
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return empty, "", fmt.Errorf("begin tag operation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	encoded, _ := json.Marshal(op.MonitorIDs)
	global := op.Action == "rename_key" || op.Action == "rename_value"
	rows, err := tx.QueryContext(ctx, `SELECT m.id,t.key,t.value FROM monitors m
 LEFT JOIN monitor_tags t ON t.monitor_id=m.id
 WHERE (? AND m.id IN (SELECT monitor_id FROM monitor_tags WHERE key=? AND (? != 'rename_value' OR value=?)))
 OR (NOT ? AND m.id IN (SELECT value FROM json_each(?)))
 ORDER BY m.id,t.key`, global, op.Key, op.Action, op.Value, global, string(encoded))
	if err != nil {
		return empty, "", fmt.Errorf("read tag operation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	current := []tagOperationRow{}
	for rows.Next() {
		var id int64
		var key, value sql.NullString
		if err := rows.Scan(&id, &key, &value); err != nil {
			return empty, "", err
		}
		if len(current) == 0 || current[len(current)-1].ID != id {
			current = append(current, tagOperationRow{ID: id, Tags: map[string]string{}})
		}
		if key.Valid {
			current[len(current)-1].Tags[key.String] = value.String
		}
	}
	err = rows.Err()
	if err != nil {
		return empty, "", err
	}
	if !global && len(current) != len(op.MonitorIDs) {
		return empty, "", fmt.Errorf("selected monitor not found: %w", sql.ErrNoRows)
	}

	// JSON encoding sorts map keys; query and ID order are explicit. The hash is
	// a validator, not an authorisation token. Both routes require write access.
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(op)
	for _, row := range current {
		_ = encoder.Encode(row.ID)
		relevant := map[string]string{}
		if v, ok := row.Tags[op.Key]; ok {
			relevant[op.Key] = v
		}
		if v, ok := row.Tags[op.NewKey]; global && ok {
			relevant[op.NewKey] = v
		}
		_ = encoder.Encode(relevant)
	}
	etag := fmt.Sprintf(`"tags-%x"`, hash.Sum(nil))
	if !preview && expected != etag {
		return empty, "", ErrTagPreviewChanged
	}
	result := TagOperationResult{Total: len(current)}
	changed := []tagOperationRow{}
	for _, row := range current {
		next := maps.Clone(row.Tags)
		switch op.Action {
		case "rename_key":
			if _, exists := next[op.NewKey]; exists {
				result.Collisions++
			} else {
				next[op.NewKey] = next[op.Key]
			}
			delete(next, op.Key)
		case "rename_value":
			next[op.Key] = op.NewValue
		case "remove":
			if next[op.Key] == op.Value {
				delete(next, op.Key)
			}
		case "apply":
			if old, ok := next[op.Key]; ok && old != op.Value {
				result.Collisions++
			}
			next[op.Key] = op.Value
		}
		if maps.Equal(next, row.Tags) {
			continue
		}
		if _, err := NormaliseTags(next); err != nil {
			return empty, "", fmt.Errorf("%w: monitor %d: %w", ErrInvalidTagOperation, row.ID, err)
		}
		changed = append(changed, tagOperationRow{ID: row.ID, Tags: next})
	}
	result.Changed = len(changed)
	result.Unchanged = result.Total - result.Changed
	if preview {
		return result, etag, nil
	}
	for _, row := range changed {
		if err := replaceTags(ctx, tx, row.ID, row.Tags); err != nil {
			return empty, "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE monitors SET updated_at=MAX(?,updated_at+1) WHERE id=?`, time.Now().Unix(), row.ID); err != nil {
			return empty, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return empty, "", fmt.Errorf("commit tag operation: %w", err)
	}
	return result, etag, nil
}

// ValidTagPreviewETag accepts only the strong validator this operation issues.
func ValidTagPreviewETag(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, `"tags-`) || !strings.HasSuffix(value, `"`) {
		return false
	}
	for _, c := range value[6:70] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
