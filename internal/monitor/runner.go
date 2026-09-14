// Package monitor wires the storage layer to the scheduler.
//
// It is the seam between "what should be checked" (the database), "run it"
// (the scheduler) and "does this mean anything" (the state engine), so none of
// the three has to know about the others.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// Runner loads monitors from the database, runs them, and records the results.
type Runner struct {
	db     *store.DB
	log    *slog.Logger
	sch    *scheduler.Scheduler
	engine *state.Engine

	// notify receives transitions that should reach a human. It is a field
	// rather than a hard dependency on a notifier package so SUB-15 can plug
	// in without touching this file.
	notify func(Alert)

	// checks counts completed checks. It is the liveness evidence the
	// watchdog needs: a process whose scheduler has wedged keeps running
	// and keeps serving HTTP, but this number stops moving.
	checks atomic.Uint64

	// bus fans check results out to live listeners (the SSE endpoint). Nil
	// means nobody is watching, which is the normal case in tests.
	bus *events.Bus

	// reminderInterval is how often reminders are swept for. See
	// Options.ReminderInterval.
	reminderInterval time.Duration

	// now is the clock, swappable in tests. Reminder logic is time-driven
	// rather than check-driven, so a test that had to wait real minutes for
	// an escalating schedule would not be written at all.
	now func() time.Time

	// pushSweep is how often the push watchdog looks for overdue monitors.
	// Zero means defaultPushSweep.
	pushSweep time.Duration
}

// Alert is a state change worth telling someone about.
type Alert struct {
	Monitor  store.Monitor
	Incident store.Incident
	Event    state.Event
	At       time.Time
}

// Options configures New.
type Options struct {
	DB  *store.DB
	Log *slog.Logger

	// AllowPrivateTargets disables the SSRF guard. See config.Config.
	AllowPrivateTargets bool

	// Workers caps concurrent checks. Zero lets the scheduler decide.
	Workers int

	// ReloadInterval controls how often the monitor set is re-read from the
	// database. Zero means 30 seconds.
	ReloadInterval time.Duration

	// FlapWindow and FlapThreshold tune oscillation suppression. Zero values
	// use the state engine's defaults.
	FlapWindow    time.Duration
	FlapThreshold int

	// Notify receives alerts. Optional; nil means log only.
	Notify func(Alert)

	// Bus receives every heartbeat and status change for live streaming.
	// Optional; nil means nothing is published.
	Bus *events.Bus

	// ReminderInterval is how often open incidents are checked for a due
	// reminder. Zero means 30 seconds. It is not the reminder delay itself —
	// that is per monitor — only the resolution at which one can fire.
	ReminderInterval time.Duration

	// PushSweep is how often push monitors are checked for being overdue.
	// Zero means defaultPushSweep. Tests set it small; nothing else needs
	// to set it at all.
	PushSweep time.Duration
}

// New builds a Runner with the standard set of checkers.
func New(opts Options) *Runner {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	guard := checker.NewGuard(opts.AllowPrivateTargets)
	httpChecker := checker.NewHTTPChecker(checker.HTTPOptions{Guard: guard})

	reminderInterval := opts.ReminderInterval
	if reminderInterval <= 0 {
		reminderInterval = 30 * time.Second
	}

	r := &Runner{
		db:               opts.DB,
		log:              log,
		notify:           opts.Notify,
		bus:              opts.Bus,
		reminderInterval: reminderInterval,
		now:              time.Now,
		pushSweep:        opts.PushSweep,
		engine: state.New(state.Options{
			FlapWindow:    opts.FlapWindow,
			FlapThreshold: opts.FlapThreshold,
		}),
	}

	r.sch = scheduler.New(scheduler.Options{
		Registry: scheduler.RegistryFunc(r.jobs),
		Checkers: map[checker.Type]checker.Checker{
			checker.TypeHTTP: httpChecker,
			checker.TypeTCP:  checker.NewTCPChecker(guard),
			checker.TypeSSL:  checker.NewSSLChecker(guard),
			checker.TypePing: checker.NewPingChecker(guard),
		},
		OnResult:       r.record,
		Workers:        opts.Workers,
		ReloadInterval: opts.ReloadInterval,
		Log:            log,
	})
	return r
}

