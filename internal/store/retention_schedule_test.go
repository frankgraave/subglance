package store

import (
	"context"
	"testing"
	"time"
)

func TestParseClockTime(t *testing.T) {
	good := map[string]ClockTime{
		"00:00": {0, 0},
		"03:30": {3, 30},
		"23:59": {23, 59},
	}
	for in, want := range good {
		got, err := ParseClockTime(in)
		if err != nil || got != want {
			t.Errorf("ParseClockTime(%q) = %v, %v; want %v", in, got, err, want)
		}
		if got.String() != in {
			t.Errorf("%v.String() = %q, want %q", got, got.String(), in)
		}
	}
	for _, in := range []string{"", "3:30", "03:3", "24:00", "12:60", "0330", "03-30", "ab:cd", "-1:30", "03:30pm"} {
		if _, err := ParseClockTime(in); err == nil {
			t.Errorf("ParseClockTime(%q) accepted, want an error", in)
		}
	}
}

func TestClockTimeNextAndPrevious(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	at := ClockTime{Hour: 3, Minute: 30}
	cases := []struct {
		name       string
		now        time.Time
		next, prev time.Time
	}{
		{
			name: "before today's time",
			now:  time.Date(2026, 6, 10, 1, 0, 0, 0, loc),
			next: time.Date(2026, 6, 10, 3, 30, 0, 0, loc),
			prev: time.Date(2026, 6, 9, 3, 30, 0, 0, loc),
		},
		{
			name: "exactly on time: due now, next is tomorrow",
			now:  time.Date(2026, 6, 10, 3, 30, 0, 0, loc),
			next: time.Date(2026, 6, 11, 3, 30, 0, 0, loc),
			prev: time.Date(2026, 6, 10, 3, 30, 0, 0, loc),
		},
		{
			// The day after the clocks go back is 25 hours long; the pass
			// keeps its wall-clock time rather than drifting to 02:30.
			name: "across the autumn change",
			now:  time.Date(2026, 10, 24, 12, 0, 0, 0, loc),
			next: time.Date(2026, 10, 25, 3, 30, 0, 0, loc),
			prev: time.Date(2026, 10, 24, 3, 30, 0, 0, loc),
		},
	}
	for _, c := range cases {
		if got := at.Next(c.now, loc); !got.Equal(c.next) {
			t.Errorf("%s: Next = %v, want %v", c.name, got, c.next)
		}
		if got := at.Previous(c.now, loc); !got.Equal(c.prev) {
			t.Errorf("%s: Previous = %v, want %v", c.name, got, c.prev)
		}
	}

	// 02:30 does not exist on the spring change in this zone. The pass
	// still runs that day, when the clock passes it.
	gap := ClockTime{Hour: 2, Minute: 30}
	now := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	next := gap.Next(now, loc)
	if next.Day() != 29 || !next.After(now) || next.Sub(now) > 4*time.Hour {
		t.Errorf("Next across the spring gap = %v, want a moment early on 29 March", next)
	}
}

func TestRetentionRunAtResolves(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	got, err := db.ResolveRetentionRunAt(ctx, nil)
	if err != nil || got.Value != DefaultRetentionRunAt || got.Source != RetentionSourceDefault {
		t.Fatalf("unset: got %+v, %v; want the default", got, err)
	}

	if err := db.SetRetentionRunAt(ctx, ClockTime{Hour: 1, Minute: 15}); err != nil {
		t.Fatal(err)
	}
	got, err = db.ResolveRetentionRunAt(ctx, nil)
	if err != nil || got.Value != (ClockTime{1, 15}) || got.Source != RetentionSourceDatabase {
		t.Fatalf("stored: got %+v, %v; want 01:15 from the database", got, err)
	}

	pin := &RetentionRunAtPin{Value: ClockTime{Hour: 5}, By: "--retention-run-at"}
	got, err = db.ResolveRetentionRunAt(ctx, pin)
	if err != nil || got.Value != (ClockTime{5, 0}) || got.Source != RetentionSourcePinned || got.PinnedBy != pin.By {
		t.Fatalf("pinned: got %+v, %v; want the pin", got, err)
	}

	if err := db.SetRetentionRunAt(ctx, ClockTime{Hour: 24}); err == nil {
		t.Error("SetRetentionRunAt accepted 24:00")
	}

	if _, err := db.Writer.ExecContext(ctx,
		`UPDATE settings SET value = '3.30' WHERE key = ?`, settingRetentionRunAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResolveRetentionRunAt(ctx, nil); err == nil {
		t.Error("a stored value this code did not write was accepted")
	}
}

func TestRetentionPassSurvivesReopen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if p, err := db.LastRetentionPass(ctx); err != nil || p != nil {
		t.Fatalf("fresh database: got %+v, %v; want none", p, err)
	}
	want := RetentionPass{
		StartedAt:     time.Date(2026, 9, 29, 3, 30, 0, 0, time.UTC),
		Duration:      1500 * time.Millisecond,
		Trigger:       "schedule",
		Heartbeats:    120,
		HourlyBuckets: 4,
		Incidents:     1,
		Deliveries:    9,
		FreedBytes:    8192,
		Error:         "disk full",
	}
	if err := db.SaveRetentionPass(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.LastRetentionPass(ctx)
	if err != nil || got == nil {
		t.Fatalf("LastRetentionPass: %+v, %v", got, err)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	got.StartedAt = want.StartedAt
	if *got != want {
		t.Errorf("round trip: got %+v, want %+v", *got, want)
	}
	if got.Succeeded() {
		t.Error("a pass with an error reports success")
	}
}
