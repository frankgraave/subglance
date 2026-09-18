package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Incident is one period during which a monitor was failing.
//
// An incident is opened on the first failed check, not on confirmation, so its
// StartedAt reflects when the outage actually began rather than when the
// system became sure of it. ConfirmedAt records the later moment — the
// difference between the two is the confirmation delay the user accepted in
// exchange for not being woken by a single dropped packet.
type Incident struct {
	ID        int64
	MonitorID int64

	StartedAt   time.Time
	ConfirmedAt time.Time // zero while unconfirmed
	ResolvedAt  time.Time // zero while ongoing
	AckedAt     time.Time // zero while unacknowledged

	Cause     string
	LastError string

	// RemindedAt is when the last reminder for this incident went out, and
	// ReminderCount how many have gone out. They live on the incident rather
	// than in memory so the escalating schedule survives a restart: without
	// them, restarting during a three-day outage would alert as if the
	// incident were new.
	RemindedAt    time.Time // zero while no reminder has been sent
	ReminderCount int
}

// Confirmed reports whether this incident passed the failure threshold.
func (i Incident) Confirmed() bool { return !i.ConfirmedAt.IsZero() }

// Acked reports whether someone has acknowledged this incident.
func (i Incident) Acked() bool { return !i.AckedAt.IsZero() }

// Resolved reports whether this incident has ended.
func (i Incident) Resolved() bool { return !i.ResolvedAt.IsZero() }

// Duration reports how long the incident lasted, or how long it has been
// running when it is still open.
func (i Incident) Duration() time.Duration {
	if i.ResolvedAt.IsZero() {
		return time.Since(i.StartedAt)
	}
	return i.ResolvedAt.Sub(i.StartedAt)
}

// ErrNoOpenIncident is returned when an operation needs an unresolved incident
// and there is none.
var ErrNoOpenIncident = errors.New("store: no open incident for monitor")

// ErrIncidentAlreadyOpen is returned when opening an incident collides with
// one that is already open for the same monitor.
//
// It is a distinct error because callers should treat it as the benign race it
// usually is — two workers reporting the same monitor at once — rather than as
// a database fault worth logging at error level.
var ErrIncidentAlreadyOpen = errors.New("store: an incident is already open for this monitor")

const incidentColumns = `
	incidents.id, incidents.monitor_id, incidents.started_at, incidents.confirmed_at,
	incidents.resolved_at, incidents.acked_at, incidents.cause, incidents.last_error,
	incidents.reminded_at, incidents.reminder_count`

