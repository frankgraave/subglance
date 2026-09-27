package store

import (
	"context"
	"fmt"
)

// ResetCounts is what ResetInstance removed, table by table. Heartbeats,
// incidents, tags and queued notifications are not counted: they go with
// their monitor or channel by cascade, and counting them first would mean
// reading the largest tables in the database only to report a number.
type ResetCounts struct {
	Monitors           int64
	Channels           int64
	APITokens          int64
	MaintenanceWindows int64
}

// ResetInstance deletes every monitor, notification channel, maintenance
// window and API token, and with them (by ON DELETE CASCADE) all heartbeats,
// hourly summaries, incidents, tags, stored failure responses and queued
// notifications.
//
// It keeps user accounts, sessions and settings. The caller stays signed in,
// the other accounts keep working, and the retention windows an operator
// chose survive: a reset empties what is monitored, not who may sign in or
// how the instance is configured.
//
// Everything happens in one transaction, so a failure part-way leaves the
// instance exactly as it was rather than half emptied.
func (db *DB) ResetInstance(ctx context.Context) (ResetCounts, error) {
	var counts ResetCounts
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return counts, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Maintenance windows are deleted outright: one chosen by tag names no
	// monitor, so it would outlive the monitors it was written for and apply
	// to the first new monitor that happened to carry the same tag.
	steps := []struct {
		stmt string
		n    *int64
	}{
		{"DELETE FROM maintenance_windows WHERE true", &counts.MaintenanceWindows},
		{"DELETE FROM monitors WHERE true", &counts.Monitors},
		{"DELETE FROM notif_channels WHERE true", &counts.Channels},
		{"DELETE FROM api_tokens WHERE true", &counts.APITokens},
	}
	for _, step := range steps {
		res, err := tx.ExecContext(ctx, step.stmt)
		if err != nil {
			return ResetCounts{}, fmt.Errorf("reset: %s: %w", step.stmt, err)
		}
		if *step.n, err = res.RowsAffected(); err != nil {
			return ResetCounts{}, fmt.Errorf("reset: %s: %w", step.stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ResetCounts{}, fmt.Errorf("commit: %w", err)
	}
	return counts, nil
}
