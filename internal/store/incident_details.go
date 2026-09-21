package store

import (
	"context"
	"database/sql"
	"fmt"
)

// IncidentDetails pairs incident history with the settings that govern its
// reminder clock. One joined read keeps the timestamp, count and configuration
// consistent without an extra monitor query for every incident. No monitor
// target, headers or other private check configuration is loaded here.
type IncidentDetails struct {
	Incident
	MonitorEnabled bool
	RepeatAfterS   int
}

// ListIncidentDetails returns bounded monitor history with reminder settings.
func (db *DB) ListIncidentDetails(ctx context.Context, monitorID int64, limit int) ([]IncidentDetails, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Reader.QueryContext(ctx, `SELECT `+incidentColumns+`, m.enabled, m.repeat_after_s
		FROM incidents JOIN monitors m ON m.id = incidents.monitor_id
		WHERE incidents.monitor_id = ?
		ORDER BY incidents.started_at DESC, incidents.id DESC LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query incident details: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIncidentDetails(rows)
}

// ListOpenIncidentDetails returns every open incident with reminder settings.
func (db *DB) ListOpenIncidentDetails(ctx context.Context) ([]IncidentDetails, error) {
	rows, err := db.Reader.QueryContext(ctx, `SELECT `+incidentColumns+`, m.enabled, m.repeat_after_s
		FROM incidents JOIN monitors m ON m.id = incidents.monitor_id
		WHERE incidents.resolved_at IS NULL
		ORDER BY incidents.started_at DESC, incidents.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query open incident details: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanIncidentDetails(rows)
}

func scanIncidentDetails(rows *sql.Rows) ([]IncidentDetails, error) {
	var out []IncidentDetails
	for rows.Next() {
		var detail IncidentDetails
		inc, err := scanIncidentWith(rows, &detail.MonitorEnabled, &detail.RepeatAfterS)
		if err != nil {
			return nil, err
		}
		detail.Incident = inc
		out = append(out, detail)
	}
	return out, rows.Err()
}
