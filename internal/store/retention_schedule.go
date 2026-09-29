package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultRetentionRunAt is when the daily retention pass runs, in the
// server's time zone, unless an operator chose another time.
//
// Rolling up and vacuuming a large table is the one piece of housekeeping
// that competes with the checks for the single writer connection, so it
// belongs in the hours nobody is looking at a dashboard. 03:30 rather than
// the round hour keeps it clear of the backups and cron jobs that tend to
// cluster on the hour on the same machine.
var DefaultRetentionRunAt = ClockTime{Hour: 3, Minute: 30}

// ClockTime is a wall-clock time of day, to the minute.
type ClockTime struct {
	Hour   int
	Minute int
}

// ParseClockTime reads a 24-hour "HH:MM" time of day.
//
// One form only, and a strict one: this value is typed into a flag, an
// environment variable and a settings field, and a lenient parser that
// accepted "3.30" or "3:30pm" in one place would be a different parser from
// the one in the next.
func ParseClockTime(s string) (ClockTime, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return ClockTime{}, fmt.Errorf("time of day %q: want HH:MM, 24-hour", s)
	}
	hour, err := strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return ClockTime{}, fmt.Errorf("time of day %q: hour must be 00 to 23", s)
	}
	minute, err := strconv.Atoi(m)
	if err != nil || minute < 0 || minute > 59 {
		return ClockTime{}, fmt.Errorf("time of day %q: minute must be 00 to 59", s)
	}
	return ClockTime{Hour: hour, Minute: minute}, nil
}

// String renders the time the way ParseClockTime reads it.
func (c ClockTime) String() string {
	return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute)
}

// Next is the first moment strictly after now at which the wall clock in
// loc reads c.
//
// Days are counted on the calendar, not in 24-hour steps, so the pass keeps
// its time of day across a daylight-saving change. A time that does not
// exist on a given day (inside a spring-forward gap) resolves to the end of
// the gap, the first instant the clock reads c or later; see on.
func (c ClockTime) Next(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	for day := 0; ; day++ {
		t := c.on(local.Year(), local.Month(), local.Day()+day, loc)
		if t.After(now) {
			return t
		}
	}
}

// Previous is the latest moment at or before now at which the wall clock in
// loc read c: the most recent time the pass was due.
func (c ClockTime) Previous(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	for day := 0; ; day-- {
		t := c.on(local.Year(), local.Month(), local.Day()+day, loc)
		if !t.After(now) {
			return t
		}
	}
}

// on is the moment the wall clock in loc reads c on the given calendar day.
// The day may be out of range; it is normalised as time.Date does.
//
// When c falls inside a spring-forward gap it never appears on the clock
// that day, and time.Date resolves it with either offset, which the Go
// documentation leaves unspecified: in America/New_York 02:30 comes back as
// 01:30 EST, an hour before the configured time, and in Europe/Amsterdam as
// 03:30 CEST. Neither is the moment an operator watching the clock would
// pick, so the gap resolves to its end, the transition itself: the first
// instant at which the clock reads c or later.
func (c ClockTime) on(year int, month time.Month, day int, loc *time.Location) time.Time {
	t := time.Date(year, month, day, c.Hour, c.Minute, 0, 0, loc)
	if t.Hour() == c.Hour && t.Minute() == c.Minute {
		return t
	}
	// Compare wall-clock readings as if they were UTC, so a gap at midnight
	// that pushed t onto the neighbouring date still orders correctly.
	want := time.Date(year, month, day, c.Hour, c.Minute, 0, 0, time.UTC)
	read := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	start, end := t.ZoneBounds()
	if read.Before(want) {
		// Resolved with the offset from before the gap: the gap ends where
		// that offset stops applying.
		return end
	}
	// Resolved with the offset from after the gap, which starts at its end.
	return start
}

// settingRetentionRunAt holds the time of day chosen on the settings page,
// as "HH:MM". Absent means the default.
const settingRetentionRunAt = "retention.run_at"

// RetentionRunAtPin is a run time fixed from a flag or environment variable.
type RetentionRunAtPin struct {
	Value ClockTime
	// By names the flag or variable, as the operator would type it.
	By string
}

