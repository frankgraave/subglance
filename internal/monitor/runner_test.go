package monitor

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// These tests drive the whole path — scheduler, checker, state engine and
// database — against a real HTTP server, because the interesting failures
// (an incident opened but never confirmed, an alert fired twice) only appear
// when the pieces are wired together.

func testDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(context.Background(), store.Options{Path: ":memory:"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// flakyServer serves whatever status the test currently wants.
type flakyServer struct {
	*httptest.Server
	status atomic.Int64
	hits   atomic.Int64
}

func newFlakyServer(t *testing.T) *flakyServer {
	t.Helper()

	fs := &flakyServer{}
	fs.status.Store(http.StatusOK)
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fs.hits.Add(1)
		w.WriteHeader(int(fs.status.Load()))
	}))
	t.Cleanup(fs.Close)
	return fs
}

// alertRecorder collects alerts in a goroutine-safe way.
type alertRecorder struct {
	mu     sync.Mutex
	alerts []Alert
}

func (a *alertRecorder) record(al Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts = append(a.alerts, al)
}

func (a *alertRecorder) events() []state.Event {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]state.Event, len(a.alerts))
	for i, al := range a.alerts {
		out[i] = al.Event
	}
	return out
}

// TestConfirmationDelaysTheAlert is the product promise in test form: a
// monitor with retries=2 must survive one failed check without alerting.
func TestConfirmationDelaysTheAlert(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	srv := newFlakyServer(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name:      "flaky",
		Type:      "http",
		Target:    srv.URL,
		IntervalS: 20,
		TimeoutS:  5,
		Retries:   2,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{
		DB:                  db,
		Log:                 quietLogger(),
		AllowPrivateTargets: true, // httptest binds to loopback
		Notify:              rec.record,
	})

	// Drive the state machine directly rather than waiting on wall-clock
	// intervals: this test is about the decisions, not about timing.
	observe := func(ok bool) {
		status := http.StatusOK
		if !ok {
			status = http.StatusInternalServerError
		}
		srv.status.Store(int64(status))
		r.record(outcomeFor(m, srv.URL, ok))
	}

	// First failure: incident opened, nobody told.
	observe(false)
	if got := rec.events(); len(got) != 0 {
		t.Fatalf("a single failure alerted: %v", got)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("expected an open incident after the first failure: %v", err)
	}
	if inc.Confirmed() {
		t.Error("incident must not be confirmed after one failure")
	}

	// Second failure: confirmed, alert fires exactly once.
	observe(false)
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("events after confirmation = %v, want one incident_confirmed", got)
	}

	// Still failing: no repeat alerts.
	observe(false)
	observe(false)
	if got := rec.events(); len(got) != 1 {
		t.Errorf("repeated failures produced %d alerts, want 1: %v", len(got), got)
	}

	// Recovery: one resolve alert.
	observe(true)
	got := rec.events()
	if len(got) != 2 || got[1] != state.EventIncidentResolved {
		t.Fatalf("events after recovery = %v, want confirmed then resolved", got)
	}

	// And the incident is closed in the database with a sane duration.
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("incident should be resolved in the database")
	}
	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want 1", len(incidents))
	}
	if !incidents[0].Resolved() || !incidents[0].Confirmed() {
		t.Errorf("stored incident: confirmed=%v resolved=%v, want both true",
			incidents[0].Confirmed(), incidents[0].Resolved())
	}
}

// A blip that recovers before the threshold must leave a record but never
// reach a human — not even with an all-clear.
func TestBlipIsSilentEndToEnd(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "blip", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 3, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})

	r.record(outcomeFor(m, "https://example.com", false))
	r.record(outcomeFor(m, "https://example.com", true))

	if got := rec.events(); len(got) != 0 {
		t.Errorf("a blip produced alerts: %v", got)
	}

	// The incident is still recorded — the user can see it happened.
	incidents, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want 1 recorded blip", len(incidents))
	}
	if incidents[0].Confirmed() {
		t.Error("a blip must never be marked confirmed")
	}
	if !incidents[0].Resolved() {
		t.Error("a blip incident should be resolved once the check passes")
	}
}

// A restart must not re-announce an outage the user already heard about.
func TestRestartDoesNotReAlert(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "persistent", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// First process: fail twice, get one alert.
	first := &alertRecorder{}
	r1 := New(Options{DB: db, Log: quietLogger(), Notify: first.record})
	r1.record(outcomeFor(m, "https://example.com", false))
	r1.record(outcomeFor(m, "https://example.com", false))

	if got := first.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("first process events = %v, want one confirmation", got)
	}

	// Second process: fresh engine, same database.
	second := &alertRecorder{}
	r2 := New(Options{DB: db, Log: quietLogger(), Notify: second.record})
	if err := r2.restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}

	r2.record(outcomeFor(m, "https://example.com", false))
	if got := second.events(); len(got) != 0 {
		t.Errorf("restart re-alerted for a known outage: %v", got)
	}

	// Recovery after the restart still reports, once.
	r2.record(outcomeFor(m, "https://example.com", true))
	got := second.events()
	if len(got) != 1 || got[0] != state.EventIncidentResolved {
		t.Errorf("recovery after restart = %v, want one resolution", got)
	}
}

