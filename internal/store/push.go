package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

// TypePush is the monitor type that is reported to rather than probed.
//
// The string lives here rather than only in the checker package because the
// store is the layer that has to keep push rows internally consistent, and a
// comparison against a literal spelled out in four files is the kind of thing
// that survives right up until someone renames it in three.
const TypePush = "push"

// Push window bounds. One minute is the shortest expected period that is not
// really a probe in disguise; thirty days is long enough for a monthly job and
// short enough that a typo in seconds-versus-days is still caught.
const (
	MinPushIntervalS = 60
	MaxPushIntervalS = 30 * 24 * 60 * 60
	MaxPushGraceS    = 30 * 24 * 60 * 60

	// DefaultPushGraceS is how late a job may report before silence counts
	// as failure, when the caller expresses no preference.
	//
	// One minute, not zero. A job scheduled hourly does not start at
	// exactly the same millisecond each hour — cron drifts, the machine is
	// busy, the network is slow — and a zero grace would turn that ordinary
	// jitter into an outage notification. Choosing zero has to be a
	// deliberate act, because it is almost always wrong.
	DefaultPushGraceS = 60
)

// ErrNotPushMonitor reports a push operation aimed at a monitor that is not
// one. It exists so the API can answer 404 rather than 500 for a token that
// somehow resolves to the wrong kind of row.
var ErrNotPushMonitor = errors.New("store: monitor is not a push monitor")

// pushGraceValue renders the grace period for storage.
//
// Zero is a legal grace for a push monitor and NULL for every other type, so
// nullInt cannot be used here: it would store a deliberate "no grace at all"
// as "not applicable", and the column would then fail the type/token CHECK in
// a way that has nothing to do with what the caller asked for.
func pushGraceValue(m Monitor) any {
	if m.Type != TypePush {
		return nil
	}
	return m.PushGraceS
}

// PushDeadline reports the moment after which this monitor counts as overdue,
// measured from the given last report.
//
// The grace period is added to the expected interval rather than replacing
// part of it: a job that runs hourly and is allowed to be five minutes late is
// late at 65 minutes, not at 60. Subtracting instead would mean that raising
// the tolerance made the monitor stricter, which nobody expects of a setting
// called "grace".
func (m Monitor) PushDeadline(last time.Time) time.Time {
	return last.Add(time.Duration(m.PushIntervalS+m.PushGraceS) * time.Second)
}

// ListEnabledPushMonitors returns the enabled push monitors, which is the set
// the watchdog has to keep an eye on.
func (db *DB) ListEnabledPushMonitors(ctx context.Context) ([]Monitor, error) {
	return db.queryMonitors(ctx, "WHERE enabled = 1 AND type = ?", TypePush)
}

// MonitorByPushToken resolves a push URL token to its monitor.
//
// It returns sql.ErrNoRows for an unknown token. The lookup is by hash, so a
// stolen database file yields no working URLs, and the plaintext never travels
// further into the process than this function's argument.
func (db *DB) MonitorByPushToken(ctx context.Context, token string) (Monitor, error) {
	if token == "" {
		return Monitor{}, sql.ErrNoRows
	}

	var id int64
	err := db.Reader.QueryRowContext(ctx,
		"SELECT id FROM monitors WHERE push_token_hash = ?", auth.HashToken(token),
	).Scan(&id)
	if err != nil {
		// sql.ErrNoRows passes through unwrapped: the caller distinguishes
		// "no such token" from a real failure, and that is the whole
		// difference between a 404 and a 500 on a public endpoint.
		if errors.Is(err, sql.ErrNoRows) {
			return Monitor{}, err
		}
		return Monitor{}, fmt.Errorf("lookup push token: %w", err)
	}

	m, err := db.GetMonitor(ctx, id)
	if err != nil {
		return Monitor{}, err
	}
	if m.Type != TypePush {
		// Unreachable while the schema CHECK holds, and worth saying out
		// loud anyway: this is the one query in the codebase that turns an
		// attacker-supplied string into a monitor id.
		return Monitor{}, ErrNotPushMonitor
	}
	return m, nil
}

// LastActivity reports when this monitor last produced a heartbeat, falling
// back to when it was created.
//
// The fallback is what makes a brand-new push monitor behave sensibly: with no
// heartbeat to measure from, a deadline computed from the zero time would be
// decades in the past and the monitor would be declared down before anyone had
// a chance to paste the URL into a script. Counting from creation gives the
// job exactly one full window to report for the first time.
func (db *DB) LastActivity(ctx context.Context, m Monitor) (time.Time, error) {
	hb, err := db.LatestHeartbeat(ctx, m.ID)
	switch {
	case err == nil:
		return hb.TS, nil
	case errors.Is(err, sql.ErrNoRows):
		return m.CreatedAt, nil
	default:
		return time.Time{}, err
	}
}