// RetentionRunAt is the resolved time of day and where it came from.
type RetentionRunAt struct {
	Value ClockTime
	// Source is RetentionSourceDefault, RetentionSourceDatabase or
	// RetentionSourcePinned, as for the windows.
	Source string
	// PinnedBy names the flag or variable when Source is pinned.
	PinnedBy string
}

// ResolveRetentionRunAt works out when the daily pass runs: a pinned time
// wins, then the stored one, then DefaultRetentionRunAt. It is resolved again
// before every wait, so a time saved on the settings page applies from the
// next pass without a restart.
func (db *DB) ResolveRetentionRunAt(ctx context.Context, pin *RetentionRunAtPin) (RetentionRunAt, error) {
	if pin != nil {
		return RetentionRunAt{Value: pin.Value, Source: RetentionSourcePinned, PinnedBy: pin.By}, nil
	}
	var v string
	err := db.Reader.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingRetentionRunAt).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return RetentionRunAt{Value: DefaultRetentionRunAt, Source: RetentionSourceDefault}, nil
	}
	if err != nil {
		return RetentionRunAt{}, fmt.Errorf("read %s: %w", settingRetentionRunAt, err)
	}
	c, err := ParseClockTime(v)
	if err != nil {
		// A row this code did not write. Refused rather than guessed, like
		// the windows: the pass still runs, at a time nobody chose, only if
		// someone decides that is acceptable.
		return RetentionRunAt{}, fmt.Errorf("setting %s: %w", settingRetentionRunAt, err)
	}
	return RetentionRunAt{Value: c, Source: RetentionSourceDatabase}, nil
}

// SetRetentionRunAt stores the time of day chosen on the settings page.
func (db *DB) SetRetentionRunAt(ctx context.Context, c ClockTime) error {
	if _, err := ParseClockTime(c.String()); err != nil {
		return err
	}
	return db.putSetting(ctx, settingRetentionRunAt, c.String())
}

// settingRetentionLastRun holds the outcome of the most recent pass, as JSON.
const settingRetentionLastRun = "retention.last_run"

// RetentionPass is the record of one maintenance pass, kept so the settings
// page can say when housekeeping last ran and what it did, and so a restart
// can tell whether the scheduled pass was missed.
type RetentionPass struct {
	StartedAt time.Time `json:"started_at"`
	// Duration is how long the pass took, failed or not.
	Duration time.Duration `json:"duration_ns"`
	// Trigger is "schedule", "startup" (a missed pass caught up) or
	// "manual".
	Trigger string `json:"trigger"`
	// The rows each step removed. Heartbeats are folded into hourly
	// buckets, not lost; the rest are deleted.
	Heartbeats    int64 `json:"heartbeats"`
	HourlyBuckets int64 `json:"hourly_buckets"`
	Incidents     int64 `json:"incidents"`
	Deliveries    int64 `json:"deliveries"`
	// FreedBytes is the disk space handed back to the filesystem.
	FreedBytes int64 `json:"freed_bytes"`
	// SizeCap is what the database size limit did, or nil when no limit
	// was set. Its rows are not included in the counts above.
	SizeCap *SizeCapResult `json:"size_cap,omitempty"`
	// Error is the reason the pass failed, or empty when it succeeded.
	Error string `json:"error,omitempty"`
}

// Succeeded reports whether the pass completed.
func (p RetentionPass) Succeeded() bool { return p.Error == "" }

// SaveRetentionPass records p as the most recent pass, replacing the last.
func (db *DB) SaveRetentionPass(ctx context.Context, p RetentionPass) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode retention pass: %w", err)
	}
	return db.putSetting(ctx, settingRetentionLastRun, string(b))
}

// LastRetentionPass returns the most recent recorded pass, or nil when none
// has been recorded yet.
func (db *DB) LastRetentionPass(ctx context.Context) (*RetentionPass, error) {
	var v string
	err := db.Reader.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingRetentionLastRun).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", settingRetentionLastRun, err)
	}
	var p RetentionPass
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return nil, fmt.Errorf("decode %s: %w", settingRetentionLastRun, err)
	}
	return &p, nil
}

func (db *DB) putSetting(ctx context.Context, key, value string) error {
	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix()); err != nil {
		return fmt.Errorf("save %s: %w", key, err)
	}
	return nil
}
