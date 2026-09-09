package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// Shutting down must not manufacture an outage.
//
// Every checker turns a cancelled context into a failed Result, and the
// scheduler waits for in-flight checks before it stops. Without a way to tell
// those apart from real failures, every restart would push a burst of
// failures through the state engine: incidents opened on healthy monitors,
// alerts sent for outages that never happened, and a red bar in the timeline
// at every deploy.
func TestShutdownDoesNotCreateFalseFailures(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "healthy", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})

	// A healthy check first, so the monitor is known to be up.
	r.record(outcomeFor(m, "https://example.com", true))
	if got := r.engine.Status(m.ID); got != state.StatusUp {
		t.Fatalf("status = %q, want up", got)
	}

	// Now several checks aborted by shutdown, as the scheduler reports them.
	for range 5 {
		r.record(abortedOutcome(m))
	}

	if got := rec.events(); len(got) != 0 {
		t.Errorf("shutdown produced alerts: %v", got)
	}
	if got := r.engine.Status(m.ID); got != state.StatusUp {
		t.Errorf("status after shutdown = %q, want up — cancelled checks were treated as failures", got)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("shutdown opened an incident for a healthy monitor")
	}

	// The heartbeat timeline must not show phantom failures either: a red bar
	// at every deploy would dent the uptime figure for no reason.
	hbs, err := db.ListHeartbeats(ctx, m.ID, 100)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != 1 {
		t.Errorf("recorded %d heartbeats, want 1 — aborted checks should not be stored", len(hbs))
	}
	for _, hb := range hbs {
		if !hb.OK {
			t.Errorf("a failed heartbeat was recorded from a cancelled check: %s", hb.Error)
		}
	}
}

// A monitor that is genuinely down when shutdown starts must keep its
// incident: dropping aborted checks must not roll back state that was already
// established.
func TestShutdownPreservesAnExistingIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "broken", Type: "http", Target: "https://example.com",
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

	r.record(abortedOutcome(m))

	if got := r.engine.Status(m.ID); got != state.StatusDown {
		t.Errorf("status = %q, want down — an aborted check must not resolve a real incident", got)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Errorf("the incident was closed by an aborted check: %v", err)
	}
}

// abortedOutcome is what the scheduler reports for a check cut short by
// shutdown: a failed result, flagged as aborted.
func abortedOutcome(m store.Monitor) scheduler.Outcome {
	cm := toCheckerMonitor(m)
	return scheduler.Outcome{
		Monitor: cm,
		Result: checker.Result{
			OK:        false,
			Kind:      checker.FailInternal,
			Error:     "check cancelled",
			CheckedAt: time.Now(),
		},
		Aborted: true,
	}
}
