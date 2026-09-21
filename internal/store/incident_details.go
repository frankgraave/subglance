package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// IncidentDetails pairs incident history with the settings that govern its
// reminder clock. One joined read keeps the timestamp, count and configuration
// consistent without an extra monitor query for every incident. No monitor
// target, headers or other private check configuration is loaded here.
type IncidentDetails struct {
	Incident
	MonitorEnabled bool
	RepeatAfterS   int
	Maintenance    bool
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
	return db.scanIncidentDetails(ctx, rows)
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
	return db.scanIncidentDetails(ctx, rows)
}

func (db *DB) scanIncidentDetails(ctx context.Context, rows *sql.Rows) ([]IncidentDetails, error) {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Release the joined cursor before reading schedules and tags, including
	// installations with a single reader connection.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	windows, err := db.ListMaintenance(ctx)
	if err != nil {
		return nil, fmt.Errorf("read incident maintenance: %w", err)
	}
	now := time.Now()
	active := make([]MaintenanceWindow, 0, len(windows))
	needsTags := false
	for _, w := range windows {
		// Active returns false for malformed recurrence. Validate stored intent
		// first so corruption cannot become a promise of reminder eligibility.
		if err := w.Validate(); err != nil {
			return nil, fmt.Errorf("invalid incident maintenance window %d: %w", w.ID, err)
		}
		if w.Active(now) {
			active = append(active, w)
			needsTags = needsTags || w.TagKey != ""
		}
	}
	var tags map[int64]map[string]string
	if needsTags {
		ids := make([]int64, 0, len(out))
		for _, detail := range out {
			ids = append(ids, detail.MonitorID)
		}
		tags, err = db.tagsForMonitors(ctx, ids)
		if err != nil {
			return nil, err
		}
	}
	for i, detail := range out {
		for _, w := range active {
			if w.MonitorID == detail.MonitorID || (w.TagKey != "" && tags[detail.MonitorID][w.TagKey] == w.TagValue) {
				out[i].Maintenance = true
				break
			}
		}
	}
	return out, nil
}
