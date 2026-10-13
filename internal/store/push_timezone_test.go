package store

import (
	"context"
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

// TestPushDeadlineKeepsADailyJobsLocalClock is the case the field exists for:
// a daily job at 06:08 in Amsterdam, with the form's default minute of grace,
// across the night the clocks go back (25-10-2026) and the night they go
// forward (28-03-2027). On the zone's clock the deadline is 06:09 local both
// times; counted in elapsed seconds it would be 05:09 local in October, an
// hour before the job runs.
func TestPushDeadlineKeepsADailyJobsLocalClock(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	m := Monitor{Type: TypePush, PushIntervalS: 86400, PushGraceS: 60, PushTimezone: "Europe/Amsterdam"}

	for _, tc := range []struct {
		name       string
		last       time.Time
		onTime     time.Time
		late       time.Time
		elapsedGap time.Duration
	}{
		{
			name:       "clocks go back",
			last:       time.Date(2026, 10, 24, 6, 8, 0, 0, ams),
			onTime:     time.Date(2026, 10, 25, 6, 9, 0, 0, ams),
			late:       time.Date(2026, 10, 25, 6, 9, 1, 0, ams),
			elapsedGap: 25 * time.Hour,
		},
		{
			name:       "clocks go forward",
			last:       time.Date(2027, 3, 27, 6, 8, 0, 0, ams),
			onTime:     time.Date(2027, 3, 28, 6, 9, 0, 0, ams),
			late:       time.Date(2027, 3, 28, 6, 9, 1, 0, ams),
			elapsedGap: 23 * time.Hour,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The test's own premise: these two days really are not 24 hours.
			if gap := tc.onTime.Add(-time.Minute).Sub(tc.last); gap != tc.elapsedGap {
				t.Fatalf("06:08 to 06:08 is %v, want %v; the dates no longer straddle a change", gap, tc.elapsedGap)
			}
			// The watchdog's rule is !now.After(deadline): on time at the
			// deadline itself, late one second after it.
			deadline := m.PushDeadline(tc.last.UTC())
			if !deadline.Equal(tc.onTime) {
				t.Errorf("deadline = %v, want %v", deadline.In(ams), tc.onTime)
			}
			if tc.onTime.After(deadline) {
				t.Errorf("a report due at 06:09 local counts as late at 06:09")
			}
			if !tc.late.After(deadline) {
				t.Errorf("silence at 06:09:01 local is not late (deadline %v)", deadline.In(ams))
			}
		})
	}
}

// TestPushDeadlineWithoutZoneCountsElapsedTime pins what an empty zone means:
// nothing changes, including the October false alarm the field exists for.
func TestPushDeadlineWithoutZoneCountsElapsedTime(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	last := time.Date(2026, 10, 24, 6, 8, 0, 0, ams)
	m := Monitor{Type: TypePush, PushIntervalS: 86400, PushGraceS: 60}

	want := last.Add(24*time.Hour + time.Minute)
	if got := m.PushDeadline(last); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got, want)
	}
}

// TestPushDeadlineCountsSeveralDays covers a weekly job, and one whose window
// holds the change somewhere in the middle rather than on its last night.
func TestPushDeadlineCountsSeveralDays(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	m := Monitor{Type: TypePush, PushIntervalS: 7 * 86400, PushGraceS: 0, PushTimezone: "America/New_York"}

	last := time.Date(2026, 10, 28, 23, 15, 0, 0, ny) // a Wednesday
	want := time.Date(2026, 11, 4, 23, 15, 0, 0, ny)  // US clocks go back 1 November
	if got := m.PushDeadline(last); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got.In(ny), want)
	}
	if got := want.Sub(last); got != 7*24*time.Hour+time.Hour {
		t.Fatalf("premise: the week is %v long", got)
	}
}

// TestPushDeadlineAmbiguousAndMissingReadings decides the two readings a
// wall clock does not map to one moment, towards the later one: a deadline
// that errs early is a false alarm.
func TestPushDeadlineAmbiguousAndMissingReadings(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	m := Monitor{Type: TypePush, PushIntervalS: 86400, PushTimezone: "Europe/Amsterdam"}

	// 02:30 happens twice on 25-10-2026: at 00:30 and at 01:30 UTC.
	last := time.Date(2026, 10, 24, 2, 30, 0, 0, ams)
	if got, want := m.PushDeadline(last), time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("repeated 02:30: deadline = %v, want the second occurrence %v", got.UTC(), want)
	}

	// 02:30 does not happen on 28-03-2027; the deadline is 03:30 CEST.
	last = time.Date(2027, 3, 27, 2, 30, 0, 0, ams)
	if got, want := m.PushDeadline(last), time.Date(2027, 3, 28, 1, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("missing 02:30: deadline = %v, want %v", got.UTC(), want)
	}
}

// TestPushDeadlineFallsBackToElapsedTime covers the rows the API never writes:
// an interval that is not whole days, and a zone this binary cannot load.
// Neither may stop the watchdog from computing a deadline at all.
func TestPushDeadlineFallsBackToElapsedTime(t *testing.T) {
	last := time.Date(2026, 10, 24, 4, 8, 0, 0, time.UTC)
	for _, m := range []Monitor{
		{Type: TypePush, PushIntervalS: 3600, PushGraceS: 60, PushTimezone: "Europe/Amsterdam"},
		{Type: TypePush, PushIntervalS: 86400, PushGraceS: 60, PushTimezone: "Mars/Olympus_Mons"},
		{Type: TypePush, PushIntervalS: 86400, PushGraceS: 60, PushTimezone: "Local"},
	} {
		want := last.Add(time.Duration(m.PushIntervalS+m.PushGraceS) * time.Second)
		if got := m.PushDeadline(last); !got.Equal(want) {
			t.Errorf("%d s in %q: deadline = %v, want %v", m.PushIntervalS, m.PushTimezone, got, want)
		}
	}
}

// TestPushTimezoneRoundTrips is the storage half: the zone is written on
// create and on update, read back, and cleared by an empty value.
func TestPushTimezoneRoundTrips(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "nightly", Type: TypePush, PushIntervalS: 86400, PushGraceS: 60,
		PushTimezone: "Europe/Amsterdam", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.PushTimezone != "Europe/Amsterdam" {
		t.Fatalf("zone after create = %q", got.PushTimezone)
	}

	got.PushTimezone = ""
	if _, err := db.UpdateMonitor(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	cleared, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cleared.PushTimezone != "" {
		t.Errorf("zone after clearing = %q", cleared.PushTimezone)
	}
	var stored any
	if err := db.Writer.QueryRowContext(ctx, `SELECT push_timezone FROM monitors WHERE id = ?`, m.ID).Scan(&stored); err != nil {
		t.Fatalf("read column: %v", err)
	}
	if stored != nil {
		t.Errorf("cleared zone stored as %#v, want NULL", stored)
	}
}

// TestSchemaKeepsPushTimezoneOnPushMonitors guards the CHECK: a zone on a
// dialled monitor is a setting nothing reads, and an empty string would be a
// second spelling of "none".
func TestSchemaKeepsPushTimezoneOnPushMonitors(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, push_timezone, created_at, updated_at)
		VALUES ('dialled', 'http', 'https://example.com', 'Europe/Amsterdam', 0, 0)`); err == nil {
		t.Error("schema accepted a push timezone on an http monitor")
	}
	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, push_token_hash, push_interval_s, push_grace_s, push_timezone, created_at, updated_at)
		VALUES ('blank', 'push', '', 'cafebabe', 86400, 60, '', 0, 0)`); err == nil {
		t.Error("schema accepted an empty push timezone")
	}
}