// Threshold 1 means alert immediately, and the incident record must still be
// complete: opened and confirmed, not one without the other.
func TestImmediateThresholdStillRecordsIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "urgent", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.record(outcomeFor(m, "https://example.com", false))

	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("events = %v, want immediate confirmation", got)
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("expected an open incident: %v", err)
	}
	if !inc.Confirmed() {
		t.Error("incident opened at threshold 1 must be confirmed too")
	}
	if inc.Cause == "" {
		t.Error("incident should carry the failure cause")
	}
}

// The alert must name the cause; "something is down" is not actionable.
func TestAlertCarriesCause(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "causal", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	r.record(outcomeFor(m, "https://example.com", false))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(rec.alerts))
	}
	if rec.alerts[0].Monitor.Name != "causal" {
		t.Errorf("alert monitor name = %q, want causal", rec.alerts[0].Monitor.Name)
	}

	// Regression: the confirm path used to hand the notifier a zero-valued
	// incident, because ConfirmIncident is an UPDATE and returns no row. The
	// alert reached the user with no cause and a year-1 timestamp.
	got := rec.alerts[0].Incident
	if got.ID == 0 {
		t.Error("alert carries no incident ID")
	}
	if got.StartedAt.IsZero() {
		t.Error("alert carries a zero start time; the notification cannot say when it began")
	}
	if got.Cause == "" {
		t.Error("alert carries no cause; 'something is down' is not actionable")
	}
	if got.LastError == "" {
		t.Error("alert carries no error text for the notification body")
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if inc.LastError == "" {
		t.Error("incident should record the error text for the notification body")
	}
}

// The same regression, on the two-step path where the incident is opened
// during a pending phase and confirmed on a later check.
func TestAlertAfterPendingCarriesFullIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "patient", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 3, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	for range 3 {
		r.record(outcomeFor(m, "https://example.com", false))
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(rec.alerts))
	}

	got := rec.alerts[0].Incident
	if got.ID == 0 || got.StartedAt.IsZero() || got.Cause == "" {
		t.Errorf("incomplete incident on alert: id=%d started=%v cause=%q",
			got.ID, got.StartedAt, got.Cause)
	}
	if !got.Confirmed() {
		t.Error("the incident attached to a confirmation alert should be confirmed")
	}
	// The outage began at the first failure, not at the third.
	if !got.ConfirmedAt.After(got.StartedAt) && !got.ConfirmedAt.Equal(got.StartedAt) {
		t.Errorf("confirmed_at %v should not precede started_at %v", got.ConfirmedAt, got.StartedAt)
	}
}

// A monitor that is deleted or paused must have its in-memory state dropped.
// Without this the state engine keeps one entry per monitor forever, so an
// instance where monitors come and go leaks memory for the life of the
// process.
func TestDeletedMonitorStateIsForgotten(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "temporary", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})

	// Build up some state: one failure leaves a streak and a pending status.
	r.record(outcomeFor(m, "https://example.com", false))
	if got := r.engine.Status(m.ID); got != state.StatusPending {
		t.Fatalf("status = %q, want pending", got)
	}

	// Delete the monitor and reload the scheduler, which is what happens in
	// production when the API deletes one.
	if err := db.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if err := r.sch.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := r.engine.Status(m.ID); got != state.StatusUnknown {
		t.Errorf("status after deletion = %q, want unknown — state was leaked", got)
	}
}

// Pausing is treated like deletion: a monitor resumed an hour later should be
// judged on what it does now, not resume a stale failure streak.
func TestPausedMonitorStateIsForgotten(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "pausable", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 3, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})

	// Two failures: pending, one short of the threshold.
	r.record(outcomeFor(m, "https://example.com", false))
	r.record(outcomeFor(m, "https://example.com", false))

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}
	if err := r.sch.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := r.engine.Status(m.ID); got != state.StatusUnknown {
		t.Errorf("status after pause = %q, want unknown", got)
	}

	// Resume it. The next failure must start a fresh streak rather than
	// immediately confirming on the third strike from before the pause.
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}

	rec := &alertRecorder{}
	r.notify = rec.record
	r.record(outcomeFor(m, "https://example.com", false))

	if got := rec.events(); len(got) != 0 {
		t.Errorf("first failure after resume alerted: %v — the streak survived the pause", got)
	}
}
