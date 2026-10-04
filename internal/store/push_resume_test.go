package store

import (
	"context"
	"testing"
	"time"
)

// TestResumeStampsResumedAt is the store half of SUB-209: the moment a paused
// monitor is resumed is kept, so the push watchdog can start a fresh window
// there instead of alarming over reports that were thrown away by design.
func TestResumeStampsResumedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "nightly", 3600, 60)

	if !m.ResumedAt.IsZero() {
		t.Fatalf("a new monitor has resumed_at %v, want none", m.ResumedAt)
	}
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	paused, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !paused.ResumedAt.IsZero() {
		t.Errorf("pausing stamped resumed_at %v", paused.ResumedAt)
	}

	before := time.Now().Add(-time.Second)
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumed, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if resumed.ResumedAt.Before(before) {
		t.Errorf("resumed_at = %v, want the moment of resuming", resumed.ResumedAt)
	}
}

// TestResumingARunningMonitorKeepsResumedAt guards the CASE in resumedAtSQL.
// A second resume, or an edit that carries enabled: true, is not a resume: if
// either moved the stamp, renaming a push monitor would quietly push its
// deadline back by a full window.
func TestResumingARunningMonitorKeepsResumedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "nightly", 3600, 60)

	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("resume a running monitor: %v", err)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.ResumedAt.IsZero() {
		t.Fatalf("resuming a monitor that was never paused stamped resumed_at %v", got.ResumedAt)
	}

	// Pause and resume once, then move the stamp into the past so a second
	// write in the same second could not hide a change.
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := db.Writer.ExecContext(ctx, "UPDATE monitors SET resumed_at = ? WHERE id = ?", old, m.ID); err != nil {
		t.Fatalf("backdate resumed_at: %v", err)
	}

	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	cur, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cur.Name = "nightly, renamed"
	if _, err := db.UpdateMonitor(ctx, cur); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, err = db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ResumedAt.Unix() != old {
		t.Errorf("resumed_at moved to %v on a monitor that was already running", got.ResumedAt)
	}
}

// TestEditThatResumesStampsResumedAt: the edit form can resume a monitor by
// sending enabled: true, and that is a resume like any other.
func TestEditThatResumesStampsResumedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "nightly", 3600, 60)
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	cur, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cur.Enabled = true
	if _, err := db.UpdateMonitor(ctx, cur); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ResumedAt.IsZero() {
		t.Error("an edit that resumed the monitor left resumed_at unset")
	}
}

// TestPushWindow pins the rule the watchdog and the API share. The cases are
// the acceptance criteria of SUB-209, in the order they were written.
func TestPushWindow(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	daily := Monitor{Type: TypePush, PushIntervalS: 24 * 3600, PushGraceS: 3600}
	window := 25 * time.Hour

	cases := []struct {
		name      string
		resumedAt time.Time
		last      time.Time
		listening time.Time
		start     time.Time
		reason    PushWindowReason
	}{{
		name:  "ordinary: the window counts from the last report",
		last:  base,
		start: base, reason: "",
	}, {
		name:      "resumed after the last report: the window counts from the resume",
		last:      base,
		resumedAt: base.Add(30 * time.Hour),
		start:     base.Add(30 * time.Hour), reason: PushWindowResumed,
	}, {
		name:      "resumed before the last report: the report wins",
		last:      base.Add(30 * time.Hour),
		resumedAt: base,
		start:     base.Add(30 * time.Hour), reason: "",
	}, {
		name:      "deadline passed while the process was down: the window counts from the start",
		last:      base,
		listening: base.Add(window + time.Hour),
		start:     base.Add(window + time.Hour), reason: PushWindowRestarted,
	}, {
		// The gap in Uptime Kuma #3599: restarting every window from the
		// start would let an instance that restarts daily never report a
		// daily job that stopped. A deadline still ahead of the start must
		// stay where it is.
		name:      "a short restart before the deadline moves nothing",
		last:      base,
		listening: base.Add(window - time.Minute),
		start:     base, reason: "",
	}, {
		name:      "a resume whose fresh window ran out before a restart: the restart wins",
		last:      base,
		resumedAt: base.Add(time.Hour),
		listening: base.Add(time.Hour + window + time.Minute),
		start:     base.Add(time.Hour + window + time.Minute), reason: PushWindowRestarted,
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := daily
			m.ResumedAt = c.resumedAt
			w := m.PushWindow(c.last, c.listening)
			if !w.Start.Equal(c.start) || w.Reason != c.reason {
				t.Errorf("window = start %v reason %q, want start %v reason %q", w.Start, w.Reason, c.start, c.reason)
			}
			if !w.Deadline.Equal(c.start.Add(window)) {
				t.Errorf("deadline = %v, want one window after %v", w.Deadline, c.start)
			}
		})
	}
}
