package state_test

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

func TestReminderGapEscalates(t *testing.T) {
	base := 15 * time.Minute

	tests := []struct {
		sent int
		want time.Duration
	}{
		{0, 15 * time.Minute},
		{1, time.Hour},
		{2, 4 * time.Hour},
		{3, 16 * time.Hour},
		{4, state.ReminderCap},
		{20, state.ReminderCap},
	}

	for _, tc := range tests {
		if got := state.ReminderGap(base, tc.sent); got != tc.want {
			t.Errorf("ReminderGap(15m, %d) = %v, want %v", tc.sent, got, tc.want)
		}
	}
}

// A schedule that only ever grew would be fine; one that jumps past the cap
// would mean a forgotten incident goes quiet for days. The cap has to clamp,
// not be skipped over.
func TestReminderGapNeverExceedsTheCap(t *testing.T) {
	for _, base := range []time.Duration{time.Minute, time.Hour, 23 * time.Hour, 86400 * time.Second} {
		for sent := 0; sent < 12; sent++ {
			if got := state.ReminderGap(base, sent); got > state.ReminderCap {
				t.Fatalf("ReminderGap(%v, %d) = %v, above the cap %v",
					base, sent, got, state.ReminderCap)
			}
		}
	}
}

func TestReminderDisabledAtZero(t *testing.T) {
	if state.ReminderEnabled(0) {
		t.Error("ReminderEnabled(0) = true, want false")
	}
	if got := state.ReminderGap(0, 3); got != 0 {
		t.Errorf("ReminderGap(0, 3) = %v, want 0", got)
	}

	now := time.Now()
	if state.ReminderDue(now.Add(time.Hour), now, time.Time{}, 0, 0) {
		t.Error("a monitor with reminders disabled reported a reminder due")
	}
}

// The first reminder is measured from confirmation, not from the incident's
// start: the confirmation delay is patience the user already paid for with
// `retries`, and charging it twice would delay the reminder by that much.
func TestFirstReminderRunsFromConfirmation(t *testing.T) {
	started := time.Date(2026, 9, 13, 2, 30, 0, 0, time.UTC)
	confirmed := started.Add(5 * time.Minute)
	base := 15 * time.Minute

	got := state.NextReminder(confirmed, time.Time{}, 0, base)
	if want := confirmed.Add(base); !got.Equal(want) {
		t.Errorf("NextReminder = %v, want %v", got, want)
	}
	if got.Equal(started.Add(base)) {
		t.Error("the first reminder was measured from the incident start, not from confirmation")
	}
}

func TestReminderDueWalksTheSchedule(t *testing.T) {
	confirmed := time.Date(2026, 9, 13, 2, 40, 0, 0, time.UTC)
	base := 15 * time.Minute

	if state.ReminderDue(confirmed.Add(14*time.Minute), confirmed, time.Time{}, 0, base) {
		t.Error("a reminder came due before the first interval had elapsed")
	}
	if !state.ReminderDue(confirmed.Add(15*time.Minute), confirmed, time.Time{}, 0, base) {
		t.Error("the first reminder did not come due on time")
	}

	// After one reminder the clock restarts from that reminder, on the longer
	// second gap.
	first := confirmed.Add(15 * time.Minute)
	if state.ReminderDue(first.Add(59*time.Minute), confirmed, first, 1, base) {
		t.Error("the second reminder came due before its longer gap elapsed")
	}
	if !state.ReminderDue(first.Add(time.Hour), confirmed, first, 1, base) {
		t.Error("the second reminder did not come due after its gap")
	}
}

// An unconfirmed incident is the single failed probe the engine deliberately
// stays quiet about. Reminding about it would announce an outage nobody was
// ever told had started.
func TestUnconfirmedIncidentNeverReminds(t *testing.T) {
	now := time.Now()
	if state.ReminderDue(now, time.Time{}, time.Time{}, 0, 15*time.Minute) {
		t.Error("an unconfirmed incident reported a reminder due")
	}
}
