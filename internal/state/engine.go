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

	// StatusWarning means checks are failing but not yet often enough to
	// count. This state is why the product does not cry wolf: it is visible
	// in the UI as "Warning" without anyone being alerted.
	StatusWarning Status = "warning"

	// StatusDown means failure is confirmed.
	StatusDown Status = "down"

	// StatusRecovering means a confirmed incident has seen passing checks,
	// but not yet enough of them in a row to close it.
	//
	// Confirmation asks for several failures before it alerts; recovery used
	// to close on the first pass. A half-broken service that fails three
	// times and then passes once therefore sent a "resolved" that was as
	// false as any alert the failure threshold exists to prevent, and
	// someone who reads "resolved" stops looking. This state is the
	// recovering side of the same rule: the incident stays open and nobody
	// is told anything until the recovery threshold is met.
	StatusRecovering Status = "recovering"

	// StatusExpiring means the checks pass, but the certificate they were
	// served expires inside the monitor's warning window.
	//
	// It is not an outage, and it is not Warning either: Warning means a
	// failure that is not confirmed yet, and an expiring certificate is a
	// fact read off the certificate, not a probe that might have been
	// unlucky. Counting it as down put a reachable site at 0% uptime and in
	// red on a public status page for the weeks before its renewal. The
	// notice still opens an incident and alerts, because a warning nobody
	// reads is how certificates expire on a Sunday; only the downtime goes.
	// An expired certificate fails the check, and that is Down.
	StatusExpiring Status = "expiring"
)

// Confirmed reports whether a status belongs to a confirmed, still-open
// incident: down, or on its way back up but not there yet. Callers that
// decide how often to check, or whether an outage is still in progress, want
// this rather than a comparison with StatusDown alone.
func (s Status) Confirmed() bool {
	return s == StatusDown || s == StatusRecovering
}

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

	// EventIncidentReminder is a repeat of an alert nobody has answered.
	//
	// It carries no state change: the incident was already confirmed and is
	// still down. It exists so the notifier can word a reminder differently
	// from a first alert ("still down, 6h12m") and so the UI can tell the
	// two apart.
	EventIncidentReminder Event = "incident_reminder"

	// EventIncidentResolved is recovery. It alerts only when the incident it
	// closes was confirmed: nobody wants a "resolved" for an outage they were
	// never told about.
	EventIncidentResolved Event = "incident_resolved"
)

// Observation is one check result as the engine needs it.
type Observation struct {
	MonitorID int64
	OK        bool
	At        time.Time

	// Kind and Error describe the failure. Ignored when OK, except on an
	// Expiring observation, where they carry the notice.
	Kind  string
	Error string

	// Expiring marks a passing check whose certificate expires inside the
	// monitor's warning window. It is ignored unless OK is set: a check
	// that failed for any other reason is a failure, whatever the
	// certificate says. See StatusExpiring.
	Expiring bool

	// FailureThreshold is how many consecutive failures confirm an incident.
	// It comes from the monitor's `retries` column, so it is per-monitor
	// rather than global: a flaky third-party API and a production checkout
	// page do not deserve the same patience.
	//
	// Zero means 1 — confirm immediately.
	FailureThreshold int

	// RecoveryThreshold is how many consecutive passing checks close a
	// confirmed incident. It comes from the monitor's `recovery_threshold`
	// column, for the same reason FailureThreshold is per monitor.
	//
	// It only applies to confirmed incidents. An unconfirmed one was never
	// announced, so there is no false "resolved" to guard against, and it
	// closes on the first pass as before.
	//
	// Zero means 1 — resolve on the first passing check.
	RecoveryThreshold int

	// LocalNetwork marks a failure that the host's own connectivity
	// explains: the check failed on a network error, and so did every
	// connectivity canary. It is ignored when OK.
	//
	// Such a failure is not evidence against the monitor. It is recorded as
	// a warning, it does not advance the failure streak, and it never opens
	// or confirms an incident, so twenty monitors behind one dead uplink do
	// not become twenty incidents and twenty dents in their uptime. The
	// streak is held rather than reset: a target that is still failing when
	// the host comes back online confirms on its next failure, as it would
	// have without the interruption.
	//
	// A monitor that is already confirmed down stays down. The canary only
	// speaks to whether a new incident is the host's fault; it does not
	// retract one that was confirmed while the host could still see out.
	LocalNetwork bool
}

