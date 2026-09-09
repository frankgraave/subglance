package monitor

import (
	"context"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Pausing or deleting a monitor mid-incident must close the incident.
//
// Without this, jobs() stops listing the monitor, so no further check ever
// arrives to produce a resolve. The incident row stays open forever: the
// dashboard shows a paused monitor as permanently down, restore() re-seeds
// that phantom incident into the engine on every restart, and the partial
// unique index blocks any future incident for that monitor.
func TestPauseResolvesOpenIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "paused-mid-incident", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})

	r.record(outcomeFor(m, "https://example.com", false))
	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Fatalf("expected an open incident: %v", err)
	}

	// Pause it, then let the runner reload as it would in production.
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}

	if inc, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Errorf("incident %d still open after pausing — it can never be resolved now", inc.ID)
	}

	// The incident must be closed, not deleted: the history is still true.
	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want 1 preserved in history", len(incidents))
	}
	if !incidents[0].Resolved() {
		t.Error("the incident should be resolved, not left dangling")
	}
}

// Resuming after a pause must be able to open a fresh incident. If the old one
// was left open, the partial unique index rejects the new one.
func TestResumeAfterPauseCanOpenNewIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "resumable", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})

	r.record(outcomeFor(m, "https://example.com", false))
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}

	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}

	r.record(outcomeFor(m, "https://example.com", false))

	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Errorf("could not open an incident after resume: %v", err)
	}
}

// A monitor that was never in trouble must not gain a phantom incident when
// it is paused.
func TestPausingAHealthyMonitorRecordsNothing(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "healthy-paused", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})
	r.record(outcomeFor(m, "https://example.com", true))

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}

	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 0 {
		t.Errorf("pausing a healthy monitor created %d incidents", len(incidents))
	}
}

// Restarting after a monitor was paused mid-incident must not re-seed the
// phantom: restore() reads open incidents, so a stranded row would put the
// engine back into a down state for a monitor that is not even running.
func TestRestartAfterPauseHasNoPhantomIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "phantom", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})
	r.record(outcomeFor(m, "https://example.com", false))

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}

	// Fresh process against the same database.
	r2 := New(Options{DB: db, Log: quietLogger()})
	if err := r2.restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}

	open, err := db.ListOpenIncidents(ctx)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("restart found %d phantom open incidents for a paused monitor", len(open))
	}
}
