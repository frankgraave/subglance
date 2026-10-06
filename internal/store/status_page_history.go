package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StatusHistoryHour is one hour of a monitor's checks, counted the way a
// public status page reads them.
//
// Checks taken during maintenance are counted apart and in no other field,
// because the page excludes them exactly as uptime does.
type StatusHistoryHour struct {
	// Hour is the start of the hour, in UTC.
	Hour time.Time

	// Up, Down and Warning are checks outside maintenance by the state
	// engine's assessment: Down means the check belonged to a confirmed
	// incident, Warning that it failed without one.
	Up      int
	Down    int
	Warning int
	// Unassessed are checks outside maintenance recorded before assessments
	// existed. They prove the monitor was watched, and say nothing else.
	Unassessed  int
	Maintenance int
}

// StatusHistoryHours returns a monitor's checks per hour from since onward,
// oldest first, with hours that saw no check left out.
//
// It reads both halves of the history the way Uptime does: raw heartbeats
// grouped by hour, and the hourly rollup for everything older. The rollup
// deletes the raw rows it absorbs in the same transaction, so the two never
// count one check twice and an hour that holds both simply adds up.
//
// since is truncated to the hour. The raw half is grouped in SQL so a
// 90-day read returns at most one row per hour however often the monitor
// checks.
func (db *DB) StatusHistoryHours(ctx context.Context, monitorID int64, since time.Time) ([]StatusHistoryHour, error) {
	from := since.UTC().Truncate(time.Hour).Unix()
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT bucket, sum(up), sum(down), sum(warn), sum(legacy), sum(maint)
		FROM (
			SELECT bucket,
			       assessed_up AS up, assessed_down AS down, warning_count AS warn,
			       up_count + down_count - assessed_up - assessed_down - warning_count - maintenance_count AS legacy,
			       maintenance_count AS maint
			FROM heartbeat_hourly
			WHERE monitor_id = ? AND bucket >= ?
			UNION ALL
			SELECT ts - ts % 3600,
			       maintenance = 0 AND assessment = 'up', maintenance = 0 AND assessment = 'down',
			       maintenance = 0 AND assessment = 'warning', maintenance = 0 AND assessment = '',
			       maintenance
			FROM heartbeats
			WHERE monitor_id = ? AND ts >= ?
		)
		GROUP BY bucket
		ORDER BY bucket`, monitorID, from, monitorID, from)
	if err != nil {
		return nil, fmt.Errorf("query status history for monitor %d: %w", monitorID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []StatusHistoryHour{}
	for rows.Next() {
		var (
			bucket int64
			h      StatusHistoryHour
		)
		if err := rows.Scan(&bucket, &h.Up, &h.Down, &h.Warning, &h.Unassessed, &h.Maintenance); err != nil {
			return nil, fmt.Errorf("scan status history hour: %w", err)
		}
		h.Hour = time.Unix(bucket, 0).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query status history for monitor %d: %w", monitorID, err)
	}
	return out, nil
}

// IncidentSpan is when one confirmed incident ran. End is zero while the
// incident is still open.
type IncidentSpan struct {
	Start time.Time
	End   time.Time
}

// ConfirmedIncidentSpans returns a monitor's confirmed incidents that were
// still running at or after since, oldest first.
//
// Unconfirmed incidents are left out: a failure that never crossed the
// threshold was never an outage, and a public page only states confirmed
// facts. Certificate notices are left out too: the service answered
// throughout, so there was no outage to list. The span starts at the incident's first failed check, not at its
// confirmation, because that is when the outage began.
func (db *DB) ConfirmedIncidentSpans(ctx context.Context, monitorID int64, since time.Time) ([]IncidentSpan, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT started_at, resolved_at
		FROM incidents
		WHERE monitor_id = ? AND confirmed_at IS NOT NULL AND notice = 0
		  AND (resolved_at IS NULL OR resolved_at >= ?)
		ORDER BY started_at, id`, monitorID, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("query incident spans for monitor %d: %w", monitorID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []IncidentSpan{}
	for rows.Next() {
		var (
			started  int64
			resolved sql.NullInt64
		)
		if err := rows.Scan(&started, &resolved); err != nil {
			return nil, fmt.Errorf("scan incident span: %w", err)
		}
		s := IncidentSpan{Start: time.Unix(started, 0).UTC()}
		if resolved.Valid {
			s.End = time.Unix(resolved.Int64, 0).UTC()
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query incident spans for monitor %d: %w", monitorID, err)
	}
	return out, nil
}
