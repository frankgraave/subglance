package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
}

// Confirmed reports whether this incident passed the failure threshold.
func (i Incident) Confirmed() bool { return !i.ConfirmedAt.IsZero() }

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
	id, monitor_id, started_at, confirmed_at, resolved_at, acked_at, cause, last_error`

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
	var (
		inc       Incident
		started   int64
		confirmed sql.NullInt64
		resolved  sql.NullInt64
		acked     sql.NullInt64
	)

	err := s.Scan(
		&inc.ID, &inc.MonitorID, &started,
		&confirmed, &resolved, &acked,
		&inc.Cause, &inc.LastError,
	)
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
	return inc, nil
}
