package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/store"
)

// The tests in this file cover SUB-209: a push monitor is not declared down
// over silence SubGlance could not hear, whether because the monitor was
// paused or because SubGlance itself was not running. Each scenario is a
// daily job: expected every 24 hours with an hour of grace.
const (
	dailyEvery = 24 * 3600
	dailyGrace = 3600
	dailyWin   = 25 * time.Hour
)

// reportedAt records one successful report from the job at the given moment.
func reportedAt(t *testing.T, r *Runner, m store.Monitor, at time.Time) {
	t.Helper()
	if err := r.RecordPush(context.Background(), m, PushReport{OK: true, At: at}); err != nil {
		t.Fatalf("RecordPush: %v", err)
	}
}

// setResumedAt moves the stored resume moment, so a test can stand one full
// window after a resume without sleeping through it.
func setResumedAt(t *testing.T, db *store.DB, id int64, at time.Time) {
	t.Helper()
	if _, err := db.Writer.ExecContext(context.Background(),
		"UPDATE monitors SET resumed_at = ? WHERE id = ?", at.Unix(), id); err != nil {
		t.Fatalf("set resumed_at: %v", err)
	}
}

// listeningSince pretends the process started listening at the given moment.
func listeningSince(r *Runner, at time.Time) {
	r.pushListeningSince.Store(at.UnixNano())
}

func overdueBeats(t *testing.T, db *store.DB, id int64) []store.Heartbeat {
	t.Helper()
	beats, err := db.ListHeartbeats(context.Background(), id, 20)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	var out []store.Heartbeat
	for _, b := range beats {
		if !b.OK {
			out = append(out, b)
		}
	}
	return out
}

// TestResumeGivesTheJobOneFullWindow is the scenario that was reproduced
// against develop before this change: a daily job that last reported 30 hours
// ago is paused and resumed, and the first sweep raised a confirmed incident
// and an alert over reports that SubGlance had accepted and thrown away.
func TestResumeGivesTheJobOneFullWindow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rec := &alertRecorder{}
	r := newPushRunner(t, db, rec)
	m := mustPushMonitor(t, db, dailyEvery, dailyGrace)
	backdate(t, db, m.ID, 40*time.Hour)
	reportedAt(t, r, m, time.Now().Add(-30*time.Hour))

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	r.sweepOverduePushMonitors(ctx)

	if got := overdueBeats(t, db, m.ID); len(got) != 0 {
		t.Fatalf("a freshly resumed monitor was declared overdue: %+v", got)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("resuming opened an incident")
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("resuming alerted: %v", got)
	}

	// One full window after the resume, silence is evidence again.
	setResumedAt(t, db, m.ID, time.Now().Add(-dailyWin-time.Minute))
	r.sweepOverduePushMonitors(ctx)

	got := overdueBeats(t, db, m.ID)
	if len(got) != 1 {
		t.Fatalf("got %d overdue beats one window after resuming, want 1", len(got))
	}
	if !strings.Contains(got[0].Error, "since the monitor was resumed") {
		t.Errorf("overdue message %q does not say the window started at the resume", got[0].Error)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Errorf("a job silent for a full window after resuming was not reported: %v", err)
	}
}

// TestRestartGivesAnExpiredWindowOneMore: the deadline fell while SubGlance was
// not running, so the job may have reported and been refused a connection.
// Nothing is raised until one window after the start.
func TestRestartGivesAnExpiredWindowOneMore(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rec := &alertRecorder{}
	r := newPushRunner(t, db, rec)
	m := mustPushMonitor(t, db, dailyEvery, dailyGrace)
	backdate(t, db, m.ID, 70*time.Hour)
	// Last report 60 hours ago, so the deadline fell 35 hours ago: before
	// either start below.
	reportedAt(t, r, m, time.Now().Add(-60*time.Hour))

	listeningSince(r, time.Now().Add(-time.Minute))
	r.sweepOverduePushMonitors(ctx)
	if got := overdueBeats(t, db, m.ID); len(got) != 0 {
		t.Fatalf("a deadline that passed while SubGlance was down raised %+v", got)
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("a restart alerted: %v", got)
	}

	// The same instance, one full window after it started.
	listeningSince(r, time.Now().Add(-dailyWin-time.Minute))
	r.sweepOverduePushMonitors(ctx)
	got := overdueBeats(t, db, m.ID)
	if len(got) != 1 {
		t.Fatalf("got %d overdue beats one window after the start, want 1", len(got))
	}
	if !strings.Contains(got[0].Error, "since SubGlance started") {
		t.Errorf("overdue message %q does not say the window started at the start", got[0].Error)
	}
}

