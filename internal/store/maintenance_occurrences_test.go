package store

import (
	"testing"
	"time"
)

// inSpans reports whether at falls inside any of spans, [Start, End).
func inSpans(spans []MaintenanceSpan, at time.Time) bool {
	for _, s := range spans {
		if !at.Before(s.Start) && at.Before(s.End) {
			return true
		}
	}
	return false
}

// TestOccurrencesAgreeWithActive samples every quarter hour of a range and
// requires Occurrences and Active to give the same answer at each instant,
// through both Amsterdam DST changes, Lord Howe's half-hour shift, an
// overnight window, a creation time and a one-off window.
func TestOccurrencesAgreeWithActive(t *testing.T) {
	cases := []struct {
		name     string
		w        MaintenanceWindow
		from, to string
	}{
		{"spring forward", MaintenanceWindow{Timezone: "Europe/Amsterdam", LocalTime: "02:30", Weekdays: []int{0}, DurationMinutes: 60},
			"2026-03-20T00:00:00Z", "2026-04-06T00:00:00Z"},
		{"fall back", MaintenanceWindow{Timezone: "Europe/Amsterdam", LocalTime: "02:30", Weekdays: []int{0, 3}, DurationMinutes: 120},
			"2026-10-15T00:00:00Z", "2026-11-02T00:00:00Z"},
		{"Lord Howe", MaintenanceWindow{Timezone: "Australia/Lord_Howe", LocalTime: "01:45", Weekdays: []int{0}, DurationMinutes: 15},
			"2026-03-28T00:00:00Z", "2026-04-12T00:00:00Z"},
		{"a full day", MaintenanceWindow{Timezone: "Europe/Amsterdam", LocalTime: "00:00", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, DurationMinutes: 1440},
			"2026-03-27T00:00:00Z", "2026-04-01T00:00:00Z"},
		{"overnight, created mid-occurrence", MaintenanceWindow{Timezone: "UTC", LocalTime: "23:30", Weekdays: []int{0}, DurationMinutes: 60,
			CreatedAt: time.Date(2026, 9, 20, 23, 50, 0, 0, time.UTC)}, "2026-09-10T00:00:00Z", "2026-10-01T00:00:00Z"},
		{"one-off", MaintenanceWindow{StartsAt: time.Date(2026, 9, 29, 22, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)},
			"2026-09-29T00:00:00Z", "2026-10-01T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, _ := time.Parse(time.RFC3339, c.from)
			to, _ := time.Parse(time.RFC3339, c.to)
			spans := c.w.Occurrences(from, to)
			if len(spans) == 0 {
				t.Fatal("no occurrences in a range chosen to hold some")
			}
			for at := from; at.Before(to); at = at.Add(15 * time.Minute) {
				if got, want := inSpans(spans, at), c.w.Active(at); got != want {
					t.Fatalf("at %s: in an occurrence = %v, Active = %v", at, got, want)
				}
			}
			for i := 1; i < len(spans); i++ {
				if !spans[i-1].Start.Before(spans[i].Start) {
					t.Fatalf("occurrences out of order: %v then %v", spans[i-1], spans[i])
				}
			}
		})
	}
}

// TestOccurrenceRunningAtFromIsReturnedWhole lets a caller say when a window
// that is already running began, instead of claiming it began at from.
func TestOccurrenceRunningAtFromIsReturnedWhole(t *testing.T) {
	w := MaintenanceWindow{Timezone: "UTC", LocalTime: "23:00", Weekdays: []int{1}, DurationMinutes: 180}
	// Monday 28 September 23:00 to Tuesday 02:00; from is inside it.
	from := time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC)
	spans := w.Occurrences(from, from.Add(time.Hour))
	if len(spans) != 1 {
		t.Fatalf("got %d occurrences, want 1: %v", len(spans), spans)
	}
	want := MaintenanceSpan{Start: time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 29, 2, 0, 0, 0, time.UTC)}
	if !spans[0].Start.Equal(want.Start) || !spans[0].End.Equal(want.End) {
		t.Fatalf("occurrence = %v, want %v", spans[0], want)
	}
}

// TestOccurrencesOutsideTheRangeAreLeftOut covers both ends: a one-off that
// ended exactly at from and a weekly start exactly at to are not in [from, to).
func TestOccurrencesOutsideTheRangeAreLeftOut(t *testing.T) {
	from := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ended := MaintenanceWindow{StartsAt: from.Add(-time.Hour), EndsAt: from}
	if got := ended.Occurrences(from, to); len(got) != 0 {
		t.Errorf("window ending at from: got %v, want none", got)
	}
	// Wednesday 30 September 12:00 UTC is exactly to.
	later := MaintenanceWindow{Timezone: "UTC", LocalTime: "12:00", Weekdays: []int{3}, DurationMinutes: 30}
	if got := later.Occurrences(from, to); len(got) != 0 {
		t.Errorf("weekly start at to: got %v, want none", got)
	}
	if got := later.Occurrences(from, to.Add(time.Minute)); len(got) != 1 {
		t.Errorf("weekly start just inside the range: got %v, want one", got)
	}
}
