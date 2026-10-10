package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrInvalidChannelOperation is a bulk channel request that cannot be read as
// one: an unknown action, a missing channel or a malformed selection.
var ErrInvalidChannelOperation = errors.New("invalid channel operation")

// ErrChannelPreviewChanged is a commit whose selection, channel links or
// channel state moved after the preview it names.
var ErrChannelPreviewChanged = errors.New("channels changed since the preview; review a new preview before saving")

// ChannelOperation adds one channel to, or removes it from, the own channel
// links of a selection of monitors.
//
// It states intent, never a replacement set. A client that sent "these are
// the channels each monitor should have" would have to read every monitor's
// links first, and a link it failed to read would quietly become a link it
// removed. Adding or removing one channel against the links the server holds
// at write time has no such read to get wrong.
type ChannelOperation struct {
	Action     string  `json:"action"`
	MonitorIDs []int64 `json:"monitor_ids"`
	ChannelID  int64   `json:"channel_id"`
}

// ChannelOperationResult counts monitors. LeftWithoutOwn is the number of
// changed monitors a remove leaves with no channel of their own: they alert
// through the tag routing rules that match them, or else the default channel.
type ChannelOperationResult struct {
	Total          int  `json:"total"`
	Changed        int  `json:"changed"`
	Unchanged      int  `json:"unchanged"`
	LeftWithoutOwn int  `json:"left_without_own"`
	ChannelEnabled bool `json:"channel_enabled"`
}

func (op ChannelOperation) normalised() (ChannelOperation, error) {
	bad := func(message string) (ChannelOperation, error) {
		return ChannelOperation{}, fmt.Errorf("%w: %s", ErrInvalidChannelOperation, message)
	}
	if op.Action != "add" && op.Action != "remove" {
		return bad("action must be add or remove")
	}
	if op.ChannelID <= 0 {
		return bad("channel_id must be a positive channel ID")
	}
	// The same ceiling as a tag change on a selection: one transaction, one
	// request body, and no silently skipped tail.
	if len(op.MonitorIDs) == 0 || len(op.MonitorIDs) > MaxTagOperationIDs {
		return bad("monitor_ids must contain between 1 and 10000 IDs")
	}
	op.MonitorIDs = slices.Clone(op.MonitorIDs)
	slices.Sort(op.MonitorIDs)
	for i, id := range op.MonitorIDs {
		if id <= 0 || (i > 0 && op.MonitorIDs[i-1] == id) {
			return bad("monitor_ids must be positive and unique")
		}
	}
	return op, nil
}

// ChangeMonitorChannels previews or commits one ChannelOperation in a single
// transaction on the writer connection.
//
// The preview returns a validator over the intent, the selection, every
// selected monitor's own channel links and whether the channel is enabled.
// The commit recomputes it inside its own transaction and refuses with
// ErrChannelPreviewChanged when it differs, so the counts someone confirmed
// are the counts that are written. Either every changed monitor gets the new
// links or none does.
//
// A changed monitor's updated_at advances, as SetMonitorChannels does: an
// edit form holding the monitor's older ETag must not put back the links it
// read before this change.
func (db *DB) ChangeMonitorChannels(ctx context.Context, raw ChannelOperation, expected string, preview bool) (ChannelOperationResult, string, error) {
	var empty ChannelOperationResult
	op, err := raw.normalised()
	if err != nil {
		return empty, "", err
	}
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return empty, "", fmt.Errorf("begin channel operation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var enabled bool
	err = tx.QueryRowContext(ctx, "SELECT enabled FROM notif_channels WHERE id = ?", op.ChannelID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, "", fmt.Errorf("%w: %d", ErrUnknownChannel, op.ChannelID)
	}
	if err != nil {
		return empty, "", fmt.Errorf("look up channel %d: %w", op.ChannelID, err)
	}

	encoded, _ := json.Marshal(op.MonitorIDs)
	rows, err := tx.QueryContext(ctx, `SELECT m.id, mc.channel_id FROM monitors m
		LEFT JOIN monitor_channels mc ON mc.monitor_id = m.id
		WHERE m.id IN (SELECT value FROM json_each(?))
		ORDER BY m.id, mc.channel_id`, string(encoded))
	if err != nil {
		return empty, "", fmt.Errorf("read channel operation: %w", err)
	}
	// Reading every row to the end closes the result set, which frees the
	// transaction's connection for the writes below; the defer covers an
	// early return.
	defer func() { _ = rows.Close() }()
	type linked struct {
		id       int64
		channels []int64
	}
	current := []linked{}
	for rows.Next() {
		var id int64
		var channel sql.NullInt64
		if err := rows.Scan(&id, &channel); err != nil {
			return empty, "", err
		}
		if len(current) == 0 || current[len(current)-1].id != id {
			current = append(current, linked{id: id, channels: []int64{}})
		}
		if channel.Valid {
			last := &current[len(current)-1]
			last.channels = append(last.channels, channel.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return empty, "", err
	}
	if len(current) != len(op.MonitorIDs) {
		return empty, "", fmt.Errorf("selected monitor not found: %w", sql.ErrNoRows)
	}

	// The hash is a validator, not an authorisation token: both routes need
	// write access. JSON encoding of sorted slices keeps it deterministic.
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(op)
	_ = encoder.Encode(enabled)
	for _, row := range current {
		_ = encoder.Encode(row.id)
		_ = encoder.Encode(row.channels)
	}
	etag := fmt.Sprintf(`"channels-%x"`, hash.Sum(nil))
	if !preview && expected != etag {
		return empty, "", ErrChannelPreviewChanged
	}

	result := ChannelOperationResult{Total: len(current), ChannelEnabled: enabled}
	changed := []int64{}
	for _, row := range current {
		has := slices.Contains(row.channels, op.ChannelID)
		if has == (op.Action == "add") {
			continue
		}
		changed = append(changed, row.id)
		if op.Action == "remove" && len(row.channels) == 1 {
			result.LeftWithoutOwn++
		}
	}
	result.Changed = len(changed)
	result.Unchanged = result.Total - result.Changed
	if preview {
		return result, etag, nil
	}

	now := time.Now().Unix()
	for _, id := range changed {
		if op.Action == "add" {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO monitor_channels (monitor_id, channel_id) VALUES (?, ?)", id, op.ChannelID)
		} else {
			_, err = tx.ExecContext(ctx,
				"DELETE FROM monitor_channels WHERE monitor_id = ? AND channel_id = ?", id, op.ChannelID)
		}
		if err != nil {
			return empty, "", fmt.Errorf("%s channel %d on monitor %d: %w", op.Action, op.ChannelID, id, err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE monitors SET updated_at = MAX(?, updated_at + 1) WHERE id = ?", now, id); err != nil {
			return empty, "", fmt.Errorf("bump monitor %d version: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return empty, "", fmt.Errorf("commit channel operation: %w", err)
	}
	return result, etag, nil
}

// ValidChannelPreviewETag accepts only the strong validator a channel
// preview issues.
func ValidChannelPreviewETag(value string) bool {
	const prefix = `"channels-`
	if len(value) != len(prefix)+64+1 || !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return false
	}
	for _, c := range value[len(prefix) : len(value)-1] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