// Run drives the scheduler until ctx is cancelled.
//
// It first restores state from the database, so a restart does not re-announce
// outages that were already reported.
//
// The push watchdog runs alongside the scheduler rather than inside it: the
// scheduler's whole model is a queue of things to go and dial, and absence is
// not something that can be dialled. It is started here and stopped by the
// same context, so there is still exactly one lifetime to manage.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.restore(ctx); err != nil {
		// A failed restore is not fatal — monitoring with a clean slate beats
		// not monitoring — but it does mean possible duplicate alerts, so it
		// is logged at error level.
		r.log.Error("failed to restore incident state; duplicate alerts are possible", "error", err)
	}
	// The reminder sweep is time-driven while everything else here is
	// check-driven, so it gets its own goroutine rather than riding along on
	// check results: a monitor on a one-hour interval would otherwise have
	// its 15-minute reminder delayed to the next check.
	reminderDone := make(chan struct{})
	go func() {
		defer close(reminderDone)
		r.remindLoop(ctx)
	}()

	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		r.runPushWatchdog(ctx)
	}()

	err := r.sch.Run(ctx)

	// Wait for both background loops before returning, for the same reason
	// the scheduler waits for in-flight checks: a sweep half-way through
	// writing a heartbeat, or a reminder half-way through a synchronous
	// notifier, must not be cut off by the process exiting underneath it.
	<-watchdogDone
	<-reminderDone
	return err
}

// remindLoop repeats alerts for confirmed incidents nobody has acknowledged,
// until ctx is cancelled.
func (r *Runner) remindLoop(ctx context.Context) {
	t := time.NewTicker(r.reminderInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sendDueReminders(ctx)
		}
	}
}

// sendDueReminders emits one reminder for every incident whose schedule has
// come due.
//
// Ordering note: the stamp is written before the alert is handed over. If the
// notifier fails, the reminder is lost rather than retried — which is the
// right way round, because the alternative is a crash loop that re-sends the
// same alert every tick. Delivery retries belong in the notifier.
func (r *Runner) sendDueReminders(ctx context.Context) {
	candidates, err := r.db.RemindableIncidents(ctx)
	if err != nil {
		r.log.Error("failed to list incidents needing a reminder", "error", err)
		return
	}

	now := r.now()
	for _, c := range candidates {
		inc := c.Incident
		if !state.ReminderDue(now, inc.ConfirmedAt, inc.RemindedAt, inc.ReminderCount, c.RepeatAfter) {
			continue
		}

		// Flapping monitors are excluded here rather than in SQL: whether a
		// monitor is oscillating is engine state, not a column. Repeating an
		// alert for a monitor that is already being suppressed would reinstate
		// exactly the noise the suppression removed.
		if r.engine.Flapping(inc.MonitorID) {
			continue
		}

		// The monitor is loaded before the schedule is advanced. The other
		// way round, a failed read still consumed the reminder: the count
		// was already incremented, so the next attempt waited the longer
		// gap for an alert nobody ever received.
		m, err := r.db.GetMonitor(ctx, inc.MonitorID)
		if err != nil {
			r.log.Error("failed to load monitor for reminder",
				"monitor_id", inc.MonitorID, "error", err)
			continue
		}

		if err := r.db.RecordReminder(ctx, inc.ID, now); err != nil {
			if !errors.Is(err, store.ErrNoOpenIncident) {
				r.log.Error("failed to stamp reminder",
					"incident_id", inc.ID, "error", err)
			}
			// Acknowledged or resolved between the query and the write. That
			// is the acknowledge button doing its job, not an error.
			continue
		}

		inc.RemindedAt = now
		inc.ReminderCount++

		r.log.Warn("repeating unacknowledged alert",
			"monitor", m.Name,
			"incident_id", inc.ID,
			"down_for", now.Sub(inc.StartedAt).Round(time.Second),
			"reminder", inc.ReminderCount)

		r.publish(events.Event{
			Kind:      events.KindIncident,
			MonitorID: inc.MonitorID,
			At:        now,
			Payload: statusPayload{
				Event: string(state.EventIncidentReminder),
				Cause: inc.Cause,
				Error: inc.LastError,
			},
		})

		// A nil notifier means log-only operation, not "skip reminders":
		// the warning above, the stamp and the event bus are the whole
		// point of a deployment that watches the log or the dashboard.
		if r.notify != nil {
			r.notify(Alert{
				Monitor:  m,
				Incident: inc,
				Event:    state.EventIncidentReminder,
				At:       now,
			})
		}
	}
}

