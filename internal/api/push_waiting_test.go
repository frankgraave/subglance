package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

// listeningPusher is a push recorder that also says when it started
// listening, the way the runner does.
type listeningPusher struct {
	fakePusher
	since time.Time
}

func (l *listeningPusher) PushListeningSince() time.Time { return l.since }

var _ PushRecorder = (*listeningPusher)(nil)

func readMonitor(t *testing.T, srv *Server, id int64) monitorResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+itoa(id), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET monitor: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// TestPushMonitorSaysWhyItIsWaiting: while a push monitor waits out the fresh
// window a resume or a restart gave it, it must not read "up" on the strength
// of a report from before the gap, and it must say why it is waiting. The
// watchdog will not raise anything in that window, so "up" would be a claim
// nobody is checking.
func TestPushMonitorSaysWhyItIsWaiting(t *testing.T) {
	srv, db := testServerWithDB(t)
	pusher := &listeningPusher{}
	srv.WithPushRecorder(pusher)
	ctx := context.Background()

	created := createPushMonitor(t, srv, pushMonitorBody) // hourly, 5 min grace
	beat := func(at time.Time) {
		t.Helper()
		if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: created.ID, TS: at, OK: true}); err != nil {
			t.Fatalf("record heartbeat: %v", err)
		}
	}

	// Reported ten minutes ago and never paused: up, and nothing to explain.
	beat(time.Now().Add(-10 * time.Minute))
	if got := readMonitor(t, srv, created.ID); got.Status != "up" || got.PushWaiting != "" {
		t.Fatalf("ordinary report: status %q push_waiting %q, want up and none", got.Status, got.PushWaiting)
	}

	// Paused and resumed after that report: waiting, because of the resume.
	if err := db.SetMonitorEnabled(ctx, created.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := db.SetMonitorEnabled(ctx, created.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	got := readMonitor(t, srv, created.ID)
	if got.Status != "pending" || got.PushWaiting != "resumed" {
		t.Fatalf("after resume: status %q push_waiting %q, want pending and resumed", got.Status, got.PushWaiting)
	}
	if got.LastCheck == nil {
		t.Error("after resume: last_check vanished; the old report still happened")
	}

	// The job reports: up again, and the explanation is gone.
	beat(time.Now())
	if got := readMonitor(t, srv, created.ID); got.Status != "up" || got.PushWaiting != "" {
		t.Fatalf("after the next report: status %q push_waiting %q, want up and none", got.Status, got.PushWaiting)
	}

	// The process started after the last report's deadline: waiting, because
	// of the restart. The heartbeat is moved back rather than the clock
	// forward, so the deadline (65 minutes) falls before the start.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE heartbeats SET ts = ? WHERE monitor_id = ?", time.Now().Add(-3*time.Hour).Unix(), created.ID); err != nil {
		t.Fatalf("move heartbeats back: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET resumed_at = NULL WHERE id = ?", created.ID); err != nil {
		t.Fatalf("clear resumed_at: %v", err)
	}
	pusher.since = time.Now().Add(-time.Minute)
	got = readMonitor(t, srv, created.ID)
	if got.Status != "pending" || got.PushWaiting != "restarted" {
		t.Fatalf("after a start past the deadline: status %q push_waiting %q, want pending and restarted", got.Status, got.PushWaiting)
	}
}

// TestPushWaitingNeverHidesAnOutage: a confirmed incident is the one thing a
// resume does not soften. The monitor stays down until the job reports.
func TestPushWaitingNeverHidesAnOutage(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithPushRecorder(&listeningPusher{})
	ctx := context.Background()
	created := createPushMonitor(t, srv, pushMonitorBody)

	if err := db.RecordHeartbeat(ctx, store.Heartbeat{
		MonitorID: created.ID, TS: time.Now().Add(-2 * time.Hour), OK: false, Error: "no report",
	}); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	started := time.Now().Add(-2 * time.Hour)
	if _, err := db.OpenIncident(ctx, created.ID, started, "push_overdue", "no report"); err != nil {
		t.Fatalf("open incident: %v", err)
	}
	if err := db.ConfirmIncident(ctx, created.ID, started, "push_overdue", "no report"); err != nil {
		t.Fatalf("confirm incident: %v", err)
	}
	if err := db.SetMonitorEnabled(ctx, created.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := db.SetMonitorEnabled(ctx, created.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}

	got := readMonitor(t, srv, created.ID)
	if got.Status != "down" || got.PushWaiting != "" {
		t.Errorf("status %q push_waiting %q on a monitor with a confirmed incident, want down and none", got.Status, got.PushWaiting)
	}
}

// TestPushWaitingHoldsBeforeTheRunnerRuns: main marks the runner as listening
// before the API serves anything, so a request that lands while Run is still
// restoring state already gets the restart rule. With the zero time, a report
// from before the downtime would read "up" while the watchdog is about to
// treat the monitor as unheard.
func TestPushWaitingHoldsBeforeTheRunnerRuns(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := context.Background()
	runner := monitor.New(monitor.Options{DB: db, Log: testLogger()})
	// The same composition as cmd/subglance: the runner is the push recorder,
	// marked as listening, and Run has not started.
	srv.WithPushRecorder(runner)
	runner.MarkPushListening()

	created := createPushMonitor(t, srv, pushMonitorBody) // hourly, 5 min grace
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: created.ID, TS: time.Now().Add(-3 * time.Hour), OK: true}); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	got := readMonitor(t, srv, created.ID)
	if got.Status != "pending" || got.PushWaiting != "restarted" {
		t.Fatalf("before Run: status %q push_waiting %q, want pending and restarted", got.Status, got.PushWaiting)
	}
}
