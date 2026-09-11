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

// heartbeatColumns is the column list every heartbeat scan expects, in the
// order scanHeartbeat reads them.
const heartbeatColumns = `id, monitor_id, ts, ok, latency_ms, status_code, error`

// maxHeartbeatsPerQuery caps how many heartbeats a single read may return per
// monitor, so a client cannot ask the server to materialise the whole table.
const maxHeartbeatsPerQuery = 1000

// defaultHeartbeatLimit is what a caller gets when it expresses no preference.
const defaultHeartbeatLimit = 100

// scanHeartbeat reads one row selected with heartbeatColumns.
//
// latency_ms, status_code and error are nullable because nullInt/nullString
// store "no value" as NULL rather than 0 or "". Reading them through the
// Null* types keeps that absence from becoming a scan error; the Go zero value
// is the agreed representation of "not applicable" on this path.
func scanHeartbeat(s scanner) (Heartbeat, error) {
	var (
		hb      Heartbeat
		ts      int64
		latency sql.NullInt64
		status  sql.NullInt64
		errText sql.NullString
	)
	if err := s.Scan(&hb.ID, &hb.MonitorID, &ts, &hb.OK, &latency, &status, &errText); err != nil {
		return Heartbeat{}, err
	}
	hb.TS = time.Unix(ts, 0).UTC()
	hb.LatencyMS = int(latency.Int64)
	hb.StatusCode = int(status.Int64)
	hb.Error = errText.String
	return hb, nil
}