// TestShortRestartDoesNotMoveALaterDeadline closes the gap reported against
// Uptime Kuma (#3599), which restarts every window at startup: an instance
// that restarts daily then never reports a daily job that stopped. Here the
// restart lands five seconds before the deadline, and the deadline stands.
func TestShortRestartDoesNotMoveALaterDeadline(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rec := &alertRecorder{}
	r := newPushRunner(t, db, rec)
	m := mustPushMonitor(t, db, dailyEvery, dailyGrace)
	backdate(t, db, m.ID, 40*time.Hour)

	last := time.Now().Add(-dailyWin - time.Minute)
	reportedAt(t, r, m, last)
	listeningSince(r, last.Add(dailyWin-5*time.Second))

	r.sweepOverduePushMonitors(ctx)
	got := overdueBeats(t, db, m.ID)
	if len(got) != 1 {
		t.Fatalf("got %d overdue beats, want 1: a restart before the deadline must not move it", len(got))
	}
	if !strings.HasPrefix(got[0].Error, "no report for ") {
		t.Errorf("overdue message %q should measure from the last report", got[0].Error)
	}
	if len(rec.events()) == 0 {
		t.Error("nobody was told about a daily job that stopped")
	}
}

// TestRunStartsListeningAndSkipsWindowsThatClosedWhileDown drives the real
// watchdog. Two push monitors: one whose deadline passed long before the
// start, one whose deadline falls a second after it. The sweep visits
// monitors in id order, so by the time the second one's overdue beat arrives
// the first has been looked at in every sweep so far, and must have none.
func TestRunStartsListeningAndSkipsWindowsThatClosedWhileDown(t *testing.T) {
	db := testDB(t)
	bus := events.NewBus(8)
	r := newPushRunnerWithBus(t, db, bus)

	expired := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, expired.ID, 2*time.Hour)
	due := mustPushMonitor(t, db, 60, 0)
	backdate(t, db, due.ID, 2*time.Hour)
	reportedAt(t, r, due, time.Now().Add(-59*time.Second))

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

	for {
		select {
		case e := <-sub.C():
			if e.Kind != events.KindHeartbeat || e.MonitorID != due.ID {
				continue
			}
			if r.PushListeningSince().IsZero() {
				t.Fatal("Run swept without recording when it started listening")
			}
			if got := overdueBeats(t, db, expired.ID); len(got) != 0 {
				t.Fatalf("a window that closed before the start was reported: %+v", got)
			}
			if got := overdueBeats(t, db, due.ID); len(got) != 1 {
				t.Fatalf("got %d overdue beats for the monitor whose deadline fell after the start, want 1", len(got))
			}
			return
		case <-time.After(10 * time.Second):
			t.Fatal("the deadline that fell after the start was never reported")
		}
	}
}

// TestMarkPushListeningKeepsTheFirstMoment: main marks the runner before the
// API serves, and Run's watchdog marks it again. The second mark must not move
// the start, or a window that restarted at the first would restart again.
func TestMarkPushListeningKeepsTheFirstMoment(t *testing.T) {
	db := testDB(t)
	r := newPushRunner(t, db, &alertRecorder{})
	if !r.PushListeningSince().IsZero() {
		t.Fatal("a new runner claims to be listening already")
	}
	r.MarkPushListening()
	first := r.PushListeningSince()
	if first.IsZero() {
		t.Fatal("MarkPushListening left the start unset")
	}
	time.Sleep(2 * time.Millisecond)
	r.MarkPushListening()
	if got := r.PushListeningSince(); !got.Equal(first) {
		t.Fatalf("a second mark moved the start from %v to %v", first, got)
	}
}
