package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// recoveringMonitor creates a monitor that confirms on one failure and needs
// two passes in a row to recover: the schema default.
func recoveringMonitor(t *testing.T, db *store.DB) store.Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "recovers", Type: "http", Target: "https://example.com/health",
		IntervalS: 60, TimeoutS: 5, Retries: 1, Enabled: true, RepeatAfterS: 60,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if m.RecoveryThreshold != 2 {
		t.Fatalf("recovery threshold = %d, want the default of 2", m.RecoveryThreshold)
	}
	return m
}

// End to end through the runner and the database: one pass leaves the
// incident open and silent, the second closes it, and the stored resolution
// time is the first pass rather than the second.
func TestRecoveryWaitsForTheThresholdAndRecordsTheFirstPass(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m := recoveringMonitor(t, db)

	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := r.recordOutcome(at(outcomeFor(m, m.Target, false), start)); err != nil {
		t.Fatal(err)
	}
	firstPass := start.Add(time.Minute)
	if err := r.recordOutcome(at(outcomeFor(m, m.Target, true), firstPass)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Fatalf("one pass closed the incident: %v", err)
	}
	if got := r.Engine().Status(m.ID); got != state.StatusRecovering {
		t.Errorf("status after one pass = %q, want recovering", got)
	}
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts after one pass = %v, want only the confirmation", got)
	}
	hb, err := db.LatestHeartbeat(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hb.Assessment != "up" {
		t.Errorf("a recovering pass is stored as %q, want up: it is not downtime", hb.Assessment)
	}

	if err := r.recordOutcome(at(outcomeFor(m, m.Target, true), start.Add(2*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 2 || got[1] != state.EventIncidentResolved {
		t.Fatalf("alerts after two passes = %v, want confirmed then resolved", got)
	}
	incs, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil || len(incs) != 1 {
		t.Fatalf("incidents = %v (err %v), want exactly one", incs, err)
	}
	if !incs[0].ResolvedAt.Equal(firstPass.UTC()) {
		t.Errorf("resolved at %v, want the first pass at %v", incs[0].ResolvedAt, firstPass.UTC())
	}
}

// A recovering monitor is passing its checks. "Still down" would be false.
func TestARecoveringMonitorIsNotReminded(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	clock := time.Now()
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.now = func() time.Time { return clock }
	m := recoveringMonitor(t, db)

	r.record(outcomeFor(m, m.Target, false))
	r.record(outcomeFor(m, m.Target, true))

	clock = clock.Add(2 * time.Hour)
	r.sendDueReminders(ctx)
	if got := rec.events(); len(got) != 1 {
		t.Errorf("alerts = %v, want only the confirmation: a recovering monitor was reminded", got)
	}
}

// A push report is the job speaking for itself, not a sample, so one good
// report closes the incident whatever the stored threshold says.
func TestAPushMonitorRecoversOnOneReport(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)
	m := mustPushMonitor(t, db, 60, 0)
	if m.RecoveryThreshold < 2 {
		t.Fatalf("precondition: stored threshold = %d, want at least 2 so the override is exercised", m.RecoveryThreshold)
	}

	if err := r.RecordPush(ctx, m, PushReport{OK: false}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPush(ctx, m, PushReport{OK: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("the incident is still open after one good report")
	}
}
