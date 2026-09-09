// Package state turns a stream of check results into incidents.
//
// # Why this is a separate package
//
// A checker answers one question: did this probe succeed, right now. That is
// not the same question as "is this site down", and conflating the two is what
// makes monitoring tools untrustworthy. A single failed probe can mean a
// dropped packet, a redeploy, or a garbage-collection pause. Alerting on it
// spends the user's trust for nothing — and per product principle 5, a wrong
// alert costs more trust than ten missed ones.
//
// So the engine sits between the checker and the notifier and decides, from
// history rather than from one sample, when something has actually gone wrong.
//
// # Deliberately pure
//
// The engine holds no database handle, does no IO and never blocks. It takes
// an observation and returns a Transition describing what changed. That makes
// every rule here testable as a table of inputs and expected outputs, which
// matters because these rules are the ones that wake people at night.
//
// Persisting the transition is the caller's job (see internal/monitor).
package state

import (
	"sync"
	"time"
)

// Status is what the engine believes about a monitor right now.
type Status string

const (
	// StatusUnknown is the state before any result has been seen.
	StatusUnknown Status = "unknown"

	// StatusUp means the last check passed.
	StatusUp Status = "up"

	// StatusPending means checks are failing but not yet often enough to
	// count. This state is why the product does not cry wolf: it is visible
	// in the UI as "degraded" without anyone being alerted.
	StatusPending Status = "pending"

	// StatusDown means failure is confirmed.
	StatusDown Status = "down"
)

// Event is what happened at a transition, from the notifier's point of view.
type Event string

const (
	// EventNone means nothing worth reporting changed.
	EventNone Event = ""

	// EventIncidentOpened is the first failure of a run. An incident record
	// is created, but nobody is alerted yet — it is not confirmed.
	EventIncidentOpened Event = "incident_opened"

	// EventIncidentConfirmed is the threshold being crossed. This is the
	// only event that alerts on failure.
	EventIncidentConfirmed Event = "incident_confirmed"

	// EventIncidentResolved is recovery. It alerts only when the incident it
	// closes was confirmed: nobody wants a "resolved" for an outage they were
	// never told about.
	EventIncidentResolved Event = "incident_resolved"

	// EventFlappingStarted means the monitor is oscillating and further
	// notifications are being held back.
	EventFlappingStarted Event = "flapping_started"

	// EventFlappingEnded means it settled down.
	EventFlappingEnded Event = "flapping_ended"
)

// Observation is one check result as the engine needs it.
type Observation struct {
	MonitorID int64
	OK        bool
	At        time.Time

	// Kind and Error describe the failure. Ignored when OK.
	Kind  string
	Error string

	// FailureThreshold is how many consecutive failures confirm an incident.
	// It comes from the monitor's `retries` column, so it is per-monitor
	// rather than global: a flaky third-party API and a production checkout
	// page do not deserve the same patience.
	//
	// Zero means 1 — confirm immediately.
	FailureThreshold int
}

// Transition describes what the engine decided about one observation.
type Transition struct {
	MonitorID int64
	From      Status
	To        Status
	Event     Event
	At        time.Time

	// ConsecutiveFails is the current failure streak, for the UI to show
	// "2 of 3 failures" while a monitor is pending.
	ConsecutiveFails int

	// Notify says whether this transition should reach a human. It is false
	// for uninteresting changes and false while flapping is suppressed.
	Notify bool

	// Suppressed marks a transition that would have notified but did not.
	// The UI shows these so suppression is visible rather than mysterious —
	// silently dropping alerts is how monitoring tools lose trust.
	Suppressed bool

	// Cause and Error carry the failure classification through to the
	// incident record and the notification body.
	Cause string
	Error string
}

// Options configures an Engine.
type Options struct {
	// FlapWindow is how far back the engine looks when counting status
	// changes. Zero means 10 minutes.
	FlapWindow time.Duration

	// FlapThreshold is how many confirmed status changes inside the window
	// mark a monitor as flapping. Zero means 5.
	//
	// A monitor that alternates up/down every check is not giving anyone
	// actionable information; it is giving them a reason to mute the tool.
	FlapThreshold int

	// Now allows tests to control time. Zero means time.Now.
	Now func() time.Time
}

// Engine tracks per-monitor state and derives transitions.
//
// It is safe for concurrent use: the scheduler reports results from a worker
// pool, so several monitors land here at once.
type Engine struct {
	mu    sync.Mutex
	state map[int64]*monitorState

	flapWindow    time.Duration
	flapThreshold int
	now           func() time.Time
}

type monitorState struct {
	status Status

	consecutiveFails int

	// incidentOpen tracks whether an incident record exists but has not been
	// resolved. It survives the pending→down promotion.
	incidentOpen      bool
	incidentConfirmed bool

	// changes holds the times of recent confirmed status changes, oldest
	// first. Only up↔down counts; pending is not a change anyone sees.
	changes []time.Time

	flapping bool
}