// Size reports how many monitors are currently scheduled.
func (r *Runner) Size() int { return r.sch.Size() }

// ChecksCompleted reports how many checks have finished since start.
//
// Only its movement is meaningful; it is not persisted and resets on restart.
func (r *Runner) ChecksCompleted() uint64 { return r.checks.Load() }

// Engine exposes the state engine so the API can report current status.
func (r *Runner) Engine() *state.Engine { return r.engine }

// restore seeds the state engine from open incidents in the database.
func (r *Runner) restore(ctx context.Context) error {
	incidents, err := r.db.ListOpenIncidents(ctx)
	if err != nil {
		return err
	}

	for _, inc := range incidents {
		status := state.StatusPending
		if inc.Confirmed() {
			status = state.StatusDown
		}
		r.engine.Restore(inc.MonitorID, status, true, inc.Confirmed())
	}

	if len(incidents) > 0 {
		r.log.Info("restored open incidents", "count", len(incidents))
	}
	return nil
}

// jobs converts enabled monitors into scheduler jobs.
//
// It also reconciles against the live monitor set: any monitor the engine
// still tracks that is no longer enabled gets forgotten, and any incident it
// left open gets closed.
//
// Closing matters as much as forgetting. Once a monitor is paused or deleted
// it stops being checked, so nothing will ever arrive to resolve its incident
// — the row would stay open forever, the dashboard would show a paused
// monitor as permanently down, a restart would re-seed the phantom, and the
// partial unique index would block every future incident for that monitor.
func (r *Runner) jobs(ctx context.Context) ([]scheduler.Job, error) {
	monitors, err := r.db.ListEnabledMonitors(ctx)
	if err != nil {
		return nil, err
	}

	// Monitors with no heartbeat yet get a fast first check rather than a full
	// interval's wait. A failure here is not fatal: falling back to the normal
	// schedule is slower, not wrong.
	checked, err := r.db.CheckedMonitorIDs(ctx)
	if err != nil {
		r.log.Warn("could not determine which monitors are new; "+
			"first checks will follow the normal interval", "error", err)
		checked = nil
	}

	live := make(map[int64]struct{}, len(monitors))
	jobs := make([]scheduler.Job, 0, len(monitors))
	for _, m := range monitors {
		// A push monitor is live — its incidents are real and must not be
		// reconciled away — but it is never scheduled. Nothing dials it;
		// the watchdog decides when its silence has gone on too long.
		// Queueing it would mean dispatching a check with no checker
		// behind it, which the scheduler would correctly report as an
		// internal error on every interval.
		live[m.ID] = struct{}{}
		if m.Type == store.TypePush {
			continue
		}
		_, seen := checked[m.ID]
		jobs = append(jobs, scheduler.Job{
			Monitor:      toCheckerMonitor(m),
			Interval:     time.Duration(m.IntervalS) * time.Second,
			NeverChecked: checked != nil && !seen,
		})
	}

	r.engine.Retain(live)
	r.closeOrphanedIncidents(ctx, live)

	return jobs, nil
}

