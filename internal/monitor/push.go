package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/store"
)

// defaultPushSweep is how often the watchdog re-reads the push monitors and
// asks which of them are overdue.
//
// Fifteen seconds, and deliberately unrelated to any monitor's expected
// interval. The sweep costs one indexed query no matter how many push monitors
// exist, and it bounds how late an overdue report can be: a job expected every
// five minutes whose absence was only noticed once an hour would make the
// grace period a lie.
const defaultPushSweep = 15 * time.Second

// pushFailureThreshold is the number of failures that confirm a push
// incident.
//
// One, regardless of the monitor's `retries`. That column means "how many
// consecutive probe failures before I believe it", and it exists because a
// single outbound probe is a noisy sample — a dropped packet, a redeploy, a GC
// pause. Neither push failure is a sample of anything: silence past the
// deadline has already survived the grace period the user chose for exactly
// this purpose, and an explicit `status=down` is the job itself reporting that
// it failed. Applying retries on top would mean a nightly backup had to fail
// three nights running before anyone heard about it, which is the opposite of
// why someone monitors a backup.
const pushFailureThreshold = 1

// pushRecoveryThreshold is 1 for the same reason. A push is not a sample
// either: a job reporting success is the job saying it worked, and a nightly
// job would otherwise need a second good night before its incident closed.
const pushRecoveryThreshold = 1

// PushReport is one incoming ping on a push URL.
type PushReport struct {
	// OK is what the job claims about itself. False when the script
	// reported its own failure with status=down.
	OK bool

	// Message is the optional note from the script, shown as the failure
	// text. Ignored when OK.
	Message string

	// At is when the ping arrived. Zero means now.
	At time.Time
}

// ErrNotPushMonitor is returned when a report is aimed at a monitor that is
// not a push monitor.
var ErrNotPushMonitor = store.ErrNotPushMonitor

// RecordPush turns an incoming ping into a heartbeat and runs it through the
// ordinary state machine.
//
// The whole point of routing it through the ordinary recording path rather
// than writing a heartbeat directly is that a push monitor then behaves like
// every other monitor from the state engine down: incidents open, confirm and
// resolve by the same rules, the live stream sees the same events, and uptime
// is computed from the same table. The only thing that differs is who started
// the check.
//
// Unlike a scheduled check, a persistence failure is returned to the caller.
// The job on the other end is waiting for an answer, and a 200 that says
// "recorded" for a heartbeat the database refused would make the dead man's
// switch lie in the one direction that matters: silence read as health.
func (r *Runner) RecordPush(ctx context.Context, m store.Monitor, rep PushReport) error {
	if m.Type != store.TypePush {
		return fmt.Errorf("%w: monitor %d is type %q", ErrNotPushMonitor, m.ID, m.Type)
	}

	at := rep.At
	if at.IsZero() {
		at = time.Now()
	}

	res := checker.Result{OK: rep.OK, CheckedAt: at}
	if !rep.OK {
		res.Kind = checker.FailPushReported
		res.Error = rep.Message
		if res.Error == "" {
			res.Error = "the job reported that it failed"
		}
	}

	return r.recordOutcome(scheduler.Outcome{Monitor: pushCheckerMonitor(m), Result: res})
}

// pushCheckerMonitor is the checker-shaped view of a push monitor.
//
// Retries is overridden rather than passed through; see pushFailureThreshold
// for why. Everything else that matters to a probe is meaningless here, so it
// is left at its zero value rather than copied along to suggest otherwise.
func pushCheckerMonitor(m store.Monitor) checker.Monitor {
	cm := toCheckerMonitor(m)
	cm.Retries = pushFailureThreshold
	cm.RecoveryThreshold = pushRecoveryThreshold
	return cm
}