// New builds an Engine with the given options.
func New(opts Options) *Engine {
	if opts.FlapWindow <= 0 {
		opts.FlapWindow = 10 * time.Minute
	}
	if opts.FlapThreshold <= 0 {
		opts.FlapThreshold = 5
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &Engine{
		state:         map[int64]*monitorState{},
		flapWindow:    opts.FlapWindow,
		flapThreshold: opts.FlapThreshold,
		now:           opts.Now,
	}
}

// Observe records one check result and returns the resulting transition.
//
// It always returns a Transition, even when nothing changed: the caller uses
// the same value to update "last seen" state in the UI, and an Event of
// EventNone is cheaper to ignore than a nil check at every call site.
func (e *Engine) Observe(o Observation) Transition {
	if o.At.IsZero() {
		o.At = e.now()
	}
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = 1
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	ms := e.state[o.MonitorID]
	if ms == nil {
		ms = &monitorState{status: StatusUnknown}
		e.state[o.MonitorID] = ms
	}

	from := ms.status
	t := Transition{
		MonitorID: o.MonitorID,
		From:      from,
		To:        from,
		At:        o.At,
		Cause:     o.Kind,
		Error:     o.Error,
	}

	if o.OK {
		e.observeSuccess(ms, &t)
	} else {
		e.observeFailure(ms, &t, o)
	}

	t.ConsecutiveFails = ms.consecutiveFails

	// Flapping is evaluated after the transition so a monitor that just
	// flipped counts its own flip. Suppression applies to the transition that
	// triggered it too — that flip is exactly the noise being suppressed.
	e.applyFlapping(ms, &t)

	return t
}

// observeFailure advances the failure streak and opens or confirms.
func (e *Engine) observeFailure(ms *monitorState, t *Transition, o Observation) {
	ms.consecutiveFails++

	switch {
	case ms.status == StatusDown:
		// Already down. The error text may have changed (a connection error
		// becoming a 500 is useful detail), but there is nothing to announce.
		return

	case ms.consecutiveFails >= o.FailureThreshold:
		// Confirmed. Open the incident first if the threshold is 1, so an
		// incident always exists before it is confirmed.
		if !ms.incidentOpen {
			ms.incidentOpen = true
		}
		ms.incidentConfirmed = true
		ms.status = StatusDown
		ms.recordChange(t.At, e.flapWindow)

		t.To = StatusDown
		t.Event = EventIncidentConfirmed
		t.Notify = true

	default:
		// Failing, not yet confirmed. An incident record is opened now so the
		// eventual confirmation carries an accurate start time — the outage
		// began at the first failure, not when we became sure of it.
		if !ms.incidentOpen {
			ms.incidentOpen = true
			t.Event = EventIncidentOpened
		}
		ms.status = StatusPending
		t.To = StatusPending
	}
}

// observeSuccess resets the streak and resolves any open incident.
func (e *Engine) observeSuccess(ms *monitorState, t *Transition) {
	ms.consecutiveFails = 0

	if !ms.incidentOpen {
		// Steady state: up and staying up, or the very first check.
		if ms.status != StatusUp {
			ms.status = StatusUp
			t.To = StatusUp
		}
		return
	}

	wasConfirmed := ms.incidentConfirmed
	ms.incidentOpen = false
	ms.incidentConfirmed = false
	ms.status = StatusUp
	t.To = StatusUp
	t.Event = EventIncidentResolved

	if wasConfirmed {
		// Only a confirmed incident produced an alert, so only a confirmed
		// incident gets an all-clear. Recovering from a single failed probe
		// nobody heard about must stay silent.
		ms.recordChange(t.At, e.flapWindow)
		t.Notify = true
	}
}

// recordChange appends a confirmed up↔down flip and drops ones that fell out
// of the window.
func (ms *monitorState) recordChange(at time.Time, window time.Duration) {
	cutoff := at.Add(-window)

	kept := ms.changes[:0]
	for _, c := range ms.changes {
		if c.After(cutoff) {
			kept = append(kept, c)
		}
	}
	ms.changes = append(kept, at)
}

// applyFlapping decides whether this monitor is oscillating, and suppresses
// the notification if so.
func (e *Engine) applyFlapping(ms *monitorState, t *Transition) {
	// Expire old flips even on checks that did not change state, otherwise a
	// monitor that settles stays marked as flapping until it next flips.
	cutoff := t.At.Add(-e.flapWindow)
	kept := ms.changes[:0]
	for _, c := range ms.changes {
		if c.After(cutoff) {
			kept = append(kept, c)
		}
	}
	ms.changes = kept

	flappingNow := len(ms.changes) >= e.flapThreshold

	switch {
	case flappingNow && !ms.flapping:
		ms.flapping = true
		// The state change itself is the news; report flapping rather than
		// the flip that triggered it, and notify once so the user knows why
		// the alerts stopped.
		t.Event = EventFlappingStarted
		t.Notify = true
		t.Suppressed = false

	case !flappingNow && ms.flapping:
		ms.flapping = false
		if t.Event == EventNone {
			t.Event = EventFlappingEnded
			t.Notify = true
		}

	case flappingNow && ms.flapping:
		// Hold everything back while it oscillates. The incident records keep
		// being written; only the notification is withheld.
		if t.Notify {
			t.Notify = false
			t.Suppressed = true
		}
	}
}

// Status reports the current status of a monitor.
func (e *Engine) Status(monitorID int64) Status {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ms := e.state[monitorID]; ms != nil {
		return ms.status
	}
	return StatusUnknown
}

// Flapping reports whether a monitor is currently suppressed for oscillation.
func (e *Engine) Flapping(monitorID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ms := e.state[monitorID]; ms != nil {
		return ms.flapping
	}
	return false
}

// Forget drops all state for a monitor. The caller must do this when a monitor
// is deleted, otherwise the map grows for the lifetime of the process.
func (e *Engine) Forget(monitorID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.state, monitorID)
}

// Restore seeds a monitor's state from the database at startup.
//
// Without this, a restart would re-open an incident that is already open and
// re-alert for an outage the user was told about ten minutes ago — which is
// exactly the kind of noise this package exists to prevent.
func (e *Engine) Restore(monitorID int64, status Status, incidentOpen, incidentConfirmed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.state[monitorID] = &monitorState{
		status:            status,
		incidentOpen:      incidentOpen,
		incidentConfirmed: incidentConfirmed,
	}
}