// ListHeartbeats returns the most recent heartbeats for a monitor, newest first.
func (db *DB) ListHeartbeats(ctx context.Context, monitorID int64, limit int) ([]Heartbeat, error) {
	if limit <= 0 {
		limit = defaultHeartbeatLimit
	}

	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+heartbeatColumns+`
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
		hb, err := scanHeartbeat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// RecentHeartbeatsForAll returns the most recent perMonitor heartbeats for
// every monitor that has any, keyed by monitor id and newest first — the same
// order as ListHeartbeats.
//
// This exists because the dashboard draws a beat bar per monitor. Doing that
// with ListHeartbeats means one query per monitor, and at 200 monitors that is
// 200 round trips through SQLite for a single page load, repeated on every
// refresh. That N+1 is the scaling problem this endpoint has to survive, so
// the whole grid comes out of one query instead.
//
// The window function does the per-monitor slicing inside SQLite: ROW_NUMBER()
// partitioned by monitor_id and ordered by ts DESC numbers each monitor's
// heartbeats independently, and keeping rows with rn <= perMonitor gives the
// newest N of each without a LIMIT per monitor. The
// idx_heartbeats_monitor_ts (monitor_id, ts DESC) index already supplies that
// ordering, so no extra sort is needed.
//
// perMonitor <= 0 falls back to the default; anything larger than
// maxHeartbeatsPerQuery is clamped.
//
// Monitors that have never been checked are simply absent from the map. That
// is deliberate and safe here: the caller renders a missing entry as "never
// checked", which is exactly what an empty slice would mean too.
func (db *DB) RecentHeartbeatsForAll(ctx context.Context, perMonitor int) (map[int64][]Heartbeat, error) {
	if perMonitor <= 0 {
		perMonitor = defaultHeartbeatLimit
	}
	if perMonitor > maxHeartbeatsPerQuery {
		perMonitor = maxHeartbeatsPerQuery
	}

	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+heartbeatColumns+`
		FROM (
			SELECT `+heartbeatColumns+`,
			       ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY ts DESC) AS rn
			FROM heartbeats
		)
		WHERE rn <= ?
		ORDER BY monitor_id, rn`, perMonitor)
	if err != nil {
		return nil, fmt.Errorf("query recent heartbeats for all monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]Heartbeat)
	for rows.Next() {
		hb, err := scanHeartbeat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan heartbeat: %w", err)
		}
		out[hb.MonitorID] = append(out[hb.MonitorID], hb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query recent heartbeats for all monitors: %w", err)
	}
	return out, nil
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
//
// Availability has to span the rollup boundary: raw heartbeats only reach back
// DefaultRawRetention, and everything older lives in heartbeat_hourly. The two
// sources are disjoint — RollupHeartbeats deletes the raw rows in the same
// transaction that writes the bucket — so their counts simply add up.
//
// An hourly bucket that straddles the start of the window is left out rather
// than counted whole or scaled: it holds no timestamps to split on, so any
// split would be a guess. The cost is bounded at one hour, and only for
// windows long enough to reach past the retention horizon.
func (db *DB) Uptime(ctx context.Context, monitorID int64, window time.Duration) (UptimeStats, error) {
	since := time.Now().Add(-window).Unix()

	var (
		stats     UptimeStats
		rawTotal  int
		rawUp     sql.NullInt64
		rawLatSum sql.NullFloat64
		rawLatN   int
	)
	err := db.Reader.QueryRowContext(ctx, `
		SELECT count(*), sum(ok), sum(latency_ms), count(latency_ms)
		FROM heartbeats
		WHERE monitor_id = ? AND ts >= ?`, monitorID, since,
	).Scan(&rawTotal, &rawUp, &rawLatSum, &rawLatN)
	if err != nil {
		return UptimeStats{}, fmt.Errorf("compute uptime for monitor %d: %w", monitorID, err)
	}

	var (
		aggUp     sql.NullInt64
		aggDown   sql.NullInt64
		aggLatSum sql.NullFloat64
		aggLatN   sql.NullInt64
	)
	err = db.Reader.QueryRowContext(ctx, `
		SELECT sum(up_count), sum(down_count),
		       sum(COALESCE(latency_avg, 0) * latency_count), sum(latency_count)
		FROM heartbeat_hourly
		WHERE monitor_id = ? AND bucket >= ?`, monitorID, since,
	).Scan(&aggUp, &aggDown, &aggLatSum, &aggLatN)
	if err != nil {
		return UptimeStats{}, fmt.Errorf("compute uptime for monitor %d: %w", monitorID, err)
	}

	stats.Up = int(rawUp.Int64) + int(aggUp.Int64)
	stats.Down = (rawTotal - int(rawUp.Int64)) + int(aggDown.Int64)
	stats.Total = stats.Up + stats.Down

	latSum := rawLatSum.Float64 + aggLatSum.Float64
	latN := rawLatN + int(aggLatN.Int64)
	if latN > 0 {
		stats.AvgLatency = int(latSum / float64(latN))
	}
	if stats.Total > 0 {
		stats.Percentage = float64(stats.Up) / float64(stats.Total) * 100
	}
	return stats, nil
}

// CheckedMonitorIDs returns the set of monitors that have at least one
// heartbeat.
//
// The scheduler needs this to tell a brand-new monitor from one that is simply
// due later. It is one grouped query rather than a LatestHeartbeat call per
// monitor, because this runs on every reload and an N+1 there would scale with
// the monitor count several times a minute.
func (db *DB) CheckedMonitorIDs(ctx context.Context) (map[int64]struct{}, error) {
	rows, err := db.Reader.QueryContext(ctx,
		`SELECT DISTINCT monitor_id FROM heartbeats`)
	if err != nil {
		return nil, fmt.Errorf("list checked monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan checked monitor id: %w", err)
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list checked monitors: %w", err)
	}
	return ids, nil
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

// UpdateMonitor writes every mutable column of a monitor and returns the row
// as stored, with a refreshed UpdatedAt.
//
// It takes a whole Monitor rather than a set of changed fields: partial-update
// semantics belong to the API layer, which knows which fields the client
// actually sent. Here the rule is simpler — what you pass is what the row
// becomes.
//
// It returns sql.ErrNoRows when the monitor does not exist, so callers can map
// a missing id to 404 without a separate existence query.
func (db *DB) UpdateMonitor(ctx context.Context, m Monitor) (Monitor, error) {
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
		UPDATE monitors SET
			name = ?, type = ?, target = ?, interval_s = ?, timeout_s = ?, retries = ?,
			method = ?, expected_status = ?, keyword = ?, keyword_mode = ?,
			follow_redirects = ?, headers_json = ?, body = ?, ssl_warn_days = ?,
			enabled = ?, updated_at = ?
		WHERE id = ?`,
		m.Name, m.Type, m.Target, m.IntervalS, m.TimeoutS, m.Retries,
		m.Method, m.ExpectedStatus, nullString(m.Keyword), m.KeywordMode,
		m.FollowRedirects, headersJSON, nullString(m.Body), m.SSLWarnDays,
		m.Enabled, now, m.ID,
	)
	if err != nil {
		return Monitor{}, fmt.Errorf("update monitor %d: %w", m.ID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return Monitor{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return Monitor{}, sql.ErrNoRows
	}

	m.UpdatedAt = time.Unix(now, 0).UTC()
	return m, nil
}
