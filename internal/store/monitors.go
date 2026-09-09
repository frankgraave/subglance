package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Monitor is a stored monitor definition.
type Monitor struct {
	ID     int64
	Name   string
	Type   string
	Target string

	IntervalS int
	TimeoutS  int
	Retries   int

	Method          string
	ExpectedStatus  string
	Keyword         string
	KeywordMode     string
	FollowRedirects bool
	Headers         map[string]string
	Body            string

	SSLWarnDays int
	Enabled     bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListMonitors returns all monitors, newest first.
func (db *DB) ListMonitors(ctx context.Context) ([]Monitor, error) {
	return db.queryMonitors(ctx, "")
}

// ListEnabledMonitors returns only monitors that are enabled, which is what the
// scheduler runs.
func (db *DB) ListEnabledMonitors(ctx context.Context) ([]Monitor, error) {
	return db.queryMonitors(ctx, "WHERE enabled = 1")
}

const monitorColumns = `
	id, name, type, target, interval_s, timeout_s, retries,
	method, expected_status, keyword, keyword_mode, follow_redirects,
	headers_json, body, ssl_warn_days, enabled, created_at, updated_at`

// queryMonitors runs a monitor SELECT with a caller-supplied WHERE clause.
//
// The clause is concatenated into the SQL, so it must never contain anything
// derived from user input. Every caller in this package passes a compile-time
// constant, and that is the rule: if a filter ever needs a value, it takes a
// bound parameter rather than string formatting.
func (db *DB) queryMonitors(ctx context.Context, where string) ([]Monitor, error) {
	// #nosec G202 -- `where` is a package-internal constant, never user input.
	q := "SELECT " + monitorColumns + " FROM monitors " + where + " ORDER BY id"

	rows, err := db.Reader.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMonitor returns one monitor by ID. It returns sql.ErrNoRows when absent.
func (db *DB) GetMonitor(ctx context.Context, id int64) (Monitor, error) {
	q := "SELECT " + monitorColumns + " FROM monitors WHERE id = ?"
	row := db.Reader.QueryRowContext(ctx, q, id)
	return scanMonitor(row)
}

// scanner covers both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(s scanner) (Monitor, error) {
	var (
		m           Monitor
		keyword     sql.NullString
		headersJSON sql.NullString
		body        sql.NullString
		created     int64
		updated     int64
	)

	err := s.Scan(
		&m.ID, &m.Name, &m.Type, &m.Target,
		&m.IntervalS, &m.TimeoutS, &m.Retries,
		&m.Method, &m.ExpectedStatus, &keyword, &m.KeywordMode, &m.FollowRedirects,
		&headersJSON, &body, &m.SSLWarnDays, &m.Enabled, &created, &updated,
	)
	if err != nil {
		return Monitor{}, err
	}

	m.Keyword = keyword.String
	m.Body = body.String
	m.CreatedAt = time.Unix(created, 0).UTC()
	m.UpdatedAt = time.Unix(updated, 0).UTC()

	if headersJSON.Valid && headersJSON.String != "" {
		if err := json.Unmarshal([]byte(headersJSON.String), &m.Headers); err != nil {
			// Malformed stored headers must not take out the whole listing:
			// one bad row would otherwise blank the entire dashboard.
			m.Headers = nil
		}
	}
	return m, nil
}

// CreateMonitor inserts a monitor and returns it with its assigned ID.
func (db *DB) CreateMonitor(ctx context.Context, m Monitor) (Monitor, error) {
	now := time.Now().Unix()
	applyMonitorDefaults(&m)

	var headersJSON any
	if len(m.Headers) > 0 {
		b, err := json.Marshal(m.Headers)
		if err != nil {
			return Monitor{}, fmt.Errorf("encode headers: %w", err)
		}
		headersJSON = string(b)
	}

	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (
			name, type, target, interval_s, timeout_s, retries,
			method, expected_status, keyword, keyword_mode, follow_redirects,
			headers_json, body, ssl_warn_days, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Type, m.Target, m.IntervalS, m.TimeoutS, m.Retries,
		m.Method, m.ExpectedStatus, nullString(m.Keyword), m.KeywordMode, m.FollowRedirects,
		headersJSON, nullString(m.Body), m.SSLWarnDays, m.Enabled, now, now,
	)
	if err != nil {
		return Monitor{}, fmt.Errorf("insert monitor: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Monitor{}, fmt.Errorf("last insert id: %w", err)
	}

	m.ID = id
	m.CreatedAt = time.Unix(now, 0).UTC()
	m.UpdatedAt = m.CreatedAt
	return m, nil
}

// applyMonitorDefaults fills in the same defaults the schema declares, so a
// caller constructing a Monitor in Go gets the same result as a bare INSERT.
func applyMonitorDefaults(m *Monitor) {
	if m.IntervalS == 0 {
		m.IntervalS = 60
	}
	if m.TimeoutS == 0 {
		m.TimeoutS = 10
	}
	if m.Method == "" {
		m.Method = "GET"
	}
	if m.ExpectedStatus == "" {
		m.ExpectedStatus = "200-299"
	}
	if m.KeywordMode == "" {
		m.KeywordMode = "absent_ok"
	}
	if m.SSLWarnDays == 0 {
		m.SSLWarnDays = 14
	}
}

// DeleteMonitor removes a monitor and, through ON DELETE CASCADE, its
// heartbeats and incidents.
func (db *DB) DeleteMonitor(ctx context.Context, id int64) error {
	_, err := db.Writer.ExecContext(ctx, "DELETE FROM monitors WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete monitor %d: %w", id, err)
	}
	return nil
}

// SetMonitorEnabled pauses or resumes a monitor.
func (db *DB) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET enabled = ?, updated_at = ? WHERE id = ?",
		enabled, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("set monitor %d enabled=%v: %w", id, enabled, err)
	}
	return nil
}

