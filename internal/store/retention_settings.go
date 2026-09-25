package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Retention windows chosen on the settings page are stored in the settings
// table, one row per window, as whole seconds. "0" means keep forever.
//
// Seconds rather than a Go duration string, so the value reads the same to
// anything that opens the database, and so a later release that changes how
// durations are printed cannot make an old row unparseable.
const (
	settingRawRetention    = "retention.raw_seconds"
	settingRollupRetention = "retention.rollup_seconds"
)

// ErrRetentionPolicy wraps every reason a retention policy is refused, so a
// caller can tell a bad request from a failed write.
var ErrRetentionPolicy = errors.New("invalid retention policy")

// RetentionError is a refused policy, naming the window it is about.
type RetentionError struct {
	// Window is "raw" or "rollup".
	Window string
	Msg    string
}

func (e *RetentionError) Error() string { return e.Msg }

// Unwrap lets errors.Is match ErrRetentionPolicy.
func (e *RetentionError) Unwrap() error { return ErrRetentionPolicy }

// Validate reports the first reason a policy cannot be honoured. Zero means
// forever for either window.
//
// Only the rules that protect the data are enforced. There is deliberately no
// upper bound: an operator who brings their own disk decides how much of it
// history may use.
func (p RetentionPolicy) Validate() error {
	if p.Raw < 0 {
		return &RetentionError{"raw", "raw retention must not be negative"}
	}
	if p.Raw > 0 && p.Raw < MinRawRetention {
		return &RetentionError{"raw", fmt.Sprintf(
			"raw retention must be at least %s, or forever: the 24-hour charts read raw heartbeats", formatWindow(MinRawRetention))}
	}
	if p.Rollup < 0 {
		return &RetentionError{"rollup", "rollup retention must not be negative"}
	}
	// Hourly buckets only exist for hours whose raw beats are gone. A rollup
	// window inside the raw one would delete a bucket that still has its own
	// heartbeats next to it, so the history would jump back into existence on
	// the next read and vanish again on the next pass.
	if p.Rollup > 0 && (p.Raw == 0 || p.Rollup < p.Raw) {
		return &RetentionError{"rollup", "hourly summaries must be kept at least as long as raw heartbeats"}
	}
	return nil
}