// closeOrphanedIncidents resolves incidents belonging to monitors that are no
// longer being checked.
//
// The incident is resolved rather than deleted: it really did happen, and the
// history should say so. What is not true is that it is still ongoing.
func (r *Runner) closeOrphanedIncidents(ctx context.Context, live map[int64]struct{}) {
	open, err := r.db.ListOpenIncidents(ctx)
	if err != nil {
		r.log.Error("failed to list open incidents while reconciling", "error", err)
		return
	}

	now := time.Now()
	for _, inc := range open {
		if _, still := live[inc.MonitorID]; still {
			continue
		}
		if _, err := r.db.ResolveIncident(ctx, inc.MonitorID, now); err != nil {
			r.log.Error("failed to close incident for a monitor that is no longer checked",
				"monitor_id", inc.MonitorID, "incident_id", inc.ID, "error", err)
			continue
		}
		r.log.Info("closed incident for a paused or deleted monitor",
			"monitor_id", inc.MonitorID, "incident_id", inc.ID)
	}
}

// toCheckerMonitor maps the stored shape onto what a checker needs.
func toCheckerMonitor(m store.Monitor) checker.Monitor {
	return checker.Monitor{
		ID:              m.ID,
		Name:            m.Name,
		Type:            checker.Type(m.Type),
		Target:          m.Target,
		Timeout:         time.Duration(m.TimeoutS) * time.Second,
		Method:          m.Method,
		ExpectedStatus:  m.ExpectedStatus,
		Keyword:         m.Keyword,
		KeywordMode:     checker.KeywordMode(m.KeywordMode),
		FollowRedirects: m.FollowRedirects,
		Headers:         m.Headers,
		Body:            m.Body,
		SSLWarnDays:     m.SSLWarnDays,
		Retries:         m.Retries,
		CaptureResponse: m.CaptureResponse,
	}
}

// maxSnapshotsPerIncident bounds how many failure responses one outage stores.
//
// Without a bound, a monitor checked every 60 seconds that stays broken for a
// week would write ten thousand snapshots of the same 2 KiB error page: 20 MB
// to say one thing. The first few are where the information is — the first
// failure, and the next couple in case the symptom changes as the outage
// develops. After that the streak is repeating itself.
const maxSnapshotsPerIncident = 3

// record persists one check result and advances the state machine.
//
// A failure to write must never stop the scheduler: losing one heartbeat is
// survivable, a monitoring system that stops monitoring is not.
// record stores one outcome for the scheduler, where a failed write is logged
// and the pipeline carries on: a check that could not be stored still happened,
// and an outage still has to be reported.
func (r *Runner) record(o scheduler.Outcome) {
	_ = r.recordOutcome(o)
}

