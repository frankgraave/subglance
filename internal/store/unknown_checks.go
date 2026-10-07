package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UnknownCheck is the latest check of a monitor that could not find out:
// a domain monitor whose registry runs no RDAP, or whose RDAP server did not
// answer. It is kept apart from heartbeats because it is not evidence about
// the target; see migration 0030.
type UnknownCheck struct {
	MonitorID int64
	At        time.Time
	Reason    string
}

// RecordUnknownCheck stores a monitor's latest check that could not find out,
// replacing the one before it.
func (db *DB) RecordUnknownCheck(ctx context.Context, c UnknownCheck) error {
	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitor_unknown_checks (monitor_id, ts, reason) VALUES (?, ?, ?)
		ON CONFLICT (monitor_id) DO UPDATE SET ts = excluded.ts, reason = excluded.reason`,
		c.MonitorID, c.At.Unix(), c.Reason)
	if err != nil {
		return fmt.Errorf("record unknown check for monitor %d: %w", c.MonitorID, err)
	}
	return nil
}

// ClearUnknownCheck removes a monitor's unknown check, once a later check has
// found out. Nothing to remove is not an error.
func (db *DB) ClearUnknownCheck(ctx context.Context, monitorID int64) error {
	if _, err := db.Writer.ExecContext(ctx,
		`DELETE FROM monitor_unknown_checks WHERE monitor_id = ?`, monitorID); err != nil {
		return fmt.Errorf("clear unknown check for monitor %d: %w", monitorID, err)
	}
	return nil
}

// LatestUnknownCheck returns a monitor's latest check that could not find
// out, and false when it has none or a later check has found out since.
func (db *DB) LatestUnknownCheck(ctx context.Context, monitorID int64) (UnknownCheck, bool, error) {
	var (
		ts     int64
		reason string
	)
	err := db.Reader.QueryRowContext(ctx,
		`SELECT ts, reason FROM monitor_unknown_checks WHERE monitor_id = ?`, monitorID).Scan(&ts, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return UnknownCheck{}, false, nil
	}
	if err != nil {
		return UnknownCheck{}, false, fmt.Errorf("read unknown check for monitor %d: %w", monitorID, err)
	}
	return UnknownCheck{MonitorID: monitorID, At: time.Unix(ts, 0).UTC(), Reason: reason}, true, nil
}
