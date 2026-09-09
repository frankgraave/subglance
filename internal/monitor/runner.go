// Package monitor wires the storage layer to the scheduler.
//
// It is the seam between "what should be checked" (the database), "run it"
// (the scheduler) and "does this mean anything" (the state engine), so none of
// the three has to know about the others.
package monitor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
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
}

// New builds a Runner with the standard set of checkers.
func New(opts Options) *Runner {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	guard := checker.NewGuard(opts.AllowPrivateTargets)
	httpChecker := checker.NewHTTPChecker(checker.HTTPOptions{Guard: guard})

	r := &Runner{
		db:     opts.DB,
		log:    log,
		notify: opts.Notify,
		engine: state.New(state.Options{
			FlapWindow:    opts.FlapWindow,
			FlapThreshold: opts.FlapThreshold,
		}),
	}

	r.sch = scheduler.New(scheduler.Options{
		Registry: scheduler.RegistryFunc(r.jobs),
		Checkers: map[checker.Type]checker.Checker{
			checker.TypeHTTP: httpChecker,
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
func (r *Runner) Run(ctx context.Context) error {
	if err := r.restore(ctx); err != nil {
		// A failed restore is not fatal — monitoring with a clean slate beats
		// not monitoring — but it does mean possible duplicate alerts, so it
		// is logged at error level.
		r.log.Error("failed to restore incident state; duplicate alerts are possible", "error", err)
	}
	return r.sch.Run(ctx)
}

// Size reports how many monitors are currently scheduled.
func (r *Runner) Size() int { return r.sch.Size() }

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
func (r *Runner) jobs(ctx context.Context) ([]scheduler.Job, error) {
	monitors, err := r.db.ListEnabledMonitors(ctx)
	if err != nil {
		return nil, err
	}

	jobs := make([]scheduler.Job, 0, len(monitors))
	for _, m := range monitors {
		jobs = append(jobs, scheduler.Job{
			Monitor:  toCheckerMonitor(m),
			Interval: time.Duration(m.IntervalS) * time.Second,
		})
	}
	return jobs, nil
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
	}
}

// record persists one check result and advances the state machine.
//
// A failure to write must never stop the scheduler: losing one heartbeat is
// survivable, a monitoring system that stops monitoring is not.
func (r *Runner) record(o scheduler.Outcome) {
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

	if err := r.db.RecordHeartbeat(ctx, hb); err != nil {
		r.log.Error("failed to record heartbeat",
			"monitor_id", o.Monitor.ID, "monitor", o.Monitor.Name, "error", err)
		// Still run the state machine: the check did happen, and an outage
		// should be reported even if the database is having a bad moment.
	}

	tr := r.engine.Observe(state.Observation{
		MonitorID:        o.Monitor.ID,
		OK:               o.Result.OK,
		At:               hb.TS,
		Kind:             string(o.Result.Kind),
		Error:            o.Result.Error,
		FailureThreshold: o.Monitor.Retries,
	})

	r.applyTransition(ctx, o, tr)
}

// applyTransition writes the incident side of a transition and emits an alert
// when one is warranted.
func (r *Runner) applyTransition(ctx context.Context, o scheduler.Outcome, tr state.Transition) {
	var (
		inc store.Incident
		err error
	)

	switch tr.Event {
	case state.EventNone:
		// Nothing structural changed. Keep the open incident's error text
		// current so the UI shows the latest symptom.
		if !o.Result.OK {
			if err := r.db.UpdateIncidentError(ctx, o.Monitor.ID, tr.Cause, tr.Error); err != nil {
				r.log.Error("failed to update incident error", "monitor_id", o.Monitor.ID, "error", err)
			}
		}
		return

	case state.EventIncidentOpened:
		inc, err = r.db.OpenIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error)
		if err != nil {
			r.log.Error("failed to open incident", "monitor_id", o.Monitor.ID, "error", err)
			return
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
				return
			}
			if err := r.db.ConfirmIncident(ctx, o.Monitor.ID, tr.At, tr.Cause, tr.Error); err != nil {
				r.log.Error("failed to confirm incident", "monitor_id", o.Monitor.ID, "error", err)
				return
			}
		} else if err != nil {
			r.log.Error("failed to confirm incident", "monitor_id", o.Monitor.ID, "error", err)
			return
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
			return
		}
		if err != nil {
			r.log.Error("failed to resolve incident", "monitor_id", o.Monitor.ID, "error", err)
			return
		}
		r.log.Info("incident resolved",
			"monitor", o.Monitor.Name,
			"duration", inc.Duration().Round(time.Second),
			"was_confirmed", inc.Confirmed())

	case state.EventFlappingStarted:
		r.log.Warn("monitor is flapping, notifications suppressed",
			"monitor", o.Monitor.Name)

	case state.EventFlappingEnded:
		r.log.Info("monitor stopped flapping, notifications resumed",
			"monitor", o.Monitor.Name)
	}

	if tr.Suppressed {
		r.log.Debug("notification suppressed while flapping",
			"monitor", o.Monitor.Name, "event", tr.Event)
	}

	if !tr.Notify || r.notify == nil {
		return
	}

	m, err := r.db.GetMonitor(ctx, o.Monitor.ID)
	if err != nil {
		r.log.Error("failed to load monitor for alert", "monitor_id", o.Monitor.ID, "error", err)
		return
	}

	r.notify(Alert{Monitor: m, Incident: inc, Event: tr.Event, At: tr.At})
}
