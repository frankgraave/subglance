package watchdog

import "time"

// Snapshot is process-local diagnostic history, never a claim that an external
// alarm is armed. It deliberately cannot contain a URL, body or raw error.
// All nullable fields remain null until observed by the real send path.
type Snapshot struct {
	Configured      bool       `json:"configured"`
	IntervalSeconds *float64   `json:"interval_seconds"`
	LastDecisionAt  *time.Time `json:"last_decision_at"`
	LastAttemptAt   *time.Time `json:"last_attempt_at"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastResult      *string    `json:"last_result"`
	LastStatusCode  *int       `json:"last_status_code"`
	LastEvent       *string    `json:"last_event"`
	Suppressed      bool       `json:"suppressed"`
	InFlight        bool       `json:"in_flight"`
	Overdue         bool       `json:"overdue"`
}

type history struct {
	decision, attempt, success time.Time
	result, event              string
	status                     int
	suppressed, inFlight       bool
}

// Snapshot copies every value while holding the read lock. Its pointers belong
// to the reader, so changing a returned timestamp cannot corrupt live history.
// A nil receiver is explicitly disabled; an unwired API source is different.
func (w *Watchdog) Snapshot() Snapshot {
	if w == nil {
		return Snapshot{}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	h := w.history
	reference := h.decision
	if reference.IsZero() {
		reference = w.createdAt
	}
	return Snapshot{
		Configured: true, IntervalSeconds: known(w.interval.Seconds()),
		LastDecisionAt: known(h.decision), LastAttemptAt: known(h.attempt), LastSuccessAt: known(h.success),
		LastResult: known(h.result), LastStatusCode: known(h.status), LastEvent: known(h.event),
		Suppressed: h.suppressed, InFlight: h.inFlight,
		Overdue: time.Since(reference) > w.interval+pingTimeout,
	}
}

func known[T comparable](value T) *T {
	var zero T
	if value == zero {
		return nil
	}
	return &value
}

func (w *Watchdog) beginAttempt(event string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UTC()
	w.history.decision, w.history.attempt = now, now
	w.history.event = event
	w.history.suppressed, w.history.inFlight = false, true
}

func (w *Watchdog) finishAttempt(result string, status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.history.result, w.history.status, w.history.inFlight = result, status, false
	if result == "succeeded" {
		w.history.success = time.Now().UTC()
	}
}
