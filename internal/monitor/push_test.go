package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func newPushRunner(t *testing.T, db *store.DB, rec *alertRecorder) *Runner {
	t.Helper()
	opts := Options{DB: db, Log: quietLogger(), PushSweep: 10 * time.Millisecond}
	if rec != nil {
		opts.Notify = rec.record
	}
	return New(opts)
}

// newPushRunnerWithBus is newPushRunner plus a live event bus, so a test can
// wait for a heartbeat to be published instead of polling the database.
func newPushRunnerWithBus(t *testing.T, db *store.DB, bus *events.Bus) *Runner {
	t.Helper()
	return New(Options{DB: db, Log: quietLogger(), PushSweep: 10 * time.Millisecond, Bus: bus})
}

func mustPushMonitor(t *testing.T, db *store.DB, every, grace int) store.Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name:          "nightly job",
		Type:          store.TypePush,
		PushIntervalS: every,
		PushGraceS:    grace,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create push monitor: %v", err)
	}
	return m
}

// backdate moves a monitor's creation time into the past so the watchdog sees
// a closed window without the test having to sleep through a real one.
func backdate(t *testing.T, db *store.DB, id int64, d time.Duration) {
	t.Helper()
	if _, err := db.Writer.ExecContext(context.Background(),
		"UPDATE monitors SET created_at = ? WHERE id = ?",
		time.Now().Add(-d).Unix(), id); err != nil {
		t.Fatalf("backdate monitor: %v", err)
	}
}

// TestRecordPushWritesAHeartbeat is the core claim of the feature: a report
// from a job is an ordinary heartbeat, so everything downstream keeps working
// without knowing push exists.
func TestRecordPushWritesAHeartbeat(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)
	m := mustPushMonitor(t, db, 3600, 60)

	if err := r.RecordPush(ctx, m, PushReport{OK: true}); err != nil {
		t.Fatalf("RecordPush: %v", err)
	}

	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(beats))
	}
	if !beats[0].OK {
		t.Error("a successful report was recorded as a failure")
	}
}

// TestPushReportedFailureOpensAConfirmedIncident checks that a job saying it
// failed is believed at once, rather than waiting out the retries column.
func TestPushReportedFailureOpensAConfirmedIncident(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rec := &alertRecorder{}
	r := newPushRunner(t, db, rec)

	m := mustPushMonitor(t, db, 3600, 60)
	// A retries value that would make a probe-based monitor wait.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET retries = 5 WHERE id = ?", m.ID); err != nil {
		t.Fatalf("set retries: %v", err)
	}
	m, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if err := r.RecordPush(ctx, m, PushReport{OK: false, Message: "restic: repo locked"}); err != nil {
		t.Fatalf("RecordPush: %v", err)
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("expected an open incident: %v", err)
	}
	if !inc.Confirmed() {
		t.Error("a self-reported failure did not confirm immediately; retries must not apply to push")
	}
	if inc.LastError != "restic: repo locked" {
		t.Errorf("incident error = %q, want the job's own message", inc.LastError)
	}
	if got := rec.events(); len(got) == 0 {
		t.Error("nobody was notified about a job that reported its own failure")
	}
}

// TestRecordPushReportsAFailedWrite is the reason RecordPush returns an error
// at all. The handler answers the job with `recorded`, and a report that was
// acknowledged but never stored is the one lie a dead man's switch cannot
// afford: the next window would go quiet with nothing in the timeline to say
// the job ever reported.
//
// The monitor is deleted underneath the in-memory copy, which is exactly the
// race a long-lived push URL can hit: the heartbeat insert then fails on the
// foreign key.
func TestRecordPushReportsAFailedWrite(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)
	m := mustPushMonitor(t, db, 3600, 60)

	if _, err := db.Writer.ExecContext(ctx,
		"DELETE FROM monitors WHERE id = ?", m.ID); err != nil {
		t.Fatalf("delete monitor: %v", err)
	}

	if err := r.RecordPush(ctx, m, PushReport{OK: true}); err == nil {
		t.Fatal("RecordPush reported success for a heartbeat that was never stored")
	}
}

