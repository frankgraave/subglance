package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
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

	// CaptureResponse allows a failed HTTP check to keep the beginning of the
	// response body. Stored as a column so it travels with the monitor into
	// the checker without a second lookup per result.
	CaptureResponse bool

	// RepeatAfterS is how long an unacknowledged, confirmed incident waits
	// before the first reminder. Zero disables reminders for this monitor.
	// The gaps after the first one grow; see state.ReminderGap.
	RepeatAfterS int

	// PushToken is the plaintext push URL token. It is set exactly once, by
	// CreateMonitor, and is never read back from the database — only its
	// hash is stored. Every other code path sees it empty.
	PushToken string

	// PushTokenPrefix is the first few characters of the token, kept in
	// clear so a list can tell two push monitors apart without being able
	// to reconstruct either.
	PushTokenPrefix string

	// PushIntervalS is how often the job is expected to report in, and
	// PushGraceS how late it may be before that silence counts as a
	// failure. Both are zero for every other monitor type.
	PushIntervalS int
	PushGraceS    int

	// Tags are key/value pairs such as `env` -> `prod`. See tags.go for the
	// shape and the normalisation rules; nil and empty mean the same thing.
	Tags map[string]string

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

// monitorColumns is every column a Monitor is scanned from, in the order
// scanMonitor reads them.
//
// push_token_hash is deliberately absent. It is a credential, and a SELECT
// that never asks for it cannot leak it into a log line, an error message or a
// debug dump of a Monitor value.
const monitorColumns = `
	id, name, type, target, interval_s, timeout_s, retries,
	method, expected_status, keyword, keyword_mode, follow_redirects,
	headers_json, body, ssl_warn_days, enabled, capture_response, repeat_after_s,
	push_token_prefix, push_interval_s, push_grace_s,
	created_at, updated_at`

