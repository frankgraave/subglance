package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	MonitorTags    map[string]string
}

// Aggregate tags inside the joined read so one incident remains one row, even
// with multiple tags. This loads no private monitor check configuration and
// introduces no per-incident database round trip.
const incidentReminderSettings = `m.enabled, m.repeat_after_s,
	(SELECT json_group_object(key, value) FROM monitor_tags WHERE monitor_id = m.id)`

// ListIncidentDetails returns bounded monitor history with reminder settings.
func (db *DB) ListIncidentDetails(ctx context.Context, monitorID int64, limit int) ([]IncidentDetails, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Reader.QueryContext(ctx, `SELECT `+incidentColumns+`, `+incidentReminderSettings+`
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
	rows, err := db.Reader.QueryContext(ctx, `SELECT `+incidentColumns+`, `+incidentReminderSettings+`
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
		var tags string
		inc, err := scanIncidentWith(rows, &detail.MonitorEnabled, &detail.RepeatAfterS, &tags)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tags), &detail.MonitorTags); err != nil {
			return nil, fmt.Errorf("decode incident monitor tags: %w", err)
		}
		detail.Incident = inc
		out = append(out, detail)
	}
	return out, rows.Err()
}
