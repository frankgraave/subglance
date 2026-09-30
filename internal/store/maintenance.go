package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Recurrence must also work in a minimal container.
)

var ErrMaintenanceLimit = errors.New("at most 200 maintenance windows")

// MaintenanceWindow targets one monitor or an exact tag pair. Weekly windows
// start at a local wall-clock time and last DurationMinutes elapsed minutes.
type MaintenanceWindow struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	MonitorID       int64     `json:"monitor_id,omitempty"`
	TagKey          string    `json:"tag_key,omitempty"`
	TagValue        string    `json:"tag_value,omitempty"`
	StartsAt        time.Time `json:"starts_at,omitempty"`
	EndsAt          time.Time `json:"ends_at,omitempty"`
	Timezone        string    `json:"timezone,omitempty"`
	Weekdays        []int     `json:"weekdays,omitempty"` // Sunday = 0.
	LocalTime       string    `json:"local_time,omitempty"`
	DurationMinutes int       `json:"duration_minutes,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

func (w MaintenanceWindow) Validate() error {
	if strings.TrimSpace(w.Name) == "" || len(w.Name) > 120 {
		return fmt.Errorf("name must contain 1–120 bytes")
	}
	if w.MonitorID < 0 || (w.MonitorID > 0) == (w.TagKey != "") {
		return fmt.Errorf("choose exactly one monitor or tag pair")
	}
	if w.TagKey != "" {
		tags, err := NormaliseTags(map[string]string{w.TagKey: w.TagValue})
		if err != nil {
			return err
		}
		if tags[w.TagKey] != w.TagValue {
			return fmt.Errorf("use a normalized tag key and value")
		}
	} else if w.TagValue != "" {
		return fmt.Errorf("tag_value requires tag_key")
	}
	if w.Timezone == "" {
		if w.StartsAt.IsZero() || !w.EndsAt.After(w.StartsAt) || w.EndsAt.Sub(w.StartsAt) > 366*24*time.Hour {
			return fmt.Errorf("one-off window requires starts_at before ends_at, at most 366 days apart")
		}
		if len(w.Weekdays) > 0 || w.LocalTime != "" || w.DurationMinutes != 0 {
			return fmt.Errorf("recurrence requires timezone")
		}
	} else {
		if w.Timezone == "Local" {
			return fmt.Errorf("use an explicit IANA timezone")
		}
		if _, err := time.LoadLocation(w.Timezone); err != nil {
			return fmt.Errorf("unknown IANA timezone")
		}
		if !w.StartsAt.IsZero() || !w.EndsAt.IsZero() {
			return fmt.Errorf("weekly windows cannot have absolute start/end times")
		}
		if _, err := time.Parse("15:04", w.LocalTime); err != nil || len(w.LocalTime) != 5 {
			return fmt.Errorf("local_time must be HH:MM")
		}
		if len(w.Weekdays) == 0 || len(w.Weekdays) > 7 || w.DurationMinutes < 1 || w.DurationMinutes > 1440 {
			return fmt.Errorf("choose weekdays and a duration of 1–1440 minutes")
		}
		seen := map[int]bool{}
		for _, d := range w.Weekdays {
			if d < 0 || d > 6 || seen[d] {
				return fmt.Errorf("weekdays must be unique integers from 0 to 6")
			}
			seen[d] = true
		}
	}
	return nil
}

// Active uses [start,end). Missing DST times are skipped; repeated wall times
// start once, at the earlier instant. Duration is elapsed time, including DST.
func (w MaintenanceWindow) Active(at time.Time) bool {
	if !w.CreatedAt.IsZero() && at.Before(w.CreatedAt) {
		return false
	}
	if w.Timezone == "" {
		return !at.Before(w.StartsAt) && at.Before(w.EndsAt)
	}
	loc, clock, ok := w.recurrence()
	if !ok {
		return false
	}
	local := at.In(loc)
	// A 24-hour elapsed window can reach two civil dates back at spring DST.
	for delta := -2; delta <= 0; delta++ {
		start, ok := w.startOn(local.Year(), local.Month(), local.Day()+delta, loc, clock)
		if ok && !at.Before(start) && at.Before(start.Add(time.Duration(w.DurationMinutes)*time.Minute)) {
			return true
		}
	}
	return false
}

// MaintenanceSpan is one stretch of time a window is active: [Start, End).
type MaintenanceSpan struct {
	Start time.Time
	End   time.Time
}

// Occurrences returns the stretches of this window that overlap [from, to),
// oldest first. It follows Active exactly: an instant is inside a returned
// span if and only if Active reports true for it. So a span starts no
// earlier than the window was created, and a weekly start time that does
// not exist on a DST day produces no span that day. Spans are not cut to
// from and to: an occurrence already running at from is returned whole, so
// a caller can say when it began.
func (w MaintenanceWindow) Occurrences(from, to time.Time) []MaintenanceSpan {
	out := []MaintenanceSpan{}
	add := func(start, end time.Time) {
		if !w.CreatedAt.IsZero() && start.Before(w.CreatedAt) {
			start = w.CreatedAt
		}
		if start.Before(end) && start.Before(to) && end.After(from) {
			out = append(out, MaintenanceSpan{Start: start, End: end})
		}
	}
	if w.Timezone == "" {
		add(w.StartsAt, w.EndsAt)
		return out
	}
	loc, clock, ok := w.recurrence()
	if !ok || !from.Before(to) {
		return out
	}
	duration := time.Duration(w.DurationMinutes) * time.Minute
	first := from.In(loc)
	last := to.In(loc)
	lastDay := time.Date(last.Year(), last.Month(), last.Day(), 12, 0, 0, 0, loc)
	// Start two civil dates early for the same reason Active looks back two:
	// an occurrence that began then can still be running at from.
	for offset := -2; ; offset++ {
		day := time.Date(first.Year(), first.Month(), first.Day()+offset, 12, 0, 0, 0, loc)
		if day.After(lastDay) {
			break
		}
		if start, ok := w.startOn(day.Year(), day.Month(), day.Day(), loc, clock); ok {
			add(start, start.Add(duration))
		}
	}
	return out
}

// recurrence returns a weekly window's zone and start time of day.
func (w MaintenanceWindow) recurrence() (*time.Location, time.Time, bool) {
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return nil, time.Time{}, false
	}
	clock, err := time.Parse("15:04", w.LocalTime)
	if err != nil {
		return nil, time.Time{}, false
	}
	return loc, clock, true
}

// startOn returns when a weekly window starts on the civil date y-m-d in loc
// (d may be out of range; it is normalised like time.Date does). It reports
// false when that date is not one of the window's weekdays, or when the start
// time does not exist that day because the clocks jump over it.
func (w MaintenanceWindow) startOn(y int, m time.Month, d int, loc *time.Location, clock time.Time) (time.Time, bool) {
	day := time.Date(y, m, d, 12, 0, 0, 0, loc)
	selected := false
	for _, wd := range w.Weekdays {
		selected = selected || int(day.Weekday()) == wd
	}
	if !selected {
		return time.Time{}, false
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	matches := func(t time.Time) bool {
		v := t.In(loc)
		return v.Year() == day.Year() && v.YearDay() == day.YearDay() && v.Hour() == clock.Hour() && v.Minute() == clock.Minute()
	}
	// Enumerate the offsets on either side of a transition, not a presumed
	// one-hour shift (Lord Howe changes by thirty minutes).
	_, base := start.Zone()
	for _, near := range []time.Time{start.Add(-24 * time.Hour), start.Add(24 * time.Hour)} {
		_, offset := near.Zone()
		candidate := start.Add(time.Duration(base-offset) * time.Second)
		if matches(candidate) && (!matches(start) || candidate.Before(start)) {
			start = candidate
		}
	}
	if !matches(start) {
		return time.Time{}, false
	}
	return start, true
}

func (db *DB) CreateMaintenance(ctx context.Context, w MaintenanceWindow) (MaintenanceWindow, error) {
	if err := w.Validate(); err != nil {
		return w, err
	}
	if w.MonitorID > 0 {
		if _, err := db.GetMonitor(ctx, w.MonitorID); err != nil {
			return w, err
		}
	}
	w.ID = 0
	w.CreatedAt = time.Now().UTC().Truncate(time.Second)
	b, err := json.Marshal(w)
	if err != nil {
		return w, err
	}
	// The count and insertion share a statement, so concurrent requests cannot
	// exceed the cap. Cancel completed schedules to make room.
	var monitorID any
	if w.MonitorID > 0 {
		monitorID = w.MonitorID
	}
	res, err := db.Writer.ExecContext(ctx, `INSERT INTO maintenance_windows(monitor_id,spec) SELECT ?,? WHERE (SELECT count(*) FROM maintenance_windows)<200`, monitorID, string(b))
	if err != nil {
		return w, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return w, ErrMaintenanceLimit
	}
	w.ID, err = res.LastInsertId()
	return w, err
}
func (db *DB) ListMaintenance(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := db.Reader.QueryContext(ctx, `SELECT id,spec FROM maintenance_windows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MaintenanceWindow{}
	for rows.Next() {
		var id int64
		var spec string
		if err := rows.Scan(&id, &spec); err != nil {
			return nil, err
		}
		var w MaintenanceWindow
		if err := json.Unmarshal([]byte(spec), &w); err != nil {
			return nil, err
		}
		w.ID = id
		out = append(out, w)
	}
	return out, rows.Err()
}
func (db *DB) DeleteMaintenance(ctx context.Context, id int64) error {
	res, err := db.Writer.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (db *DB) InMaintenance(ctx context.Context, monitorID int64, at time.Time) (bool, error) {
	windows, err := db.ListMaintenance(ctx)
	if err != nil {
		return false, err
	}
	var m *Monitor
	for _, w := range windows {
		if !w.Active(at) {
			continue
		}
		if w.MonitorID == monitorID {
			return true, nil
		}
		if w.TagKey != "" {
			if m == nil {
				v, e := db.GetMonitor(ctx, monitorID)
				if e != nil {
					return false, e
				}
				m = &v
			}
			if m.Tags[w.TagKey] == w.TagValue {
				return true, nil
			}
		}
	}
	return false, nil
}

func (db *DB) SetMaintenancePending(ctx context.Context, id int64, pending bool) error {
	_, err := db.Writer.ExecContext(ctx, `UPDATE incidents SET maintenance_pending=? WHERE id=?`, pending, id)
	return err
}

// SuppressDelivery terminates a queued alert without charging a send attempt.
func (db *DB) SuppressDelivery(ctx context.Context, id int64) error {
	_, err := db.Writer.ExecContext(ctx, `UPDATE notif_outbox SET suppressed=1, last_error='suppressed by scheduled maintenance', updated_at=? WHERE id=?`, time.Now().Unix(), id)
	return err
}