// recordOutcome stores one outcome and reports whether anything durable failed.
//
// Callers that answer a client need that answer. RecordPush sits behind the
// public push endpoint and must not tell a job "recorded" when the heartbeat
// or the incident never reached the database, because the whole promise of a
// push monitor is that the report it acknowledges is the one it will remember.
func (r *Runner) recordOutcome(o scheduler.Outcome) error {
	// A check aborted by shutdown says nothing about the target. Every
	// checker turns a cancelled context into a failed Result, and the
	// scheduler waits for those in-flight results before stopping — so
	// without this guard, every restart would inject a burst of false
	// failures into the state engine, open incidents for healthy monitors,
	// and page someone about an outage that never happened.
	//
	// The heartbeat is dropped too: recording it would put a phantom red bar
	// in the timeline and dent the uptime figure at every deploy.
	if o.Aborted {
		r.log.Debug("discarding check cancelled by shutdown",
			"monitor", o.Monitor.Name)
		return nil
	}

	// Counted before the write: the check itself is what proves the
	// pipeline is alive, and a database that has gone read-only must not
	// make a working scheduler look dead to the watchdog.
	r.checks.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hb := store.Heartbeat{
		MonitorID:  o.Monitor.ID,
		TS:         o.Result.CheckedAt,
		OK:         o.Result.OK,
		LatencyMS:  int(o.Result.Latency.Milliseconds()),
		StatusCode: o.Result.StatusCode,
		Error:      o.Result.Error,
	}
	if hb.TS.IsZero() {
		hb.TS = time.Now()
	}

	// The state engine runs before the write, not after, because the failure
	// streak it reports decides whether this result's response snapshot is
	// worth storing. Observe only touches in-memory state, so moving it ahead
	// of the heartbeat write changes nothing about what either one decides.
	tr := r.engine.Observe(state.Observation{
		MonitorID:        o.Monitor.ID,
		OK:               o.Result.OK,
		At:               hb.TS,
		Kind:             string(o.Result.Kind),
		Error:            o.Result.Error,
		FailureThreshold: o.Monitor.Retries,
	})

	if snap := snapshotToStore(o.Result, tr.ConsecutiveFails); snap != nil {
		hb.Response = snap
	}

	// The error is kept rather than only logged, but the state machine has
	// already run: the check did happen, and an outage should be reported
	// even if the database is having a bad moment.
	var hbErr error
	if err := r.db.RecordHeartbeat(ctx, hb); err != nil {
		r.log.Error("failed to record heartbeat",
			"monitor_id", o.Monitor.ID, "monitor", o.Monitor.Name, "error", err)
		hbErr = fmt.Errorf("record heartbeat: %w", err)
	}

	// Publish before applying the transition so the dashboard paints the new
	// bar immediately, rather than waiting on incident bookkeeping.
	r.publish(events.Event{
		Kind:      events.KindHeartbeat,
		MonitorID: o.Monitor.ID,
		At:        hb.TS,
		Payload: heartbeatPayload{
			OK:         hb.OK,
			LatencyMS:  hb.LatencyMS,
			StatusCode: hb.StatusCode,
			Error:      hb.Error,
		},
	})

	return errors.Join(hbErr, r.applyTransition(ctx, o, tr))
}

// snapshotToStore decides whether this result's captured response is worth a
// row, and converts it to the storage shape.
//
// The rule is the first maxSnapshotsPerIncident failures of a streak. A
// monitor that has been returning the same 503 for six hours has already said
// everything it has to say, and every repeat after that is storage spent on a
// copy of something already on disk.
func snapshotToStore(res checker.Result, consecutiveFails int) *store.ResponseSnapshot {
	if res.Response == nil || res.OK {
		return nil
	}
	if consecutiveFails > maxSnapshotsPerIncident {
		return nil
	}
	return &store.ResponseSnapshot{
		Body:      res.Response.Body,
		Headers:   res.Response.Headers,
		Truncated: res.Response.Truncated,
	}
}

