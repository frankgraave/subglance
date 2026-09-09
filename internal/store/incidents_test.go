package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIncidentLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m := mustCreateMonitor(t, db, "example")
	start := time.Now().Truncate(time.Second)

	// Open.
	inc, err := db.OpenIncident(ctx, m.ID, start, "status", "unexpected status 500")
	if err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	if inc.ID == 0 {
		t.Fatal("expected an assigned incident ID")
	}
	if inc.Confirmed() {
		t.Error("a freshly opened incident must not be confirmed")
	}

	// Confirm.
	confirmAt := start.Add(time.Minute)
	if err := db.ConfirmIncident(ctx, m.ID, confirmAt, "status", "unexpected status 500"); err != nil {
		t.Fatalf("ConfirmIncident: %v", err)
	}

	got, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if !got.Confirmed() {
		t.Error("incident should be confirmed")
	}
	if !got.ConfirmedAt.Equal(confirmAt.UTC()) {
		t.Errorf("confirmed_at = %v, want %v", got.ConfirmedAt, confirmAt.UTC())
	}
	// The outage started at the first failure, not at confirmation.
	if !got.StartedAt.Equal(start.UTC()) {
		t.Errorf("started_at = %v, want %v", got.StartedAt, start.UTC())
	}

	// Confirming twice must not move the timestamp: a restart re-running the
	// path should not rewrite when the outage was confirmed.
	if err := db.ConfirmIncident(ctx, m.ID, confirmAt.Add(time.Hour), "status", "still 500"); err != nil {
		t.Fatalf("second ConfirmIncident: %v", err)
	}
	got, _ = db.OpenIncidentFor(ctx, m.ID)
	if !got.ConfirmedAt.Equal(confirmAt.UTC()) {
		t.Errorf("confirmed_at moved on re-confirm: %v", got.ConfirmedAt)
	}
	if got.LastError != "still 500" {
		t.Errorf("last_error should still update: got %q", got.LastError)
	}

	// Resolve.
	resolveAt := start.Add(5 * time.Minute)
	resolved, err := db.ResolveIncident(ctx, m.ID, resolveAt)
	if err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
	if !resolved.Confirmed() {
		t.Error("resolved incident should report it was confirmed, so the notifier can send an all-clear")
	}
	if d := resolved.Duration(); d != 5*time.Minute {
		t.Errorf("duration = %v, want 5m", d)
	}

	if _, err := db.OpenIncidentFor(ctx, m.ID); !errors.Is(err, ErrNoOpenIncident) {
		t.Errorf("after resolve, OpenIncidentFor error = %v, want ErrNoOpenIncident", err)
	}
}

// Same guarantee as TestOnlyOneOpenIncidentPerMonitor, but through the Go API:
// OpenIncident must surface the index violation rather than swallowing it.
func TestOpenIncidentRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := mustCreateMonitor(t, db, "example")

	if _, err := db.OpenIncident(ctx, m.ID, time.Now(), "status", "500"); err != nil {
		t.Fatalf("first open: %v", err)
	}

	_, err := db.OpenIncident(ctx, m.ID, time.Now(), "status", "500")
	if err == nil {
		t.Fatal("expected the second open incident to be rejected by the unique index")
	}
	// The caller has to be able to tell a benign race from a broken database,
	// so the collision must surface as its own error rather than a raw driver
	// message.
	if !errors.Is(err, ErrIncidentAlreadyOpen) {
		t.Errorf("error = %v, want ErrIncidentAlreadyOpen", err)
	}
}

// Once resolved, a new incident for the same monitor must be allowed —
// otherwise a monitor could only ever fail once.
func TestNewIncidentAfterResolve(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := mustCreateMonitor(t, db, "example")

	t0 := time.Now().Truncate(time.Second)
	if _, err := db.OpenIncident(ctx, m.ID, t0, "status", "500"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.ResolveIncident(ctx, m.ID, t0.Add(time.Minute)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := db.OpenIncident(ctx, m.ID, t0.Add(2*time.Minute), "timeout", "deadline exceeded"); err != nil {
		t.Fatalf("second open after resolve: %v", err)
	}

	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 2 {
		t.Fatalf("got %d incidents, want 2", len(incidents))
	}
	// Newest first.
	if incidents[0].Cause != "timeout" {
		t.Errorf("expected newest incident first, got cause %q", incidents[0].Cause)
	}
}

func TestResolveWithoutOpenIncident(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := mustCreateMonitor(t, db, "example")

	if _, err := db.ResolveIncident(ctx, m.ID, time.Now()); !errors.Is(err, ErrNoOpenIncident) {
		t.Errorf("error = %v, want ErrNoOpenIncident", err)
	}
}

func TestListOpenIncidents(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	a := mustCreateMonitor(t, db, "a")
	b := mustCreateMonitor(t, db, "b")
	c := mustCreateMonitor(t, db, "c")

	now := time.Now()
	for _, m := range []Monitor{a, b, c} {
		if _, err := db.OpenIncident(ctx, m.ID, now, "status", "500"); err != nil {
			t.Fatalf("open for %s: %v", m.Name, err)
		}
	}
	if _, err := db.ResolveIncident(ctx, b.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolve b: %v", err)
	}

	open, err := db.ListOpenIncidents(ctx)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("got %d open incidents, want 2", len(open))
	}
	for _, inc := range open {
		if inc.MonitorID == b.ID {
			t.Error("resolved incident should not appear in the open list")
		}
	}
}

func TestAckIncident(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := mustCreateMonitor(t, db, "example")

	now := time.Now().Truncate(time.Second)
	inc, err := db.OpenIncident(ctx, m.ID, now, "status", "500")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	ackAt := now.Add(30 * time.Second)
	if err := db.AckIncident(ctx, inc.ID, ackAt); err != nil {
		t.Fatalf("AckIncident: %v", err)
	}

	got, _ := db.OpenIncidentFor(ctx, m.ID)
	if got.AckedAt.IsZero() {
		t.Fatal("expected acked_at to be set")
	}

	// Acknowledging again keeps the first timestamp — who saw it first is the
	// useful fact.
	if err := db.AckIncident(ctx, inc.ID, ackAt.Add(time.Hour)); err != nil {
		t.Fatalf("second ack: %v", err)
	}
	got, _ = db.OpenIncidentFor(ctx, m.ID)
	if !got.AckedAt.Equal(ackAt.UTC()) {
		t.Errorf("acked_at moved on re-ack: %v", got.AckedAt)
	}
}

// Deleting a monitor must take its incidents with it; ON DELETE CASCADE only
// works when the foreign_keys pragma actually took effect.
func TestIncidentsCascadeOnMonitorDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := mustCreateMonitor(t, db, "example")

	if _, err := db.OpenIncident(ctx, m.ID, time.Now(), "status", "500"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 0 {
		t.Errorf("got %d incidents after deleting the monitor, want 0", len(incidents))
	}
}

func mustCreateMonitor(t *testing.T, db *DB, name string) Monitor {
	t.Helper()

	m, err := db.CreateMonitor(context.Background(), Monitor{
		Name:    name,
		Type:    "http",
		Target:  "https://" + name + ".example.com",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor(%s): %v", name, err)
	}
	return m
}