// queryMonitors runs a monitor SELECT with a caller-supplied WHERE clause.
//
// The clause is concatenated into the SQL, so it must never contain anything
// derived from user input. Every caller in this package passes a compile-time
// constant, and that is the rule: a filter that needs a value writes a `?` in
// the constant and passes the value through args, never through formatting.
func (db *DB) queryMonitors(ctx context.Context, where string, args ...any) ([]Monitor, error) {
	// #nosec G202 -- `where` is a package-internal constant, never user input.
	q := "SELECT " + monitorColumns + " FROM monitors " + where + " ORDER BY id"

	rows, err := db.Reader.QueryContext(ctx, q, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Tags come from a second table, read in one query for the whole page
	// rather than one per monitor.
	ids := make([]int64, 0, len(out))
	for _, m := range out {
		ids = append(ids, m.ID)
	}
	tags, err := db.tagsForMonitors(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

// GetMonitor returns one monitor by ID. It returns sql.ErrNoRows when absent.
func (db *DB) GetMonitor(ctx context.Context, id int64) (Monitor, error) {
	q := "SELECT " + monitorColumns + " FROM monitors WHERE id = ?"
	row := db.Reader.QueryRowContext(ctx, q, id)
	m, err := scanMonitor(row)
	if err != nil {
		return Monitor{}, err
	}
	tags, err := db.tagsForMonitors(ctx, []int64{id})
	if err != nil {
		return Monitor{}, err
	}
	m.Tags = tags[id]
	return m, nil
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
		pushPrefix  sql.NullString
		pushEvery   sql.NullInt64
		pushGrace   sql.NullInt64
		created     int64
		updated     int64
	)

	err := s.Scan(
		&m.ID, &m.Name, &m.Type, &m.Target,
		&m.IntervalS, &m.TimeoutS, &m.Retries,
		&m.Method, &m.ExpectedStatus, &keyword, &m.KeywordMode, &m.FollowRedirects,
		&headersJSON, &body, &m.SSLWarnDays, &m.Enabled, &m.CaptureResponse, &m.RepeatAfterS,
		&pushPrefix, &pushEvery, &pushGrace,
		&created, &updated,
	)
	if err != nil {
		return Monitor{}, err
	}

	m.Keyword = keyword.String
	m.Body = body.String
	m.PushTokenPrefix = pushPrefix.String
	m.PushIntervalS = int(pushEvery.Int64)
	m.PushGraceS = int(pushGrace.Int64)
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
	ApplyMonitorDefaults(&m)

	var headersJSON any
	if len(m.Headers) > 0 {
		b, err := json.Marshal(m.Headers)
		if err != nil {
			return Monitor{}, fmt.Errorf("encode headers: %w", err)
		}
		headersJSON = string(b)
	}

	// Monitor row and tag rows go in together: a monitor that briefly exists
	// without the tags it was created with would be shown ungrouped on any
	// dashboard that happened to load in between.
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return Monitor{}, fmt.Errorf("begin create monitor: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A push monitor is issued its token here rather than by the caller, so
	// there is exactly one place in the codebase that decides what a push
	// credential looks like and exactly one moment at which the plaintext
	// exists.
	var tokenHash any
	if m.Type == TypePush {
		token, prefix, err := auth.GeneratePushToken()
		if err != nil {
			return Monitor{}, err
		}
		m.PushToken = token
		m.PushTokenPrefix = prefix
		tokenHash = auth.HashToken(token)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO monitors (
			name, type, target, interval_s, timeout_s, retries,
			method, expected_status, keyword, keyword_mode, follow_redirects,
			headers_json, body, ssl_warn_days, enabled, capture_response, repeat_after_s,
			push_token_hash, push_token_prefix, push_interval_s, push_grace_s,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Type, m.Target, m.IntervalS, m.TimeoutS, m.Retries,
		m.Method, m.ExpectedStatus, nullString(m.Keyword), m.KeywordMode, m.FollowRedirects,
		headersJSON, nullString(m.Body), m.SSLWarnDays, m.Enabled, m.CaptureResponse, m.RepeatAfterS,
		tokenHash, nullString(m.PushTokenPrefix),
		nullInt(m.PushIntervalS), pushGraceValue(m),
		now, now,
	)
	if err != nil {
		return Monitor{}, fmt.Errorf("insert monitor: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Monitor{}, fmt.Errorf("last insert id: %w", err)
	}

	m.ID = id
	if err := replaceTags(ctx, tx, id, m.Tags); err != nil {
		return Monitor{}, err
	}
	if err := tx.Commit(); err != nil {
		return Monitor{}, fmt.Errorf("commit create monitor: %w", err)
	}

	m.CreatedAt = time.Unix(now, 0).UTC()
	m.UpdatedAt = m.CreatedAt
	return m, nil
}

// ApplyMonitorDefaults fills in the same defaults the schema declares, so a
// caller constructing a Monitor in Go gets the same result as a bare INSERT.
//
// Exported because the API needs it for monitors that are never inserted: a
// preview check has to probe with exactly the settings the monitor would get
// once saved, and a second copy of these numbers in the API package would
// drift from this one the first time a default changed.
func ApplyMonitorDefaults(m *Monitor) {
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
//
// updated_at is forced to advance rather than simply set to now: it is the
// version stamp behind ETag/If-Match, and at second resolution a pause in the
// same second as a previous write would leave the stamp equal and let a stale
// conditional PATCH through.
func (db *DB) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET enabled = ?, updated_at = MAX(?, updated_at + 1) WHERE id = ?",
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

	// Response is the captured failure response, nil when there is none.
	// ListHeartbeats fills it in; the bulk beat-bar read deliberately does
	// not, because that view never shows it.
	Response *ResponseSnapshot
}

// ResponseSnapshot is the beginning of a failed response, kept for diagnosis.
//
// It lives in its own table rather than as columns on heartbeats: the beat bar
// reads the newest N heartbeats for every monitor in one query, and a 2 KiB
// text column would ride along on every one of those rows for a view that
// never shows it.
type ResponseSnapshot struct {
	Body      string
	Headers   map[string]string
	Truncated bool
}

// RecordHeartbeat stores one check result, and its response snapshot when the
// result carries one.
//
// Both rows go in one transaction. A snapshot without its heartbeat is
// impossible anyway (the foreign key forbids it), but a committed heartbeat
// whose snapshot write failed would show the UI a failure that claims to have
// captured a response and then has none.
func (db *DB) RecordHeartbeat(ctx context.Context, hb Heartbeat) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error)
		VALUES (?, ?, ?, ?, ?, ?)`,
		hb.MonitorID, hb.TS.Unix(), hb.OK,
		nullInt(hb.LatencyMS), nullInt(hb.StatusCode), nullString(hb.Error),
	)
	if err != nil {
		return fmt.Errorf("insert heartbeat for monitor %d: %w", hb.MonitorID, err)
	}

	if hb.Response != nil {
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("heartbeat id for monitor %d: %w", hb.MonitorID, err)
		}
		var headersJSON any
		if len(hb.Response.Headers) > 0 {
			b, err := json.Marshal(hb.Response.Headers)
			if err != nil {
				return fmt.Errorf("encode response headers: %w", err)
			}
			headersJSON = string(b)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO heartbeat_responses (heartbeat_id, body, headers_json, truncated)
			VALUES (?, ?, ?, ?)`,
			id, hb.Response.Body, headersJSON, hb.Response.Truncated,
		); err != nil {
			return fmt.Errorf("insert response snapshot for monitor %d: %w", hb.MonitorID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record heartbeat: %w", err)
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

// ListHeartbeats returns the most recent heartbeats for a monitor, newest
// first, with the captured response attached to every failure that has one.
func (db *DB) ListHeartbeats(ctx context.Context, monitorID int64, limit int) ([]Heartbeat, error) {
	return db.listHeartbeats(ctx, monitorID, limit, true)
}

// listHeartbeats is the shared body. withResponses is false for callers that
// only need the beat itself: attaching snapshots costs a second query, and the
// monitor listing makes this call once per monitor for a field it never shows.
func (db *DB) listHeartbeats(ctx context.Context, monitorID int64, limit int, withResponses bool) ([]Heartbeat, error) {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if withResponses {
		if err := db.attachResponses(ctx, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attachResponses fills in the snapshot for every heartbeat in the page that
// has one.
//
// One query for the page rather than one per beat: a 1000-beat listing would
// otherwise be 1001 round trips through SQLite for a panel that is collapsed
// by default. Successful checks never have a snapshot, so the WHERE clause
// normally matches a handful of rows out of the thousand asked for.
func (db *DB) attachResponses(ctx context.Context, hbs []Heartbeat) error {
	ids := make([]int64, 0, len(hbs))
	for _, hb := range hbs {
		if !hb.OK {
			ids = append(ids, hb.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	// The ids go in as one JSON array bound to a single parameter, so there
	// is no SQL built by concatenation and no per-page statement to cache.
	encoded, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode heartbeat ids: %w", err)
	}

	rows, err := db.Reader.QueryContext(ctx, `
		SELECT heartbeat_id, body, headers_json, truncated
		FROM heartbeat_responses
		WHERE heartbeat_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return fmt.Errorf("query response snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found := make(map[int64]*ResponseSnapshot, len(ids))
	for rows.Next() {
		var (
			id          int64
			snap        ResponseSnapshot
			headersJSON sql.NullString
		)
		if err := rows.Scan(&id, &snap.Body, &headersJSON, &snap.Truncated); err != nil {
			return fmt.Errorf("scan response snapshot: %w", err)
		}
		if headersJSON.Valid && headersJSON.String != "" {
			if err := json.Unmarshal([]byte(headersJSON.String), &snap.Headers); err != nil {
				// A malformed stored header map must not blank the body it
				// belongs to; the body is the part worth reading.
				snap.Headers = nil
			}
		}
		found[id] = &snap
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("query response snapshots: %w", err)
	}

	for i := range hbs {
		if snap, present := found[hbs[i].ID]; present {
			hbs[i].Response = snap
		}
	}
	return nil
}

// CountSnapshotsSince reports how many failure-response snapshots a monitor has
// stored since a given moment.
//
// The runner needs this at startup. Its per-outage snapshot budget lives in
// memory on the failure streak, so a restart during a long outage would hand
// the monitor a fresh budget and write the same error page again — every
// restart, for as long as the outage lasts. Counting what is already on disk
// for the open incident lets the budget survive the process that spent it.
//
// The count is capped by the caller's limit so an outage that predates the
// budget cannot make startup read an unbounded number of rows.
func (db *DB) CountSnapshotsSince(ctx context.Context, monitorID int64, since time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var n int
	err := db.Reader.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT 1
			FROM heartbeat_responses r
			JOIN heartbeats h ON h.id = r.heartbeat_id
			WHERE h.monitor_id = ? AND h.ts >= ?
			LIMIT ?
		)`, monitorID, since.Unix(), limit).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count snapshots for monitor %d: %w", monitorID, err)
	}
	return n, nil
}

// CountFailedHeartbeatsSince reports how many failed heartbeats a monitor has
// recorded since a given moment.
//
// The runner needs this at startup to rebuild the alert streak of an incident
// that is open but not yet confirmed. The snapshot count cannot stand in for
// it: a monitor with response capture disabled fails without ever writing a
// snapshot, so a restart would restore a streak of zero and the incident would
// have to start counting towards its failure threshold again.
//
// The count is capped by the caller's limit, so a long outage cannot make
// startup read an unbounded number of rows. Only the distance to the failure
// threshold is meaningful, and that is a small number.
func (db *DB) CountFailedHeartbeatsSince(ctx context.Context, monitorID int64, since time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var n int
	err := db.Reader.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT 1
			FROM heartbeats
			WHERE monitor_id = ? AND ts >= ? AND ok = 0
			LIMIT ?
		)`, monitorID, since.Unix(), limit).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count failed heartbeats for monitor %d: %w", monitorID, err)
	}
	return n, nil
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
	hbs, err := db.listHeartbeats(ctx, monitorID, 1, false)
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

// ErrVersionConflict reports that a conditional update was refused because the
// row had already moved on. The API layer maps it to 412 Precondition Failed.
var ErrVersionConflict = errors.New("monitor was modified by someone else")

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
//
// This is the unconditional, last-write-wins path. Use
// UpdateMonitorIfUnchanged when the caller has a version to defend.
func (db *DB) UpdateMonitor(ctx context.Context, m Monitor) (Monitor, error) {
	return db.updateMonitor(ctx, m, nil)
}

// UpdateMonitorIfUnchanged writes the monitor only while its stored updated_at
// is one of the accepted versions, and returns ErrVersionConflict otherwise.
//
// The comparison happens inside the UPDATE statement, not in Go. A read of
// updated_at followed by a compare and a write is the very race this guards
// against: two editors could both read the same stamp, both find it equal, and
// both write. Letting SQLite match on the old value makes the check and the
// write a single atomic step.
//
// accepted takes a set because If-Match may offer several entity tags, and any
// one of them matching is enough. An empty set can never match and is refused
// without touching the database.
func (db *DB) UpdateMonitorIfUnchanged(ctx context.Context, m Monitor, accepted []time.Time) (Monitor, error) {
	if len(accepted) == 0 {
		return Monitor{}, ErrVersionConflict
	}
	stamps := make([]int64, 0, len(accepted))
	for _, t := range accepted {
		stamps = append(stamps, t.Unix())
	}
	return db.updateMonitor(ctx, m, stamps)
}

// updateMonitor is the shared body of the conditional and unconditional
// updates. An empty expected means no version check.
func (db *DB) updateMonitor(ctx context.Context, m Monitor, expected []int64) (Monitor, error) {
	ApplyMonitorDefaults(&m)

	// The stamp must strictly advance, because it doubles as the version a
	// caller compares against. Second resolution means two edits inside the
	// same second would otherwise leave updated_at unchanged, and a stale
	// If-Match from before the first edit would sail through the second —
	// silently reintroducing the overwrite this whole mechanism prevents.
	//
	// The guarantee belongs in the statement, not here. This value is only a
	// floor; `updated_at = MAX(?, updated_at + 1)` compares it against the
	// row as stored, so two writers working from the same snapshot still get
	// different stamps. Deciding it in Go would compare against a snapshot
	// that a concurrent writer may already have superseded.
	next := time.Now().Unix()

	var headersJSON any
	if len(m.Headers) > 0 {
		b, err := json.Marshal(m.Headers)
		if err != nil {
			return Monitor{}, fmt.Errorf("encode headers: %w", err)
		}
		headersJSON = string(b)
	}

	// The token columns are absent on purpose: an update writes everything
	// mutable, and a push token is not. Rotating one is a separate,
	// deliberate act, not something a PATCH of the name should be able to do
	// by accident — the old URL would stop working the moment it happened,
	// silently, in whatever script holds it.
	args := []any{
		m.Name, m.Type, m.Target, m.IntervalS, m.TimeoutS, m.Retries,
		m.Method, m.ExpectedStatus, nullString(m.Keyword), m.KeywordMode,
		m.FollowRedirects, headersJSON, nullString(m.Body), m.SSLWarnDays,
		m.Enabled, m.CaptureResponse, m.RepeatAfterS,
		nullInt(m.PushIntervalS), pushGraceValue(m),
		next, m.ID,
	}

	// Both variants are compile-time constants. An IN list sized to the
	// caller's slice would mean building SQL by concatenation; passing the
	// accepted versions as one JSON array keeps them a bound parameter, so
	// there is no string-built query to get wrong.
	q := updateMonitorSQL
	if len(expected) > 0 {
		encoded, err := json.Marshal(expected)
		if err != nil {
			return Monitor{}, fmt.Errorf("encode accepted versions: %w", err)
		}
		q = updateMonitorIfUnchangedSQL
		args = append(args, string(encoded))
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return Monitor{}, fmt.Errorf("begin update monitor %d: %w", m.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return Monitor{}, fmt.Errorf("update monitor %d: %w", m.ID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return Monitor{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// With a version predicate in play, "no row matched" cannot be
		// attributed to a missing id without a second query whose answer
		// would already be out of date. A monitor deleted underneath a
		// conditional write is genuinely no longer in the state the caller
		// based its edit on, so reporting a conflict is the honest answer.
		if len(expected) > 0 {
			return Monitor{}, ErrVersionConflict
		}
		return Monitor{}, sql.ErrNoRows
	}

	// The statement decides the final stamp, not this function: MAX(?, +1)
	// may have picked the row's own value when a concurrent writer got there
	// first. Reading it back keeps the returned monitor — and therefore the
	// ETag the caller hands out — equal to what is actually stored.
	// Tags are replaced, not merged: the API models them as one field of the
	// monitor, exactly like headers, so there has to be a way to remove one.
	if err := replaceTags(ctx, tx, m.ID, m.Tags); err != nil {
		return Monitor{}, err
	}

	var stored int64
	if err := tx.QueryRowContext(ctx,
		`SELECT updated_at FROM monitors WHERE id = ?`, m.ID).Scan(&stored); err != nil {
		return Monitor{}, fmt.Errorf("read back updated_at for monitor %d: %w", m.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return Monitor{}, fmt.Errorf("commit update monitor %d: %w", m.ID, err)
	}

	m.UpdatedAt = time.Unix(stored, 0).UTC()
	return m, nil
}

// updateMonitorSetClause is shared by the conditional and unconditional
// updates so the two can never drift into writing different columns.
const updateMonitorSetClause = `
	UPDATE monitors SET
		name = ?, type = ?, target = ?, interval_s = ?, timeout_s = ?, retries = ?,
		method = ?, expected_status = ?, keyword = ?, keyword_mode = ?,
		follow_redirects = ?, headers_json = ?, body = ?, ssl_warn_days = ?,
		enabled = ?, capture_response = ?, repeat_after_s = ?,
		push_interval_s = ?, push_grace_s = ?,
		updated_at = MAX(?, updated_at + 1)
	WHERE id = ?`

const updateMonitorSQL = updateMonitorSetClause

// updateMonitorIfUnchangedSQL additionally requires the row to still carry one
// of the accepted versions, which arrive as a JSON array in a single bound
// parameter. json_each expands it inside SQLite, so the check and the write
// remain one atomic statement.
const updateMonitorIfUnchangedSQL = updateMonitorSetClause +
	` AND updated_at IN (SELECT value FROM json_each(?))`