// runPushWatchdog declares push monitors down once they stop reporting in.
//
// This is the half of the feature that cannot be a checker. Every other type
// fails by being dialled and not answering; a push monitor fails by nothing
// happening at all, and nothing happening does not arrive on any queue. So the
// watchdog is the one thing in the system that has to go looking for absence.
func (r *Runner) runPushWatchdog(ctx context.Context) {
	interval := r.pushSweep
	if interval <= 0 {
		interval = defaultPushSweep
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// The process is listening from here on. Only the first start counts:
	// the moment SubGlance began hearing reports is a fact about the
	// process, and a second Run on the same runner does not undo it.
	r.pushListeningSince.CompareAndSwap(0, r.now().UnixNano())

	// Sweep once immediately, so the cadence starts with the process rather
	// than one tick later. It no longer reports deadlines that passed while
	// the process was down, which it once did on the argument that the
	// outage did not pause because the process did. That argument missed
	// that the job's reports could not arrive either: a job that ran on time
	// was refused a connection, and alarming over that silence is a false
	// alarm. Such a window starts again now; see store.Monitor.PushWindow.
	r.sweepOverduePushMonitors(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweepOverduePushMonitors(ctx)
		}
	}
}

// sweepOverduePushMonitors records one failure for every push monitor whose
// window has closed without a report.
func (r *Runner) sweepOverduePushMonitors(ctx context.Context) {
	monitors, err := r.db.ListEnabledPushMonitors(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		r.log.Error("push watchdog could not list monitors", "error", err)
		return
	}

	now := time.Now()
	for _, m := range monitors {
		if m.PushIntervalS <= 0 {
			// Unreachable through the API, which requires the interval.
			// A row that got here another way is a configuration fault,
			// not an outage, so it is skipped rather than declared down.
			r.log.Warn("push monitor has no expected interval, skipping",
				"monitor_id", m.ID, "monitor", m.Name)
			continue
		}

		last, err := r.db.LastActivity(ctx, m)
		if err != nil {
			r.log.Error("push watchdog could not read last activity",
				"monitor_id", m.ID, "error", err)
			continue
		}

		window := m.PushWindow(last, r.PushListeningSince())
		deadline := window.Deadline
		if !now.After(deadline) {
			continue
		}

		// The synthetic failure is timestamped at the deadline, not at the
		// sweep. That is the moment the job was definitively late, and
		// dating it "now" would make the gap in the heartbeat timeline
		// depend on the sweep interval rather than on what happened.
		//
		// It also spaces repeats correctly without any extra bookkeeping:
		// this heartbeat becomes the monitor's last activity, so the next
		// deadline is one full window later. A job that stays silent for a
		// week produces one beat per window, not one per sweep.
		r.log.Warn("push monitor is overdue",
			"monitor", m.Name, "monitor_id", m.ID,
			"expected_every", time.Duration(m.PushIntervalS)*time.Second,
			"grace", time.Duration(m.PushGraceS)*time.Second,
			"last_report", last,
			"window_start", window.Start)

		r.record(scheduler.Outcome{
			Monitor: pushCheckerMonitor(m),
			Result: checker.Result{
				OK:        false,
				Kind:      checker.FailPushOverdue,
				Error:     overdueMessage(m, window, now),
				CheckedAt: deadline,
			},
		})
	}
}

// overdueMessage says how late the job is in words a human can act on.
//
// "no report for 3h2m0s, expected every 1h0m0s" beats "monitor is down",
// because the two sentences prompt different actions: one sends someone to
// look at the job, the other sends them to look at the network.
//
// When the window did not start at the last report, the sentence says where it
// did start. "No report for 9 days" about a monitor that was paused for eight
// of them would be true of the database and false of the job.
func overdueMessage(m store.Monitor, w store.PushWindow, now time.Time) string {
	since := now.Sub(w.Start).Round(time.Second)
	every := (time.Duration(m.PushIntervalS) * time.Second).Round(time.Second)
	switch w.Reason {
	case store.PushWindowResumed:
		return fmt.Sprintf("no report in the %s since the monitor was resumed, expected every %s", since, every)
	case store.PushWindowRestarted:
		return fmt.Sprintf("no report in the %s since SubGlance started, expected every %s", since, every)
	default:
		return fmt.Sprintf("no report for %s, expected every %s", since, every)
	}
}

// PushListeningSince is when this process started listening for push reports,
// or the zero time before Run has started the watchdog. The API reads it to
// say why a push monitor is waiting.
func (r *Runner) PushListeningSince() time.Time {
	ns := r.pushListeningSince.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