// OpenIncident creates an unconfirmed incident for a monitor.
//
// The schema carries a partial unique index on (monitor_id) WHERE resolved_at
// IS NULL, so a double-open loses in the database rather than producing two
// incidents and two alerts for one outage. That collision is reported as
// ErrIncidentAlreadyOpen so callers can treat it as the benign race it is.
func (db *DB) OpenIncident(ctx context.Context, monitorID int64, at time.Time, cause, lastError string) (Incident, error) {
	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO incidents (monitor_id, started_at, cause, last_error)
		VALUES (?, ?, ?, ?)`,
		monitorID, at.Unix(), cause, lastError)
	if err != nil {
		// The partial unique index rejected a second open incident. Report it
		// as the specific, expected condition rather than a raw driver error,
		// so callers can distinguish a race from a broken database.
		if isUniqueViolation(err) {
			return Incident{}, fmt.Errorf("%w (monitor %d)", ErrIncidentAlreadyOpen, monitorID)
		}
		return Incident{}, fmt.Errorf("open incident for monitor %d: %w", monitorID, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Incident{}, fmt.Errorf("last insert id: %w", err)
	}

	return Incident{
		ID:        id,
		MonitorID: monitorID,
		StartedAt: at.UTC().Truncate(time.Second),
		Cause:     cause,
		LastError: lastError,
	}, nil
}

// ConfirmIncident marks the open incident for a monitor as confirmed.
//
// It is idempotent: confirming an already-confirmed incident keeps the
// original timestamp, so a restart cannot rewrite history.
func (db *DB) ConfirmIncident(ctx context.Context, monitorID int64, at time.Time, cause, lastError string) error {
	res, err := db.Writer.ExecContext(ctx, `
		UPDATE incidents
		SET confirmed_at = COALESCE(confirmed_at, ?),
		    cause        = ?,
		    last_error   = ?
		WHERE monitor_id = ? AND resolved_at IS NULL`,
		at.Unix(), cause, lastError, monitorID)
	if err != nil {
		return fmt.Errorf("confirm incident for monitor %d: %w", monitorID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNoOpenIncident
	}
	return nil
}

// ResolveIncident closes the open incident for a monitor.
//
// It returns the incident as it was just before resolution, which is what the
// notifier needs: whether it was ever confirmed decides whether an all-clear
// is sent at all.
func (db *DB) ResolveIncident(ctx context.Context, monitorID int64, at time.Time) (Incident, error) {
	inc, err := db.OpenIncidentFor(ctx, monitorID)
	if err != nil {
		return Incident{}, err
	}

	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE incidents SET resolved_at = ? WHERE id = ?", at.Unix(), inc.ID,
	); err != nil {
		return Incident{}, fmt.Errorf("resolve incident %d: %w", inc.ID, err)
	}

	inc.ResolvedAt = at.UTC().Truncate(time.Second)
	return inc, nil
}

// OpenIncidentFor returns the unresolved incident for a monitor, or
// ErrNoOpenIncident.
func (db *DB) OpenIncidentFor(ctx context.Context, monitorID int64) (Incident, error) {
	row := db.Reader.QueryRowContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents WHERE monitor_id = ? AND resolved_at IS NULL",
		monitorID)

	inc, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNoOpenIncident
	}
	if err != nil {
		return Incident{}, fmt.Errorf("read open incident for monitor %d: %w", monitorID, err)
	}
	return inc, nil
}

// UpdateIncidentError refreshes the last error on the open incident without
// touching its lifecycle timestamps. A connection failure that becomes a 500
// is worth recording; it is not worth a new incident.
func (db *DB) UpdateIncidentError(ctx context.Context, monitorID int64, cause, lastError string) error {
	_, err := db.Writer.ExecContext(ctx, `
		UPDATE incidents SET cause = ?, last_error = ?
		WHERE monitor_id = ? AND resolved_at IS NULL`,
		cause, lastError, monitorID)
	if err != nil {
		return fmt.Errorf("update incident error for monitor %d: %w", monitorID, err)
	}
	return nil
}

// AckIncident marks an incident as acknowledged: someone has seen it and is
// working on it. Acknowledgement stops repeat notifications without pretending
// the problem is solved.
func (db *DB) AckIncident(ctx context.Context, incidentID int64, at time.Time) error {
	res, err := db.Writer.ExecContext(ctx,
		"UPDATE incidents SET acked_at = COALESCE(acked_at, ?) WHERE id = ?",
		at.Unix(), incidentID)
	if err != nil {
		return fmt.Errorf("ack incident %d: %w", incidentID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListIncidents returns incidents for a monitor, newest first.
//
// Timestamps have second granularity, so two incidents in the same second are
// common in tests and possible in production. The id tiebreaker keeps
// "newest first" a real guarantee rather than whatever the query planner
// happened to do.
func (db *DB) ListIncidents(ctx context.Context, monitorID int64, limit int) ([]Incident, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.Reader.QueryContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents WHERE monitor_id = ? ORDER BY started_at DESC, id DESC LIMIT ?",
		monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanIncidents(rows)
}

// ListOpenIncidents returns every unresolved incident across all monitors,
// which is what the dashboard's "what is broken right now" view needs.
func (db *DB) ListOpenIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := db.Reader.QueryContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents WHERE resolved_at IS NULL ORDER BY started_at DESC, id DESC")
	if err != nil {
		return nil, fmt.Errorf("query open incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanIncidents(rows)
}

// ResolvedIncidentCursor is a position in the resolved-incident history.
//
// It is the sort key itself — resolution time plus id — rather than an offset.
// An offset into a list that grows at the head is wrong by construction here:
// incidents resolve while somebody is paging, and OFFSET 50 after one new
// resolution shows a row the reader has already seen while hiding the one
// behind it. A keyset asks "everything strictly older than this exact row",
// which stays true no matter what lands above it.
//
// The id half is not decoration. Resolution timestamps have second
// granularity and a recovery sweep can close several incidents in the same
// second, so a cursor carrying only the timestamp would either repeat that
// second's rows or skip past them — an outage missing from the history, which
// is precisely the failure this endpoint was built to end.
type ResolvedIncidentCursor struct {
	ResolvedAt time.Time
	ID         int64
}

// ResolvedIncidentPage is one page of instance-wide resolved history, plus the
// answer to the question the page itself cannot carry: is there more?
//
// HasMore is derived by asking for one row beyond the page and discarding it,
// not by comparing the row count to the limit. A full page and a full page
// that happens to be the last one are indistinguishable otherwise, which is
// exactly the ambiguity the client used to paper over by declaring any full
// page "truncated".
type ResolvedIncidentPage struct {
	Incidents []Incident
	HasMore   bool
	// Next is the cursor to pass for the following page. Zero when HasMore
	// is false, so a caller cannot accidentally page past the end.
	Next ResolvedIncidentCursor
}

// ListResolvedIncidents returns resolved incidents across every monitor,
// newest resolution first, one page at a time.
//
// Filtered on resolved_at rather than started_at, because the question the
// screen asks is "what recovered recently". Filtering on the start date drops
// exactly the long outages a reader most wants to find: an incident that began
// five weeks ago and came back yesterday is the most interesting row on the
// card and the first one a started_at filter deletes.
//
// The window is a half-open interval [since, cursor): since is inclusive so a
// "last 30 days" request keeps an incident that resolved exactly on the
// boundary, and the cursor is exclusive so the row that ended the previous
// page is not repeated at the top of this one.
func (db *DB) ListResolvedIncidents(ctx context.Context, since time.Time, cursor ResolvedIncidentCursor, limit int) (ResolvedIncidentPage, error) {
	if limit <= 0 {
		limit = 50
	}

	// The window's lower bound. Zero means "no lower bound", and 0 is the
	// correct sentinel for that rather than a special case in the SQL: unix
	// epoch is before every incident this table can hold.
	var sinceUnix int64
	if !since.IsZero() {
		sinceUnix = since.Unix()
	}

	/*
	 * The cursor's upper bound, as a sentinel rather than as an optional
	 * clause.
	 *
	 * The obvious shape here is to build the WHERE from a slice of predicates
	 * and join it, and that is a string-concatenated query — gosec flags it,
	 * and it is right to: the habit is what makes injection possible even when
	 * this particular instance is safe. One fixed statement with bounds that
	 * default to "past the newest row" says the same thing with no assembly,
	 * and the planner sees the same keyset walk either way.
	 */
	cursorUnix, cursorID := int64(math.MaxInt64), int64(math.MaxInt64)
	if !cursor.ResolvedAt.IsZero() {
		cursorUnix, cursorID = cursor.ResolvedAt.Unix(), cursor.ID
	}

	rows, err := db.Reader.QueryContext(ctx,
		// The tuple comparison is spelled out because SQLite has no row-value
		// ordering here: strictly older by resolution, or the same second and
		// a lower id. One expression, so the planner can walk
		// idx_incidents_resolved rather than filtering after the sort.
		"SELECT "+incidentColumns+` FROM incidents
		 WHERE incidents.resolved_at IS NOT NULL
		   AND incidents.resolved_at >= ?
		   AND (incidents.resolved_at < ?
		        OR (incidents.resolved_at = ? AND incidents.id < ?))
		 ORDER BY incidents.resolved_at DESC, incidents.id DESC
		 LIMIT ?`,
		// One row beyond the page, so "there is more" is observed rather than
		// inferred from a full page.
		sinceUnix, cursorUnix, cursorUnix, cursorID, limit+1)
	if err != nil {
		return ResolvedIncidentPage{}, fmt.Errorf("query resolved incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found, err := scanIncidents(rows)
	if err != nil {
		return ResolvedIncidentPage{}, err
	}

	page := ResolvedIncidentPage{Incidents: found}
	if len(found) > limit {
		last := found[limit-1]
		page.Incidents = found[:limit]
		page.HasMore = true
		page.Next = ResolvedIncidentCursor{ResolvedAt: last.ResolvedAt, ID: last.ID}
	}
	return page, nil
}

// isUniqueViolation reports whether an error came from a UNIQUE constraint.
//
// The pure-Go SQLite driver does not export a typed error for this, so the
// message is the only signal available. It is matched loosely on purpose: the
// alternative is depending on a driver-internal error code that would break on
// upgrade, and a false negative here only costs a less specific error message.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "constraint failed: unique")
}

func scanIncidents(rows *sql.Rows) ([]Incident, error) {
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func scanIncident(s scanner) (Incident, error) {
	return scanIncidentWith(s)
}

// scanIncidentWith reads an incident row, plus any extra destinations a joined
// query appended after the incident columns.
func scanIncidentWith(s scanner, extra ...any) (Incident, error) {
	var (
		inc       Incident
		started   int64
		confirmed sql.NullInt64
		resolved  sql.NullInt64
		acked     sql.NullInt64
		reminded  sql.NullInt64
	)

	dest := []any{
		&inc.ID, &inc.MonitorID, &started,
		&confirmed, &resolved, &acked,
		&inc.Cause, &inc.LastError,
		&reminded, &inc.ReminderCount,
	}
	err := s.Scan(append(dest, extra...)...)
	if err != nil {
		return Incident{}, err
	}

	inc.StartedAt = time.Unix(started, 0).UTC()
	if confirmed.Valid {
		inc.ConfirmedAt = time.Unix(confirmed.Int64, 0).UTC()
	}
	if resolved.Valid {
		inc.ResolvedAt = time.Unix(resolved.Int64, 0).UTC()
	}
	if acked.Valid {
		inc.AckedAt = time.Unix(acked.Int64, 0).UTC()
	}
	if reminded.Valid {
		inc.RemindedAt = time.Unix(reminded.Int64, 0).UTC()
	}
	return inc, nil
}

// RecordReminder stamps an incident with the reminder that just went out.
//
// The count is incremented in SQL rather than written from a value the caller
// read earlier: two paths can reach this (the reminder ticker and a check that
// lands at the same moment), and a read-modify-write would let one of them
// silently reset the escalation.
//
// It refuses to stamp a resolved or acknowledged incident. That is the last
// line of defence for the promise acknowledging makes — the caller checks it
// too, but the window between deciding to remind and writing the stamp is
// exactly when someone clicks acknowledge.
func (db *DB) RecordReminder(ctx context.Context, incidentID int64, at time.Time) error {
	res, err := db.Writer.ExecContext(ctx, `
		UPDATE incidents
		SET reminded_at = ?, reminder_count = reminder_count + 1
		WHERE id = ? AND resolved_at IS NULL AND acked_at IS NULL`,
		at.Unix(), incidentID)
	if err != nil {
		return fmt.Errorf("record reminder for incident %d: %w", incidentID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNoOpenIncident
	}
	return nil
}

// RemindableIncident is an open incident plus the one monitor setting the
// reminder schedule needs.
//
// Only the interval travels with it, not the whole monitor: the ticker looks
// at every candidate on every pass but alerts on almost none of them, so
// loading full monitor rows here would be a join per tick to answer a question
// two integers can. The monitor is fetched for the few that are actually due.
type RemindableIncident struct {
	Incident    Incident
	RepeatAfter time.Duration
}

// RemindableIncidents returns every incident that is a candidate for a
// reminder: confirmed, unresolved, unacknowledged, and belonging to an enabled
// monitor that has reminders switched on.
//
// It deliberately does not decide whether a reminder is *due*. That rule lives
// in the state package with the rest of the alerting logic, where it is a pure
// function over timestamps and can be tested as a table. This query only
// narrows the set the ticker has to look at.
//
// A paused monitor is excluded: it is not being checked, so its incident is
// frozen rather than ongoing, and reminding about an outage nobody is watching
// for would be alerting on stale information.
func (db *DB) RemindableIncidents(ctx context.Context) ([]RemindableIncident, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT `+incidentColumns+`, m.repeat_after_s
		FROM incidents
		JOIN monitors m ON m.id = incidents.monitor_id
		WHERE incidents.resolved_at IS NULL
		  AND incidents.acked_at IS NULL
		  AND incidents.confirmed_at IS NOT NULL
		  AND m.enabled = 1
		  AND m.repeat_after_s > 0
		ORDER BY incidents.id`)
	if err != nil {
		return nil, fmt.Errorf("query remindable incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RemindableIncident
	for rows.Next() {
		var (
			r       RemindableIncident
			seconds int64
		)
		inc, err := scanIncidentWith(rows, &seconds)
		if err != nil {
			return nil, err
		}
		r.Incident = inc
		r.RepeatAfter = time.Duration(seconds) * time.Second
		out = append(out, r)
	}
	return out, rows.Err()
}
