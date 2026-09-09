package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// Flapping suppression must silence notifications without also silencing the
// database.
//
// The failure this guards against: a monitor recovers at the exact moment its
// oscillation crosses the flap threshold. If the flapping signal replaces the
// resolve event, the runner logs "flapping" and never closes the incident.
// The engine then believes the monitor is up while the database holds an open,
// confirmed incident — so the dashboard shows red forever, and the partial
// unique index blocks every future incident for that monitor.
func TestFlappingDoesNotStrandAnOpenIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "flapper", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{
		DB:            db,
		Log:           quietLogger(),
		Notify:        rec.record,
		FlapWindow:    10 * time.Minute,
		FlapThreshold: 4, // two full down/up cycles
	})

	// Oscillate: down, up, down, up. The fourth flip trips the threshold at
	// the same moment the monitor recovers.
	for range 2 {
		r.record(outcomeFor(m, "https://example.com", false))
		r.record(outcomeFor(m, "https://example.com", true))
	}

	if !r.engine.Flapping(m.ID) {
		t.Fatal("expected the monitor to be flapping after four flips")
	}

	// The engine says up. The database must agree.
	if got := r.engine.Status(m.ID); got != state.StatusUp {
		t.Fatalf("engine status = %q, want up", got)
	}

	if inc, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Errorf("incident %d left open after recovery (confirmed=%v) — "+
			"the flapping signal swallowed the resolve",
			inc.ID, inc.Confirmed())
	}

	// And a later failure must still be able to open a fresh incident. If the
	// previous one was stranded, the partial unique index rejects this.
	r.record(outcomeFor(m, "https://example.com", false))

	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Errorf("could not open a new incident after flapping: %v", err)
	}
}

// The mirror case: flapping trips on a failure rather than a recovery. The
// incident must still be opened and confirmed in the database, even though
// nobody is notified.
func TestFlappingStillRecordsIncidents(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "flapper2", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{
		DB:            db,
		Log:           quietLogger(),
		Notify:        rec.record,
		FlapWindow:    10 * time.Minute,
		FlapThreshold: 3, // trips on the third flip, which is a failure
	})

	r.record(outcomeFor(m, "https://example.com", false)) // change 1
	r.record(outcomeFor(m, "https://example.com", true))  // change 2
	r.record(outcomeFor(m, "https://example.com", false)) // change 3 — trips

	if !r.engine.Flapping(m.ID) {
		t.Fatal("expected flapping")
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("the failure that tripped flapping did not record an incident: %v", err)
	}
	if !inc.Confirmed() {
		t.Error("incident should be confirmed; suppression hides the alert, not the record")
	}
}

// While flapping, notifications are withheld but the incident lifecycle must
// keep running in the database, so history stays accurate.
func TestSuppressedTransitionsStillPersist(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "flapper3", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{
		DB: db, Log: quietLogger(), Notify: rec.record,
		FlapWindow: 10 * time.Minute, FlapThreshold: 2,
	})

	// Trip flapping quickly, then keep oscillating.
	for range 4 {
		r.record(outcomeFor(m, "https://example.com", false))
		r.record(outcomeFor(m, "https://example.com", true))
	}

	incidents, err := db.ListIncidents(ctx, m.ID, 50)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) < 4 {
		t.Errorf("got %d incidents, want one per down cycle — suppression must not skip the record", len(incidents))
	}
	for _, inc := range incidents {
		if !inc.Resolved() {
			t.Errorf("incident %d left unresolved after the monitor recovered", inc.ID)
		}
	}

	// Notifications, on the other hand, should be few.
	if got := len(rec.events()); got > 3 {
		t.Errorf("sent %d notifications while flapping; suppression is not working", got)
	}
}