// heartbeatPayload is the wire shape of a single check result.
//
// It is deliberately not store.Heartbeat: the stored row carries an ID and a
// monitor ID that the envelope already provides, and pinning the wire format
// here means a schema change cannot silently alter the public API.
type heartbeatPayload struct {
	OK         bool   `json:"ok"`
	LatencyMS  int    `json:"latency_ms"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// statusPayload describes a monitor changing state.
type statusPayload struct {
	Event string `json:"event"`
	Cause string `json:"cause,omitempty"`
	Error string `json:"error,omitempty"`
	// Suppressed marks a transition the flapping filter decided not to
	// alert on. The dashboard still shows it; a human just is not paged.
	Suppressed bool `json:"suppressed,omitempty"`
}

// publish sends an event if anyone is listening. Nil bus is the normal case in
// tests and must stay a no-op rather than a panic.
func (r *Runner) publish(e events.Event) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(e)
}

// applyTransition writes the incident side of a transition and emits an alert
// when one is warranted.
//
// It returns the first persistence failure it hit. Alert delivery problems are
// logged but not returned: a caller deciding whether to acknowledge a report
// cares about what was stored, not about who was told.
func (r *Runner) applyTransition(ctx context.Context, o scheduler.Outcome, tr state.Transition) error {
	var (
		inc        store.Incident
		err        error
		persistErr error
	)

	switch tr.Event {
	case state.EventNone:
		// Nothing structural changed. Keep the open incident's error text
		// current so the UI shows the latest symptom. Do not return: a
		// flapping status can change on a check that produced no event, and
		// swallowing that would leave the log claiming a monitor is still
		// flapping long after it settled.
		if !o.Result.OK {
			if err := r.db.UpdateIncidentError(ctx, o.Monitor.ID, tr.Cause, tr.Error); err != nil {
				r.log.Error("failed to update incident error", "monitor_id", o.Monitor.ID, "error", err)
				persistErr = fmt.Errorf("update incident error: %w", err)
			}
		}

	case state.EventIncidentOpened:
		inc, err = r.db.OpenIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error)
		if err != nil {
			r.log.Error("failed to open incident", "monitor_id", o.Monitor.ID, "error", err)
			return fmt.Errorf("open incident: %w", err)
		}
		r.log.Info("incident opened, awaiting confirmation",
			"monitor", o.Monitor.Name,
			"consecutive_fails", tr.ConsecutiveFails,
			"threshold", o.Monitor.Retries,
			"cause", tr.Cause)

	case state.EventIncidentConfirmed:
		// With a threshold of 1 there was no pending phase, so the incident
		// may not exist yet.
		if err := r.db.ConfirmIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error); errors.Is(err, store.ErrNoOpenIncident) {
			if _, err := r.db.OpenIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error); err != nil {
				r.log.Error("failed to open incident on confirm", "monitor_id", o.Monitor.ID, "error", err)
				return fmt.Errorf("open incident on confirm: %w", err)
			}
			if err := r.db.ConfirmIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error); err != nil {
				r.log.Error("failed to confirm incident", "monitor_id", o.Monitor.ID, "error", err)
				return fmt.Errorf("confirm incident: %w", err)
			}
		} else if err != nil {
			r.log.Error("failed to confirm incident", "monitor_id", o.Monitor.ID, "error", err)
			return fmt.Errorf("confirm incident: %w", err)
		}

		// Read the incident back rather than reusing whatever the branch above
		// happened to produce. ConfirmIncident is an UPDATE and returns no
		// row, so without this the alert would carry a zero-valued incident —
		// no start time, no cause — which is exactly the detail the person
		// being paged needs.
		if inc, err = r.db.OpenIncidentFor(ctx, o.Monitor.ID); err != nil {
			r.log.Error("failed to reload confirmed incident", "monitor_id", o.Monitor.ID, "error", err)
			// Carry on: an alert with thin detail beats no alert at all.
		}

		r.log.Warn("incident confirmed",
			"monitor", o.Monitor.Name,
			"cause", tr.Cause,
			"error", tr.Error,
			"suppressed", tr.Suppressed)

	case state.EventIncidentResolved:
		inc, err = r.db.ResolveIncident(ctx, o.Monitor.ID, tr.At)
		if errors.Is(err, store.ErrNoOpenIncident) {
			return nil
		}
		if err != nil {
			r.log.Error("failed to resolve incident", "monitor_id", o.Monitor.ID, "error", err)
			return fmt.Errorf("resolve incident: %w", err)
		}
		r.log.Info("incident resolved",
			"monitor", o.Monitor.Name,
			"duration", inc.Duration().Round(time.Second),
			"was_confirmed", inc.Confirmed())
	}

	// Flapping is reported alongside the incident lifecycle rather than in
	// place of it, so this runs after the incident has been persisted.
	if tr.FlappingChanged {
		if tr.Flapping {
			r.log.Warn("monitor is flapping, notifications suppressed",
				"monitor", o.Monitor.Name)
		} else {
			r.log.Info("monitor stopped flapping, notifications resumed",
				"monitor", o.Monitor.Name)
		}
	}

	// Stream every structural change, including suppressed ones.
	//
	// Suppression is about not waking a human at 3am; it is not about hiding
	// what happened from someone actively looking at the dashboard. A screen
	// that silently omits a flapping monitor's transitions is lying by
	// omission — the exact failure this product exists to prevent.
	if tr.Event != state.EventNone {
		r.publish(events.Event{
			Kind:      events.KindStatus,
			MonitorID: o.Monitor.ID,
			At:        tr.At,
			Payload: statusPayload{
				Event:      string(tr.Event),
				Cause:      tr.Cause,
				Error:      tr.Error,
				Suppressed: tr.Suppressed,
			},
		})
	}

	if tr.Suppressed {
		r.log.Debug("notification suppressed while flapping",
			"monitor", o.Monitor.Name, "event", tr.Event)
	}

	if !tr.Notify || r.notify == nil {
		return persistErr
	}

	// A notification with no incident behind it would be an empty alert. That
	// happens when flapping starts or ends on an otherwise uneventful check:
	// worth a log line, not worth waking anyone.
	if tr.Event == state.EventNone {
		return persistErr
	}

	m, err := r.db.GetMonitor(ctx, o.Monitor.ID)
	if err != nil {
		r.log.Error("failed to load monitor for alert", "monitor_id", o.Monitor.ID, "error", err)
		return persistErr
	}

	r.notify(Alert{Monitor: m, Incident: inc, Event: tr.Event, At: tr.At})
	return persistErr
}

// ErrUnsupportedType is returned when a monitor names a check type that has no
// implementation. It is a configuration fault, not a check failure.
var ErrUnsupportedType = errors.New("unsupported monitor type")

// ErrPushNotProbeable is returned when something asks for an on-demand check
// of a push monitor. It is a client mistake, not a server fault, which is why
// it is distinguishable from ErrUnsupportedType.
var ErrPushNotProbeable = errors.New("push monitors cannot be checked on demand")

// CheckNow probes a monitor once, outside its schedule, and returns the result.
//
// It implements the API's Prober interface, which is why it takes a
// store.Monitor rather than the checker's shape: the caller has just loaded
// the row and should not have to know about the mapping.
//
// An enabled monitor's result goes through the same recording path as a
// scheduled one, so a monitor that has just been fixed turns green immediately
// instead of at the next tick. A paused monitor's result is returned but not
// recorded: the monitor promised not to watch it, and writing heartbeats into that gap
// would present an unmonitored period as a monitored one.
func (r *Runner) CheckNow(ctx context.Context, m store.Monitor) (checker.Result, error) {
	// There is nothing to probe on demand: a push monitor's health is a
	// statement about whether its job reported in, and the server cannot
	// make that happen by asking. Falling through would produce "unsupported
	// monitor type push", which is true of the checker table and misleading
	// about the product.
	if m.Type == store.TypePush {
		return checker.Result{}, fmt.Errorf("%w: a push monitor is reported to, not checked", ErrPushNotProbeable)
	}

	cm := toCheckerMonitor(m)

	c, ok := r.sch.CheckerFor(cm.Type)
	if !ok {
		return checker.Result{}, fmt.Errorf("%w: %q", ErrUnsupportedType, cm.Type)
	}

	res := c.Check(ctx, cm)

	if m.Enabled {
		// Record on the caller's behalf but not on its context: a client
		// that disconnects the instant the probe returns must not abort the
		// heartbeat write half-way. record uses its own bounded context.
		r.record(scheduler.Outcome{Monitor: cm, Result: res})
	}
	return res, nil
}