// TestRecordPushRefusesNonPushMonitors keeps the push path from being a second
// way to write heartbeats for a probe-based monitor.
func TestRecordPushRefusesNonPushMonitors(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "http one", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := r.RecordPush(ctx, m, PushReport{OK: true}); err == nil {
		t.Fatal("RecordPush accepted an http monitor")
	}
	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 0 {
		t.Errorf("%d heartbeats written for a rejected report", len(beats))
	}
}

// TestWatchdogDeclaresOverdueMonitorsDown is the half of the feature that
// cannot be a checker: nothing happening has to be noticed by someone going
// out to look for it.
func TestWatchdogDeclaresOverdueMonitorsDown(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	// Expected every 60s with no grace, created two hours ago: overdue.
	m := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, m.ID, 2*time.Hour)

	r.sweepOverduePushMonitors(ctx)

	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 1 {
		t.Fatalf("got %d heartbeats, want 1 synthetic failure", len(beats))
	}
	if beats[0].OK {
		t.Error("the overdue beat was recorded as a success")
	}
	if beats[0].Error == "" {
		t.Error("the overdue beat carries no explanation")
	}

	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("expected an open incident: %v", err)
	}
	if !inc.Confirmed() {
		t.Error("an overdue push monitor did not confirm; the grace period is the patience")
	}
}

// TestWatchdogLeavesMonitorsInsideTheirWindowAlone is the false-alarm test. A
// job that reported a moment ago must not be touched, however long its window.
func TestWatchdogLeavesMonitorsInsideTheirWindowAlone(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)
	m := mustPushMonitor(t, db, 3600, 60)

	r.sweepOverduePushMonitors(ctx)

	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 0 {
		t.Fatalf("%d beats written for a monitor well inside its window", len(beats))
	}
}

// TestWatchdogRespectsGrace is the whole reason grace is a separate column: a
// job that is slightly late is healthy, and only the second number can say so.
func TestWatchdogRespectsGrace(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	// Expected every 60s, allowed to be 10 minutes late, last seen 5 minutes
	// ago. Past the interval, inside the grace.
	m := mustPushMonitor(t, db, 60, 600)
	backdate(t, db, m.ID, 5*time.Minute)

	r.sweepOverduePushMonitors(ctx)

	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 0 {
		t.Fatalf("%d beats written for a job still inside its grace period", len(beats))
	}
}

// TestWatchdogDoesNotRepeatEverySweep guards the property that makes the
// synthetic beat's timestamp matter: dating it at the deadline means the next
// deadline is one full window later, so a week of silence produces one beat
// per window rather than one per sweep.
func TestWatchdogDoesNotRepeatEverySweep(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	m := mustPushMonitor(t, db, 3600, 0)
	backdate(t, db, m.ID, 90*time.Minute)

	for range 5 {
		r.sweepOverduePushMonitors(ctx)
	}

	beats, err := db.ListHeartbeats(ctx, m.ID, 20)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 1 {
		t.Errorf("got %d beats from 5 sweeps, want 1 — the watchdog is beating per sweep", len(beats))
	}
}

// TestWatchdogSkipsPausedMonitors: a paused monitor is one the instance
// promised not to watch, and manufacturing failures for it would misrepresent
// an unmonitored period as a monitored one.
func TestWatchdogSkipsPausedMonitors(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	m := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, m.ID, 2*time.Hour)
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}

	r.sweepOverduePushMonitors(ctx)

	beats, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(beats) != 0 {
		t.Errorf("%d beats written for a paused push monitor", len(beats))
	}
}

// TestPushReportResolvesAnOverdueIncident closes the loop: the job comes back,
// and the ordinary resolve path handles it with no push-specific code.
func TestPushReportResolvesAnOverdueIncident(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	m := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, m.ID, 2*time.Hour)
	r.sweepOverduePushMonitors(ctx)

	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Fatalf("setup: expected an open incident, got %v", err)
	}

	if err := r.RecordPush(ctx, m, PushReport{OK: true}); err != nil {
		t.Fatalf("RecordPush: %v", err)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("the incident is still open after the job reported in")
	}
	if got := r.Engine().Status(m.ID); got != state.StatusUp {
		t.Errorf("status = %v, want up", got)
	}
}