// formatWindow renders a window the way the error messages and logs speak
// about it: whole days where it divides, hours otherwise.
func formatWindow(d time.Duration) string {
	if d == 0 {
		return "forever"
	}
	if d%(24*time.Hour) == 0 {
		days := int64(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return strconv.FormatInt(days, 10) + " days"
	}
	return d.String()
}

// RetentionPin is a window fixed from outside the database: a command-line
// flag or an environment variable.
type RetentionPin struct {
	// Value is the pinned window. Zero means forever.
	Value time.Duration
	// By names what pinned it, as the operator would type it: the flag
	// "--raw-retention" or the variable "SUBGLANCE_RAW_RETENTION".
	By string
}

// RetentionPins holds the windows pinned at startup. A nil pin leaves that
// window to the settings page.
type RetentionPins struct {
	Raw    *RetentionPin
	Rollup *RetentionPin
}

// Where a window's effective value came from.
const (
	RetentionSourceDefault  = "default"
	RetentionSourceDatabase = "database"
	RetentionSourcePinned   = "pinned"
)

// RetentionWindow is one resolved window and where its value came from.
type RetentionWindow struct {
	Value time.Duration
	// Source is RetentionSourceDefault, RetentionSourceDatabase or
	// RetentionSourcePinned.
	Source string
	// PinnedBy names the flag or variable when Source is pinned.
	PinnedBy string
	// Raised is set when the stored or default value had to change to stay
	// valid next to a pinned window, and holds the value that was set aside.
	Raised *time.Duration
}

// EffectiveRetention is the policy a maintenance pass will actually apply.
type EffectiveRetention struct {
	Raw    RetentionWindow
	Rollup RetentionWindow
}

// Policy returns the windows as a RetentionPolicy.
func (e EffectiveRetention) Policy() RetentionPolicy {
	return RetentionPolicy{Raw: e.Raw.Value, Rollup: e.Rollup.Value}
}

// StoredRetention reads the windows saved from the settings page. A nil
// field has never been saved.
func (db *DB) StoredRetention(ctx context.Context) (raw, rollup *time.Duration, err error) {
	raw, err = db.readRetentionSetting(ctx, settingRawRetention)
	if err != nil {
		return nil, nil, err
	}
	rollup, err = db.readRetentionSetting(ctx, settingRollupRetention)
	if err != nil {
		return nil, nil, err
	}
	return raw, rollup, nil
}

func (db *DB) readRetentionSetting(ctx context.Context, key string) (*time.Duration, error) {
	var v string
	err := db.Reader.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs < 0 {
		// A row this code did not write. Refusing it is safer than guessing
		// a window: a misread "forever" or a misread short window both cost
		// data, only in different directions.
		return nil, fmt.Errorf("setting %s holds %q, want a non-negative number of seconds", key, v)
	}
	d := time.Duration(secs) * time.Second
	return &d, nil
}

// ResolveRetention works out the policy a pass applies: a pinned window
// wins, then the stored one, then the default.
//
// A pinned window can make a stored one invalid — a flag that raises raw
// retention past the stored rollup window, for instance. The stored value is
// then set aside for the nearest valid one. Where the repair can lengthen, it
// does: resolving a conflict should not be what deletes somebody's history.
// The one case it cannot is a pinned rollup window shorter than the stored
// raw one, and there the pin wins, because it is the operator's explicit
// instruction and the stored value was only ever a page setting. Raised
// records the value that was set aside, so the settings page can say so.
func (db *DB) ResolveRetention(ctx context.Context, pins RetentionPins) (EffectiveRetention, error) {
	storedRaw, storedRollup, err := db.StoredRetention(ctx)
	if err != nil {
		return EffectiveRetention{}, err
	}
	eff := EffectiveRetention{
		Raw:    resolveWindow(pins.Raw, storedRaw, DefaultRawRetention),
		Rollup: resolveWindow(pins.Rollup, storedRollup, DefaultRollupRetention),
	}

	if eff.Policy().Validate() == nil {
		return eff, nil
	}
	// Only a combination with at least one unpinned window can be repaired.
	// Rollup rises to meet raw (raw forever forces rollup forever); failing
	// that, raw comes down to a pinned rollup window.
	switch {
	case eff.Rollup.Source != RetentionSourcePinned:
		lift(&eff.Rollup, raiseRollup(eff.Raw.Value))
	case eff.Raw.Source != RetentionSourcePinned:
		lift(&eff.Raw, eff.Rollup.Value)
	}
	if err := eff.Policy().Validate(); err != nil {
		return EffectiveRetention{}, err
	}
	return eff, nil
}

func resolveWindow(pin *RetentionPin, stored *time.Duration, def time.Duration) RetentionWindow {
	switch {
	case pin != nil:
		return RetentionWindow{Value: pin.Value, Source: RetentionSourcePinned, PinnedBy: pin.By}
	case stored != nil:
		return RetentionWindow{Value: *stored, Source: RetentionSourceDatabase}
	default:
		return RetentionWindow{Value: def, Source: RetentionSourceDefault}
	}
}

// raiseRollup is the shortest valid rollup window next to a raw window.
func raiseRollup(raw time.Duration) time.Duration {
	if raw == 0 {
		return 0
	}
	return raw
}

func lift(w *RetentionWindow, to time.Duration) {
	if w.Value == to {
		return
	}
	was := w.Value
	w.Raised = &was
	w.Value = to
}

// SetRetention stores the windows chosen on the settings page. A nil window
// is left as it was.
//
// The combination is validated against pins, because the stored value only
// matters for windows that are not pinned and the pinned ones are what it
// has to coexist with.
func (db *DB) SetRetention(ctx context.Context, raw, rollup *time.Duration, pins RetentionPins) error {
	storedRaw, storedRollup, err := db.StoredRetention(ctx)
	if err != nil {
		return err
	}
	if raw != nil {
		storedRaw = raw
	}
	if rollup != nil {
		storedRollup = rollup
	}
	next := RetentionPolicy{
		Raw:    resolveWindow(pins.Raw, storedRaw, DefaultRawRetention).Value,
		Rollup: resolveWindow(pins.Rollup, storedRollup, DefaultRollupRetention).Value,
	}
	if err := next.Validate(); err != nil {
		return err
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retention update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	for key, v := range map[string]*time.Duration{settingRawRetention: raw, settingRollupRetention: rollup} {
		if v == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, strconv.FormatInt(int64(*v/time.Second), 10), now); err != nil {
			return fmt.Errorf("save %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retention update: %w", err)
	}
	return nil
}

// TableUsage is what one table costs today and how fast it grows.
type TableUsage struct {
	Name string
	Rows int64
	// Bytes is the space the table and its indexes occupy, or -1 when the
	// page-level statistics are unavailable.
	Bytes int64
	// RowsPerDay is the steady-state inflow, measured over recent history.
	RowsPerDay float64
}

// RetentionTables reports the tables retention governs, with their size and
// daily growth. The growth figures are measured from what arrived recently,
// not modelled, so they follow the instance's real monitors and intervals.
func (db *DB) RetentionTables(ctx context.Context) ([]TableUsage, error) {
	return db.retentionTablesAt(ctx, time.Now())
}

func (db *DB) retentionTablesAt(ctx context.Context, now time.Time) ([]TableUsage, error) {
	day := now.Add(-24 * time.Hour).Unix()
	month := now.Add(-30 * 24 * time.Hour).Unix()

	queries := []struct {
		name, rows, perDay string
		args               []any
		scale              float64
	}{
		{"heartbeats", `SELECT count(*) FROM heartbeats`,
			`SELECT count(*) FROM heartbeats WHERE ts >= ?`, []any{day}, 1},
		{"heartbeat_responses", `SELECT count(*) FROM heartbeat_responses`,
			`SELECT count(*) FROM heartbeat_responses r JOIN heartbeats h ON h.id = r.heartbeat_id WHERE h.ts >= ?`,
			[]any{day}, 1},
		// A day of raw beats becomes one bucket per monitor per hour it
		// covers, so the buckets tomorrow's rollups will write are counted
		// from the beats that arrived today.
		{"heartbeat_hourly", `SELECT count(*) FROM heartbeat_hourly`,
			`SELECT count(*) FROM (SELECT DISTINCT monitor_id, ts / 3600 FROM heartbeats WHERE ts >= ?)`,
			[]any{day}, 1},
		// Incidents are too rare for one day to mean anything, so their
		// rate is a thirty-day average.
		{"incidents", `SELECT count(*) FROM incidents`,
			`SELECT count(*) FROM incidents WHERE resolved_at >= ?`, []any{month}, 30},
	}

	sizes, sizeErr := db.tableBytes(ctx)
	out := make([]TableUsage, 0, len(queries))
	for _, q := range queries {
		u := TableUsage{Name: q.name, Bytes: -1}
		if err := db.Reader.QueryRowContext(ctx, q.rows).Scan(&u.Rows); err != nil {
			return nil, fmt.Errorf("count %s: %w", q.name, err)
		}
		var n int64
		if err := db.Reader.QueryRowContext(ctx, q.perDay, q.args...).Scan(&n); err != nil {
			return nil, fmt.Errorf("measure growth of %s: %w", q.name, err)
		}
		u.RowsPerDay = float64(n) / q.scale
		if sizeErr == nil {
			u.Bytes = sizes[q.name]
		}
		out = append(out, u)
	}
	return out, nil
}

// tableBytes sums the pages of every table and its indexes, via SQLite's
// dbstat virtual table. It reads every page once, which on the sizes this
// product reaches is a fraction of a second, and it is only called from a
// settings page someone is looking at.
func (db *DB) tableBytes(ctx context.Context) (map[string]int64, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT s.tbl_name, sum(d.pgsize)
		FROM dbstat AS d JOIN sqlite_schema AS s ON s.name = d.name
		WHERE s.tbl_name IN ('heartbeats', 'heartbeat_responses', 'heartbeat_hourly', 'incidents')
		GROUP BY s.tbl_name`)
	if err != nil {
		return nil, fmt.Errorf("read table sizes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var bytes int64
		if err := rows.Scan(&name, &bytes); err != nil {
			return nil, fmt.Errorf("scan table size: %w", err)
		}
		out[name] = bytes
	}
	return out, rows.Err()
}

// RetentionImpact is what the next pass would remove under a policy.
type RetentionImpact struct {
	// Heartbeats is the number of raw rows that would be folded into hourly
	// buckets. They are summarised, not lost, but their per-check detail
	// and any stored failure responses go.
	Heartbeats int64
	// HourlyBuckets and Incidents are deleted outright.
	HourlyBuckets int64
	Incidents     int64
}

// PreviewRetention counts what a pass under p would remove if it ran now,
// without changing anything. It exists so a shorter window can say what it
// costs before it is saved rather than after.
func (db *DB) PreviewRetention(ctx context.Context, p RetentionPolicy) (RetentionImpact, error) {
	return db.previewRetentionAt(ctx, time.Now(), p)
}

func (db *DB) previewRetentionAt(ctx context.Context, now time.Time, p RetentionPolicy) (RetentionImpact, error) {
	var out RetentionImpact
	if p.Raw > 0 {
		cutoff := now.Add(-p.Raw).Truncate(bucketSize).Unix()
		if err := db.Reader.QueryRowContext(ctx,
			`SELECT count(*) FROM heartbeats WHERE ts < ?`, cutoff).Scan(&out.Heartbeats); err != nil {
			return out, fmt.Errorf("count heartbeats to roll up: %w", err)
		}
	}
	if p.Rollup > 0 {
		cutoff := now.Add(-p.Rollup).Truncate(bucketSize).Unix()
		if err := db.Reader.QueryRowContext(ctx,
			`SELECT count(*) FROM heartbeat_hourly WHERE bucket < ?`, cutoff).Scan(&out.HourlyBuckets); err != nil {
			return out, fmt.Errorf("count hourly buckets to prune: %w", err)
		}
		if err := db.Reader.QueryRowContext(ctx,
			`SELECT count(*) FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at < ?`,
			cutoff).Scan(&out.Incidents); err != nil {
			return out, fmt.Errorf("count incidents to prune: %w", err)
		}
	}
	return out, nil
}
