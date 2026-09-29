package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrConfigKeyTaken is returned when a configuration key is already held by
// another object of the same kind.
var ErrConfigKeyTaken = errors.New("store: configuration key is already in use")

// Both kinds share one implementation; the queries differ only in table and
// column, which are compile-time constants below and never caller input.
type configKeyTable struct {
	list, set string
}

var (
	monitorKeyTable = configKeyTable{
		list: "SELECT monitor_id, key FROM monitor_config_keys",
		set: `INSERT INTO monitor_config_keys (monitor_id, key) VALUES (?, ?)
		      ON CONFLICT (monitor_id) DO UPDATE SET key = excluded.key`,
	}
	channelKeyTable = configKeyTable{
		list: "SELECT channel_id, key FROM channel_config_keys",
		set: `INSERT INTO channel_config_keys (channel_id, key) VALUES (?, ?)
		      ON CONFLICT (channel_id) DO UPDATE SET key = excluded.key`,
	}
)

// MonitorConfigKeys returns the stored configuration key of every monitor that
// has one, by monitor id. See migration 0023 for what a key is for.
func (db *DB) MonitorConfigKeys(ctx context.Context) (map[int64]string, error) {
	return db.configKeys(ctx, monitorKeyTable)
}

// ChannelConfigKeys is MonitorConfigKeys for notification channels.
func (db *DB) ChannelConfigKeys(ctx context.Context) (map[int64]string, error) {
	return db.configKeys(ctx, channelKeyTable)
}

// SetMonitorConfigKey stores or replaces a monitor's configuration key. It
// reports ErrConfigKeyTaken when another monitor holds the key and
// ErrNotFound when the monitor does not exist.
func (db *DB) SetMonitorConfigKey(ctx context.Context, monitorID int64, key string) error {
	return db.setConfigKey(ctx, monitorKeyTable, "monitor", monitorID, key)
}

// SetChannelConfigKey is SetMonitorConfigKey for notification channels.
func (db *DB) SetChannelConfigKey(ctx context.Context, channelID int64, key string) error {
	return db.setConfigKey(ctx, channelKeyTable, "channel", channelID, key)
}

func (db *DB) configKeys(ctx context.Context, t configKeyTable) (map[int64]string, error) {
	rows, err := db.Reader.QueryContext(ctx, t.list)
	if err != nil {
		return nil, fmt.Errorf("list configuration keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]string{}
	for rows.Next() {
		var (
			id  int64
			key string
		)
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan configuration key: %w", err)
		}
		out[id] = key
	}
	return out, rows.Err()
}

func (db *DB) setConfigKey(ctx context.Context, t configKeyTable, what string, id int64, key string) error {
	_, err := db.Writer.ExecContext(ctx, t.set, id, key)
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err):
		return fmt.Errorf("%w: %s key %q", ErrConfigKeyTaken, what, key)
	case isForeignKeyViolation(err):
		return fmt.Errorf("%w: %s %d", ErrNotFound, what, id)
	default:
		return fmt.Errorf("set %s %d configuration key: %w", what, id, err)
	}
}

// isForeignKeyViolation reports whether err is SQLite refusing a row whose
// parent does not exist. Matched on the message, like isUniqueViolation, so
// the check does not depend on one driver's error type.
func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "foreign key constraint")
}
