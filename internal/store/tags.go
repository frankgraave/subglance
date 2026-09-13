package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Tags on a monitor are key/value pairs: `env` -> `prod`, `customer` -> `acme`.
//
// On the wire they are a JSON object rather than a list of `key:value`
// strings. That is what makes the colon question go away instead of being
// answered: the API never splits on `:`, so a value may contain as many colons
// as it likes (`url:https://example.com` is a legal value). The `key:value`
// spelling is an input and display convention for humans, not a parse format.
//
// ONE VALUE PER KEY PER MONITOR, enforced by the primary key of monitor_tags.
// A monitor that is in two environments at once has no place in a grouped
// view, and every consumer would need its own tie-break rule. Several values
// means several keys.
const (
	// MaxTagsPerMonitor is a guard against a caller turning the tag table
	// into free-form storage. Twenty dimensions on one monitor is already
	// well past what a grouped dashboard can show.
	MaxTagsPerMonitor = 20
	maxTagKeyLen      = 32
	maxTagValueLen    = 64
)

// tagKeyPattern keeps keys to something that can be a column header, a URL
// query parameter and a CSS class without escaping: lowercase alphanumerics,
// with dash, underscore and dot allowed inside.
var tagKeyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9_.-]*[a-z0-9])?$`)

// NormaliseTags trims and lowercases keys, trims values, and rejects anything
// that cannot be stored.
//
// Keys are lowercased because `Env` and `env` are the same dimension to
// everyone except a string comparison, and two spellings of one key would
// split a grouped view in half. Values keep their case: `customer:Acme` is a
// name, and flattening it would show up in the UI.
//
// It returns a new map and never mutates its argument, because the API layer
// hands it a map decoded straight from the request body and also reports that
// body back in error messages.
func NormaliseTags(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MaxTagsPerMonitor {
		return nil, fmt.Errorf("at most %d tags per monitor, got %d", MaxTagsPerMonitor, len(in))
	}

	out := make(map[string]string, len(in))
	// Sorted so that the error a caller gets for a body with several
	// problems is the same one every time; map order would make the
	// message depend on the run.
	for _, rawKey := range sortedKeys(in) {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			return nil, fmt.Errorf("tag key must not be empty")
		}
		if utf8.RuneCountInString(key) > maxTagKeyLen {
			return nil, fmt.Errorf("tag key %q is longer than %d characters", key, maxTagKeyLen)
		}
		if !tagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf(
				"tag key %q must be lowercase letters, digits, dash, underscore or dot, and start and end with a letter or digit",
				key)
		}
		// Two keys that normalise to the same thing are a mistake in the
		// request, not an instruction about which one wins. Silently
		// keeping the last would depend on map iteration order.
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("tag key %q appears twice", key)
		}

		value := strings.TrimSpace(in[rawKey])
		if value == "" {
			return nil, fmt.Errorf("tag %q must have a value; a bare tag is not a tag", key)
		}
		if utf8.RuneCountInString(value) > maxTagValueLen {
			return nil, fmt.Errorf("value of tag %q is longer than %d characters", key, maxTagValueLen)
		}
		if strings.ContainsAny(value, "\n\r\t") {
			return nil, fmt.Errorf("value of tag %q must not contain line breaks or tabs", key)
		}
		out[key] = value
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// replaceTags makes the stored tags of one monitor equal to the map given.
//
// Replace rather than merge: the API models tags as one field of the monitor,
// so sending `{"env":"prod"}` means those are the tags, exactly as sending
// `headers` replaces the headers. Merging would leave no way to remove a tag.
func replaceTags(ctx context.Context, ex execer, monitorID int64, tags map[string]string) error {
	if _, err := ex.ExecContext(ctx, "DELETE FROM monitor_tags WHERE monitor_id = ?", monitorID); err != nil {
		return fmt.Errorf("clear tags for monitor %d: %w", monitorID, err)
	}
	for _, key := range sortedKeys(tags) {
		if _, err := ex.ExecContext(ctx,
			"INSERT INTO monitor_tags (monitor_id, key, value) VALUES (?, ?, ?)",
			monitorID, key, tags[key]); err != nil {
			return fmt.Errorf("insert tag %q on monitor %d: %w", key, monitorID, err)
		}
	}
	return nil
}

// execer is the part of *sql.DB and *sql.Tx that the tag writes need, so the
// same code serves a write inside a transaction and a standalone one.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// tagsForMonitors reads the tags of the given monitors in one query.
//
// One query rather than one per monitor: the listing endpoint renders every
// monitor on the dashboard, and a per-monitor read would be a few hundred
// round trips per page load for a handful of rows of data.
func (db *DB) tagsForMonitors(ctx context.Context, ids []int64) (map[int64]map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// The id list is built from rows this package just read, never from
	// user input, and the placeholders are generated rather than the
	// values interpolated.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	// #nosec G202 -- only generated placeholders are concatenated, never values.
	q := "SELECT monitor_id, key, value FROM monitor_tags WHERE monitor_id IN (" + placeholders + ")"
	rows, err := db.Reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query monitor tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]map[string]string, len(ids))
	for rows.Next() {
		var (
			id         int64
			key, value string
		)
		if err := rows.Scan(&id, &key, &value); err != nil {
			return nil, fmt.Errorf("scan monitor tag: %w", err)
		}
		if out[id] == nil {
			out[id] = make(map[string]string, 4)
		}
		out[id][key] = value
	}
	return out, rows.Err()
}