// Heartbeat is one recorded check result.
type Heartbeat struct {
	ID         int64
	MonitorID  int64
	TS         time.Time
	OK         bool
	LatencyMS  int
	StatusCode int
	Error      string
}

// RecordHeartbeat stores one check result.
func (db *DB) RecordHeartbeat(ctx context.Context, hb Heartbeat) error {
	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error)
		VALUES (?, ?, ?, ?, ?, ?)`,
		hb.MonitorID, hb.TS.Unix(), hb.OK,
		nullInt(hb.LatencyMS), nullInt(hb.StatusCode), nullString(hb.Error),
	)
	if err != nil {
		return fmt.Errorf("insert heartbeat for monitor %d: %w", hb.MonitorID, err)
	}
	return nil
}

// ListHeartbeats returns the most recent heartbeats for a monitor, newest first.
func (db *DB) ListHeartbeats(ctx context.Context, monitorID int64, limit int) ([]Heartbeat, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := db.Reader.QueryContext(ctx, `
		SELECT id, monitor_id, ts, ok, latency_ms, status_code, error
		FROM heartbeats
		WHERE monitor_id = ?
		ORDER BY ts DESC
		LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query heartbeats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Heartbeat
	for rows.Next() {
		var (
			hb      Heartbeat
			ts      int64
			latency sql.NullInt64
			status  sql.NullInt64
			errText sql.NullString
		)
		if err := rows.Scan(&hb.ID, &hb.MonitorID, &ts, &hb.OK, &latency, &status, &errText); err != nil {
			return nil, err
		}
		hb.TS = time.Unix(ts, 0).UTC()
		hb.LatencyMS = int(latency.Int64)
		hb.StatusCode = int(status.Int64)
		hb.Error = errText.String
		out = append(out, hb)
	}
	return out, rows.Err()
}

// UptimeStats summarises a monitor over a window.
type UptimeStats struct {
	Total      int
	Up         int
	Down       int
	Percentage float64
	AvgLatency int
}

// Uptime computes availability for a monitor over the given window.
func (db *DB) Uptime(ctx context.Context, monitorID int64, window time.Duration) (UptimeStats, error) {
	since := time.Now().Add(-window).Unix()

	var (
		stats   UptimeStats
		upCount sql.NullInt64
		avgLat  sql.NullFloat64
	)
	err := db.Reader.QueryRowContext(ctx, `
		SELECT count(*), sum(ok), avg(latency_ms)
		FROM heartbeats
		WHERE monitor_id = ? AND ts >= ?`, monitorID, since,
	).Scan(&stats.Total, &upCount, &avgLat)
	if err != nil {
		return UptimeStats{}, fmt.Errorf("compute uptime for monitor %d: %w", monitorID, err)
	}

	stats.Up = int(upCount.Int64)
	stats.Down = stats.Total - stats.Up
	stats.AvgLatency = int(avgLat.Float64)
	if stats.Total > 0 {
		stats.Percentage = float64(stats.Up) / float64(stats.Total) * 100
	}
	return stats, nil
}

// LatestHeartbeat returns the most recent heartbeat for a monitor.
// It returns sql.ErrNoRows when the monitor has never been checked.
func (db *DB) LatestHeartbeat(ctx context.Context, monitorID int64) (Heartbeat, error) {
	hbs, err := db.ListHeartbeats(ctx, monitorID, 1)
	if err != nil {
		return Heartbeat{}, err
	}
	if len(hbs) == 0 {
		return Heartbeat{}, sql.ErrNoRows
	}
	return hbs[0], nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}