// TestPushMonitorsAreNeverScheduled is the scheduler's side of the contract.
// Queueing one would dispatch a check with no checker behind it, producing an
// "unsupported monitor type" failure on every interval.
func TestPushMonitorsAreNeverScheduled(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	push := mustPushMonitor(t, db, 3600, 0)
	if _, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "http one", Type: "http", Target: "https://example.com", Enabled: true,
	}); err != nil {
		t.Fatalf("create http monitor: %v", err)
	}

	jobs, err := r.jobs(ctx)
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	for _, j := range jobs {
		if j.Monitor.ID == push.ID {
			t.Fatal("a push monitor was queued for dialling")
		}
	}
	if len(jobs) != 1 {
		t.Errorf("got %d jobs, want just the http monitor", len(jobs))
	}

	// And its incidents must survive reconciliation: being unscheduled is
	// not the same as being gone, and closing them would make a down push
	// monitor flip back to healthy on the next reload.
	backdate(t, db, push.ID, 2*time.Hour)
	r.sweepOverduePushMonitors(ctx)
	if _, err := db.OpenIncidentFor(ctx, push.ID); err != nil {
		t.Fatalf("setup: expected an open incident, got %v", err)
	}
	if _, err := r.jobs(ctx); err != nil {
		t.Fatalf("jobs: %v", err)
	}
	if _, err := db.OpenIncidentFor(ctx, push.ID); err != nil {
		t.Error("reconciliation closed a live push monitor's incident")
	}
}

// TestCheckNowRefusesPushMonitors: there is nothing to probe on demand, and
// the error should say why rather than blaming a missing checker.
func TestCheckNowRefusesPushMonitors(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)
	m := mustPushMonitor(t, db, 3600, 0)

	if _, err := r.CheckNow(ctx, m); err == nil {
		t.Fatal("CheckNow probed a push monitor")
	}
}

// TestWatchdogRunsUnderRun proves the watchdog is actually started by Run and
// stopped by its context, rather than only working when a test calls the sweep
// by hand.
func TestWatchdogRunsUnderRun(t *testing.T) {
	db := testDB(t)
	bus := events.NewBus(8)
	r := newPushRunnerWithBus(t, db, bus)
	m := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, m.ID, 2*time.Hour)

	// Subscribe before Run starts, so the first sweep's beat cannot be missed
	// between starting the runner and starting to listen.
	sub := bus.Subscribe()
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait for the watchdog's own signal rather than polling the clock. The
	// timeout is only a failure guard: the test passes on the event, so how
	// busy the machine is changes how long it waits, not what it concludes.
	for {
		select {
		case e := <-sub.C():
			if e.Kind != events.KindHeartbeat || e.MonitorID != m.ID {
				continue
			}
			beats, err := db.ListHeartbeats(context.Background(), m.ID, 5)
			if err != nil {
				t.Fatalf("ListHeartbeats: %v", err)
			}
			if len(beats) == 0 {
				t.Fatal("the watchdog published a heartbeat it never stored")
			}
			return
		case <-time.After(10 * time.Second):
			t.Fatal("Run never swept for overdue push monitors")
		}
	}
}

// TestOverdueFailureKind keeps the two push failure modes distinguishable.
// "your job did not run" and "your job ran and said it failed" send a person
// to different places.
func TestOverdueFailureKind(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := newPushRunner(t, db, nil)

	overdue := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, overdue.ID, 2*time.Hour)
	r.sweepOverduePushMonitors(ctx)

	inc, err := db.OpenIncidentFor(ctx, overdue.ID)
	if err != nil {
		t.Fatalf("incident: %v", err)
	}
	if inc.Cause != string(checker.FailPushOverdue) {
		t.Errorf("cause = %q, want %q", inc.Cause, checker.FailPushOverdue)
	}

	reported := mustPushMonitor(t, db, 60, 0)
	if err := r.RecordPush(ctx, reported, PushReport{OK: false}); err != nil {
		t.Fatalf("RecordPush: %v", err)
	}
	inc, err = db.OpenIncidentFor(ctx, reported.ID)
	if err != nil {
		t.Fatalf("incident: %v", err)
	}
	if inc.Cause != string(checker.FailPushReported) {
		t.Errorf("cause = %q, want %q", inc.Cause, checker.FailPushReported)
	}
}