// CauseLocalNetwork is the cause recorded for a failure that LocalNetwork
// explains, in place of the checker's own classification.
const CauseLocalNetwork = "local_network"

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

	// ConsecutiveOKs is the passing streak inside a confirmed incident, for
	// the UI to show "1 of 2" while a monitor is recovering. It is zero
	// whenever no confirmed incident is open.
	ConsecutiveOKs int

	// RecoveryThreshold is the number of passes this observation needed to
	// close the incident. Set only when To is StatusRecovering, so "1 of 2"
	// can be said from the transition alone.
	RecoveryThreshold int

	// RecoveredAt is when the outage ended, set only on
	// EventIncidentResolved. It is the first passing check of the streak
	// that met the recovery threshold, not the check that met it: the
	// checks spent confirming a recovery are not downtime, and recording the
	// later moment would charge them to the uptime figure.
	RecoveredAt time.Time

	// SnapshotsSpent is how much of this incident's response-snapshot budget
	// is already on disk, not counting the check being reported. A failure
	// only spends budget once its snapshot is stored, so the caller confirms
	// a write with SpendSnapshot; see monitorState.
	SnapshotsSpent int

	// Notify says whether this transition should reach a human. It is false
	// for uninteresting changes and false while flapping is suppressed.
	Notify bool

	// Suppressed marks a transition that would have notified but did not.
	// The UI shows these so suppression is visible rather than mysterious —
	// silently dropping alerts is how monitoring tools lose trust.
	Suppressed bool

	// Flapping reports whether the monitor is currently oscillating, and
	// FlappingChanged whether that status just changed.
	//
	// These are separate from Event on purpose. Event is an instruction to
	// the persistence layer — open, confirm or resolve an incident — while
	// flapping only concerns whether a human hears about it. Folding the two
	// together previously let a flapping signal overwrite a resolve, leaving
	// the incident open in the database forever while the engine believed
	// the monitor was up.
	Flapping        bool
	FlappingChanged bool

	// Cause and Error carry the failure classification through to the
	// incident record and the notification body.
	Cause string
	Error string

	// Notice says the incident this transition is about is a certificate
	// notice (StatusExpiring) rather than an outage: the one open after it,
	// including while an unconfirmed failure is counted against it, or the
	// one it resolved. The caller uses it to open the incident as a notice,
	// and to keep a failure's text off one: an unconfirmed failure must not
	// relabel what the notice said.
	Notice bool

	// Replaces says Event applies to a new incident that starts at
	// StartedAt: the incident that was open before this check is closed at
	// that moment first, without anyone being told. It is set when a
	// failure is confirmed against an open certificate notice, because an
	// outage that began this morning is not the notice that began two
	// weeks ago, and reporting it as such would date the outage from the
	// notice.
	Replaces  bool
	StartedAt time.Time
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

	// consecutiveOKs and recoveringSince track a confirmed incident's
	// passing streak: how many checks in a row have passed, and when the
	// first of them ran. Both are zero outside a confirmed incident.
	consecutiveOKs  int
	recoveringSince time.Time

	// recoveryThreshold is the threshold the last recovering pass was
	// measured against, so a reader can say "1 of 2" without looking the
	// monitor up again. Meaningful only while status is StatusRecovering.
	recoveryThreshold int

	// snapshotsSpent is the per-incident response-snapshot budget: how many
	// snapshots this incident has actually stored. It is not the failure
	// streak, because plenty of failures store nothing — a monitor with
	// response capture off, a check whose response was never captured, a
	// failure while the monitor flaps, a heartbeat whose write fails. Each of
	// those used to consume budget and could leave a genuine failure later in
	// the same outage with nothing left to spend.
	snapshotsSpent int

	// incidentOpen tracks whether an incident record exists but has not been
	// resolved. It survives the pending→down promotion.
	incidentOpen      bool
	incidentConfirmed bool

	// notice marks the open, confirmed incident as a certificate notice
	// rather than an outage, and failingSince is the first failure of a
	// streak counted against it, which is when an outage confirmed from
	// that streak began. Both are zero outside a notice.
	notice       bool
	failingSince time.Time

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
	if o.RecoveryThreshold <= 0 {
		o.RecoveryThreshold = 1
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

	switch {
	case o.OK && o.Expiring:
		e.observeExpiring(ms, &t, o)
	case o.OK:
		e.observeSuccess(ms, &t, o)
	case o.LocalNetwork && !ms.status.Confirmed():
		observeLocalNetwork(ms, &t)
	default:
		e.observeFailure(ms, &t, o)
	}

	t.ConsecutiveFails = ms.consecutiveFails
	t.ConsecutiveOKs = ms.consecutiveOKs
	t.SnapshotsSpent = ms.snapshotsSpent
	t.Notice = t.Notice || ms.notice

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
	case ms.status == StatusRecovering:
		// A failure while recovering. The passing streak is broken and the
		// monitor is down again, but this is the same outage: the incident
		// never closed and the user was never told it had, so there is no
		// new alert and no flip for the flapping window to count.
		ms.consecutiveOKs = 0
		ms.recoveringSince = time.Time{}
		ms.status = StatusDown
		t.To = StatusDown

	case ms.status == StatusDown:
		// Already down. The error text may have changed (a connection error
		// becoming a 500 is useful detail), but there is nothing to announce.
		return

	case ms.notice && ms.consecutiveFails >= o.FailureThreshold:
		// A failure confirmed while a certificate notice is open. The
		// outage is a new incident, dated from the first failure of this
		// streak; the notice it replaces closes without a message, since
		// the alert about the outage is the news.
		t.Replaces = true
		t.StartedAt = ms.failingSince
		if t.StartedAt.IsZero() {
			t.StartedAt = t.At
		}
		ms.notice = false
		ms.failingSince = time.Time{}
		ms.incidentOpen = true
		ms.incidentConfirmed = true
		ms.status = StatusDown
		ms.recordChange(t.At, e.flapWindow)

		t.To = StatusDown
		t.Event = EventIncidentConfirmed
		t.Notify = true

	case ms.notice:
		// Failing against an open notice, not yet confirmed. The notice
		// stays open, and nothing is said: like any unconfirmed failure,
		// this may be a blip.
		if ms.failingSince.IsZero() {
			ms.failingSince = t.At
		}
		ms.status = StatusWarning
		t.To = StatusWarning

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
		ms.status = StatusWarning
		t.To = StatusWarning
	}
}

// observeLocalNetwork records a failure the host's own connectivity explains.
// See Observation.LocalNetwork.
//
// The monitor shows as a warning — something is failing, nothing is
// confirmed — and nothing else moves: no streak, no incident, no flip for the
// flapping window. An incident that was already open and unconfirmed stays
// open, so its start time survives if the target turns out to be down too.
func observeLocalNetwork(ms *monitorState, t *Transition) {
	ms.status = StatusWarning
	t.To = StatusWarning
	t.Cause = CauseLocalNetwork
}

// WouldConfirm reports whether one more failure would confirm an incident for
// this monitor under the given threshold: it is not already confirmed, and
// its failure streak is one short of the threshold.
//
// The caller asks before observing a failure, so that the cost of finding out
// whether the host itself is offline is paid only at the moment an alert
// would go out, not on every failed check.
func (e *Engine) WouldConfirm(monitorID int64, failureThreshold int) bool {
	if failureThreshold <= 0 {
		failureThreshold = 1
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	ms := e.state[monitorID]
	if ms == nil {
		return failureThreshold == 1
	}
	return !ms.status.Confirmed() && ms.consecutiveFails+1 >= failureThreshold
}

// observeExpiring records a passing check whose certificate expires inside the
// warning window. See StatusExpiring.
//
// The notice is confirmed at once, without the failure threshold: the date on
// a certificate does not change between two checks, so there is no blip to
// wait out. It is one incident and one alert for as long as it lasts, however
// many checks see the same certificate.
//
// During an outage the check is a pass like any other: it counts towards the
// recovery streak, and the outage resolves as usual. One transition carries
// one event, so the caller observes the same check again once the resolution
// is applied, and that second observation opens the notice: "back up" first,
// then the notice, rather than one message that has to say both.
func (e *Engine) observeExpiring(ms *monitorState, t *Transition, o Observation) {
	switch {
	case ms.notice:
		// The notice is open already, and a streak of unconfirmed
		// failures against it is over.
		ms.consecutiveFails = 0
		ms.failingSince = time.Time{}
		ms.status = StatusExpiring
		t.To = StatusExpiring

	case ms.incidentOpen:
		// An outage, confirmed or not, is open. This check ends it or
		// counts towards ending it, as any passing check would.
		e.observeSuccess(ms, t, o)

	default:
		ms.consecutiveFails = 0
		ms.snapshotsSpent = 0
		ms.incidentOpen = true
		ms.incidentConfirmed = true
		ms.notice = true
		ms.status = StatusExpiring

		// No flip is recorded: the flapping window counts outages starting
		// and ending, and a certificate's date does not oscillate. While a
		// monitor does flap, applyFlapping holds this alert back like any
		// other.
		t.To = StatusExpiring
		t.Event = EventIncidentConfirmed
		t.Notify = true
	}
}

// observeSuccess resets the failure streak and resolves any open incident
// once the recovery threshold allows it.
func (e *Engine) observeSuccess(ms *monitorState, t *Transition, o Observation) {
	ms.consecutiveFails = 0

	// A renewed certificate closes its notice on the first check that sees
	// it. The recovery threshold guards against a half-broken service's
	// lucky pass; a new expiry date is not luck.
	if ms.incidentOpen && ms.incidentConfirmed && !ms.notice {
		// A confirmed incident has to earn its all-clear the same way it
		// earned its alert: with a streak, not one sample.
		if ms.consecutiveOKs == 0 {
			ms.recoveringSince = t.At
		}
		ms.consecutiveOKs++
		if ms.consecutiveOKs < o.RecoveryThreshold {
			// The snapshot budget is left alone: it belongs to the
			// incident, and the incident is still open.
			ms.recoveryThreshold = o.RecoveryThreshold
			ms.status = StatusRecovering
			t.To = StatusRecovering
			t.RecoveryThreshold = o.RecoveryThreshold
			return
		}
	}

	t.RecoveredAt = t.At
	if !ms.recoveringSince.IsZero() {
		t.RecoveredAt = ms.recoveringSince
	}
	ms.consecutiveOKs = 0
	ms.recoveringSince = time.Time{}
	ms.snapshotsSpent = 0

	if !ms.incidentOpen {
		t.RecoveredAt = time.Time{}
		// Steady state: up and staying up, or the very first check.
		if ms.status != StatusUp {
			ms.status = StatusUp
			t.To = StatusUp
		}
		return
	}

	wasConfirmed, wasNotice := ms.incidentConfirmed, ms.notice
	ms.incidentOpen = false
	ms.incidentConfirmed = false
	ms.notice = false
	ms.failingSince = time.Time{}
	ms.status = StatusUp
	t.To = StatusUp
	t.Event = EventIncidentResolved
	t.Notice = wasNotice

	if wasConfirmed {
		// Only a confirmed incident produced an alert, so only a confirmed
		// incident gets an all-clear. Recovering from a single failed probe
		// nobody heard about must stay silent.
		//
		// A renewed certificate is not a flip for the flapping window: the
		// window counts outages starting and ending, and a notice is
		// neither.
		if !wasNotice {
			ms.recordChange(t.At, e.flapWindow)
		}
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
//
// It never touches t.Event. Event is what the persistence layer acts on, and
// an incident that was opened, confirmed or resolved must be recorded whether
// or not a human is told about it. Flapping is reported alongside, in its own
// fields.
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

	t.Flapping = flappingNow
	t.FlappingChanged = flappingNow != ms.flapping

	switch {
	case flappingNow && !ms.flapping:
		// Just started oscillating. Notify once so the user understands why
		// the alerts are about to stop, and let this transition's own event
		// stand — the incident it describes still has to be recorded.
		ms.flapping = true
		t.Notify = true
		t.Suppressed = false

	case !flappingNow && ms.flapping:
		// Settled down. A warning (including its recovery) never alerts.
		ms.flapping = false
		t.Notify = t.Notify || (t.To != StatusWarning && t.From != StatusWarning)

	case flappingNow && ms.flapping:
		// Still oscillating. Hold the notification back; the incident record
		// is written regardless.
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

// Recovery reports a recovering monitor's passing streak: how many checks in
// a row have passed and how many the incident needs before it closes. ok is
// false for every monitor that is not recovering, including one the engine
// has never seen, so a caller cannot mistake "no streak" for "zero passes".
func (e *Engine) Recovery(monitorID int64) (passes, threshold int, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ms := e.state[monitorID]
	if ms == nil || ms.status != StatusRecovering {
		return 0, 0, false
	}
	return ms.consecutiveOKs, ms.recoveryThreshold, true
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

// Retain drops every monitor not present in live.
//
// This is the reconciliation the engine needs to stay bounded: monitors are
// deleted and paused through several different paths, and relying on each of
// them to call Forget is the kind of bookkeeping that eventually gets missed.
// Comparing against the authoritative set on every reload cannot drift.
//
// A paused monitor is forgotten along with a deleted one, deliberately. A
// monitor resumed an hour later should be judged on what it does now rather
// than resuming a failure streak from before the pause.
func (e *Engine) Retain(live map[int64]struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id := range e.state {
		if _, ok := live[id]; !ok {
			delete(e.state, id)
		}
	}
}

// Len reports how many monitors the engine is tracking. It exists so tests
// and metrics can assert the map does not grow without bound.
// SpendSnapshot records that one response snapshot was stored for a monitor's
// open incident and reports the incident's new total.
//
// The budget is charged here rather than in Observe because only the caller
// knows whether the snapshot reached the disk. Charging on every failure meant
// suppressed and failed writes drained an allowance they never used.
func (e *Engine) SpendSnapshot(monitorID int64) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	ms, ok := e.state[monitorID]
	if !ok {
		return 0
	}
	ms.snapshotsSpent++
	return ms.snapshotsSpent
}

func (e *Engine) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.state)
}

// RestoredState is what the database still knows about a monitor when the
// process starts, as opposed to what the engine has observed itself.
type RestoredState struct {
	Status            Status
	IncidentOpen      bool
	IncidentConfirmed bool

	// ConsecutiveFails seeds the alert streak: how many failures this open
	// incident has already recorded. Restoring it as zero meant a pending
	// incident that was one check away from confirming had to start counting
	// again, so a monitor could stay pending across a restart loop and never
	// alert at all.
	ConsecutiveFails int

	// SnapshotsSpent seeds the response-snapshot budget with the number of
	// snapshots this incident already wrote. Restoring it as zero handed
	// every restart during a long outage a fresh budget, so the same error
	// page was written again on each one.
	//
	// It is separate from ConsecutiveFails because the two counts disagree:
	// a monitor with response capture disabled fails without ever storing a
	// snapshot, and a snapshot is skipped while a monitor flaps.
	//
	// The database count is authoritative here for the same reason: it is a
	// count of rows that exist, not of failures that happened.
	SnapshotsSpent int

	// Notice marks the open, confirmed incident as a certificate notice
	// rather than an outage. The caller restores Status as StatusExpiring
	// with it.
	Notice bool

	// FailingSince is the first failure of a streak counted against a
	// restored notice, so an outage confirmed from that streak is dated from
	// it. Zero without a streak, and ignored outside a notice.
	FailingSince time.Time
}

// Restore seeds a monitor's state from the database at startup.
//
// Without this, a restart would re-open an incident that is already open and
// re-alert for an outage the user was told about ten minutes ago — which is
// exactly the kind of noise this package exists to prevent.
//
// A recovery streak is deliberately not restored. A monitor that was
// recovering when the process stopped comes back as down with no passing
// checks counted, and has to meet its recovery threshold again. That can
// delay a "resolved" by the checks it had already passed, but it can never
// send one early — and a false "resolved" is the thing the threshold exists
// to prevent. A caller that restores StatusRecovering gets StatusDown.
func (e *Engine) Restore(monitorID int64, rs RestoredState) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if rs.ConsecutiveFails < 0 {
		rs.ConsecutiveFails = 0
	}
	if rs.SnapshotsSpent < 0 {
		rs.SnapshotsSpent = 0
	}

	if rs.Status == StatusRecovering {
		rs.Status = StatusDown
	}

	ms := &monitorState{
		status:            rs.Status,
		incidentOpen:      rs.IncidentOpen,
		incidentConfirmed: rs.IncidentConfirmed,
		consecutiveFails:  rs.ConsecutiveFails,
		snapshotsSpent:    rs.SnapshotsSpent,
		notice:            rs.Notice && rs.IncidentOpen && rs.IncidentConfirmed,
	}
	if ms.notice && ms.consecutiveFails > 0 {
		ms.failingSince = rs.FailingSince
	}
	e.state[monitorID] = ms
}
