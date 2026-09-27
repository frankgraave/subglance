package state

import (
	"fmt"
	"testing"
	"time"
)

// observe feeds one check through the engine with both thresholds set.
func observe(e *Engine, c *clock, ok bool, failures, recovery int) Transition {
	c.advance(time.Minute)
	o := Observation{
		MonitorID:         1,
		OK:                ok,
		At:                c.Now(),
		FailureThreshold:  failures,
		RecoveryThreshold: recovery,
	}
	if !ok {
		o.Kind = "status"
		o.Error = "unexpected status 500"
	}
	return e.Observe(o)
}

// The pattern from the ticket: a half-broken service that fails three times,
// passes once, and fails again. With a recovery threshold of 2 the single pass
// is not a recovery, so the user hears one alert and nothing else — no false
// "resolved", and no second alert for what is still the same outage.
func TestAHalfBrokenServiceSendsNoFalseResolve(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	var confirmed, resolved, notified int
	for cycle := range 3 {
		for range 3 {
			tr := observe(e, c, false, 3, 2)
			if tr.Event == EventIncidentConfirmed {
				confirmed++
			}
			if tr.Notify {
				notified++
			}
		}
		tr := observe(e, c, true, 3, 2)
		if tr.Event == EventIncidentResolved {
			resolved++
		}
		if tr.Notify {
			notified++
		}
		if cycle > 0 && tr.To != StatusRecovering {
			t.Errorf("cycle %d: status after one pass = %q, want recovering", cycle, tr.To)
		}
	}

	if confirmed != 1 || resolved != 0 || notified != 1 {
		t.Errorf("confirmed=%d resolved=%d notified=%d, want 1, 0, 1", confirmed, resolved, notified)
	}
	if e.Flapping(1) {
		t.Error("a failure while recovering must not count as a flip")
	}
}

func TestRecoveryThresholdPassesCloseTheIncident(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	observe(e, c, false, 1, 3)

	for i := 1; i <= 2; i++ {
		tr := observe(e, c, true, 1, 3)
		if tr.To != StatusRecovering || tr.Event != EventNone || tr.Notify {
			t.Fatalf("pass %d: to=%q event=%q notify=%v, want recovering, no event, silent", i, tr.To, tr.Event, tr.Notify)
		}
		if tr.ConsecutiveOKs != i {
			t.Errorf("pass %d: consecutive oks = %d, want %d", i, tr.ConsecutiveOKs, i)
		}
	}

	tr := observe(e, c, true, 1, 3)
	if tr.To != StatusUp || tr.Event != EventIncidentResolved || !tr.Notify {
		t.Fatalf("third pass: to=%q event=%q notify=%v, want up, resolved, notify", tr.To, tr.Event, tr.Notify)
	}
	if tr.ConsecutiveOKs != 0 {
		t.Errorf("consecutive oks after resolve = %d, want 0", tr.ConsecutiveOKs)
	}
}

// The outage ended at the first pass of the closing streak. Recording the
// moment the threshold was met instead would count the confirmation checks as
// downtime in every uptime figure.
func TestRecoveredAtIsTheFirstPassOfTheClosingStreak(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	observe(e, c, false, 1, 2)
	observe(e, c, true, 1, 2) // a pass that does not last
	observe(e, c, false, 1, 2)
	first := observe(e, c, true, 1, 2)
	last := observe(e, c, true, 1, 2)

	if last.Event != EventIncidentResolved {
		t.Fatalf("event = %q, want resolved", last.Event)
	}
	if !last.RecoveredAt.Equal(first.At) {
		t.Errorf("recovered at %v, want the first pass of the streak at %v (resolve ran at %v)", last.RecoveredAt, first.At, last.At)
	}
}

func TestAFailureWhileRecoveringResetsTheStreakSilently(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	observe(e, c, false, 1, 3)
	observe(e, c, true, 1, 3)
	observe(e, c, true, 1, 3)

	tr := observe(e, c, false, 1, 3)
	if tr.To != StatusDown || tr.Event != EventNone || tr.Notify {
		t.Fatalf("failure while recovering: to=%q event=%q notify=%v, want down, no event, silent", tr.To, tr.Event, tr.Notify)
	}
	if tr.ConsecutiveOKs != 0 {
		t.Errorf("consecutive oks = %d, want 0", tr.ConsecutiveOKs)
	}

	// The streak starts over: two passes are not enough any more.
	observe(e, c, true, 1, 3)
	if tr := observe(e, c, true, 1, 3); tr.Event != EventNone {
		t.Errorf("second pass of a new streak resolved: event = %q", tr.Event)
	}
	if tr := observe(e, c, true, 1, 3); tr.Event != EventIncidentResolved {
		t.Errorf("third pass of a new streak: event = %q, want resolved", tr.Event)
	}
}

// Threshold 1 and a zero threshold are both the behaviour before the setting
// existed: the first pass resolves.
func TestRecoveryThresholdOneResolvesOnTheFirstPass(t *testing.T) {
	for _, threshold := range []int{0, 1} {
		c := newClock()
		e := New(Options{Now: c.Now, FlapThreshold: 99})

		observe(e, c, false, 1, threshold)
		tr := observe(e, c, true, 1, threshold)
		if tr.To != StatusUp || tr.Event != EventIncidentResolved || !tr.Notify {
			t.Errorf("threshold %d: to=%q event=%q notify=%v, want up, resolved, notify", threshold, tr.To, tr.Event, tr.Notify)
		}
		if !tr.RecoveredAt.Equal(tr.At) {
			t.Errorf("threshold %d: recovered at %v, want %v", threshold, tr.RecoveredAt, tr.At)
		}
	}
}

// An unconfirmed incident was never announced, so there is no false "resolved"
// to protect against: it closes on the first pass whatever the threshold.
func TestAnUnconfirmedIncidentClosesOnTheFirstPass(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	observe(e, c, false, 3, 5)
	tr := observe(e, c, true, 3, 5)
	if tr.To != StatusUp || tr.Event != EventIncidentResolved || tr.Notify {
		t.Errorf("to=%q event=%q notify=%v, want up, resolved, silent", tr.To, tr.Event, tr.Notify)
	}
}

// A recovery streak is not persisted. After a restart the monitor is down with
// no passes counted, so a resolve can come late but never early.
func TestRestoreDropsTheRecoveryStreak(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	e.Restore(1, RestoredState{Status: StatusRecovering, IncidentOpen: true, IncidentConfirmed: true})
	if got := e.Status(1); got != StatusDown {
		t.Fatalf("restored status = %q, want down", got)
	}

	if tr := observe(e, c, true, 1, 2); tr.To != StatusRecovering || tr.ConsecutiveOKs != 1 {
		t.Fatalf("first pass after restore: to=%q oks=%d, want recovering, 1", tr.To, tr.ConsecutiveOKs)
	}
	if tr := observe(e, c, true, 1, 2); tr.Event != EventIncidentResolved {
		t.Errorf("second pass after restore: event = %q, want resolved", tr.Event)
	}
}

func TestConfirmedCoversRecovering(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusUnknown: false, StatusUp: false, StatusWarning: false,
		StatusDown: true, StatusRecovering: true,
	} {
		if got := s.Confirmed(); got != want {
			t.Errorf("%q.Confirmed() = %v, want %v", s, got, want)
		}
	}
}

// Recovery is what the API reads to say "Recovering (1 of 2)". It must report
// the streak only while the monitor is recovering, so an up or down monitor is
// never shown with a stale count.
func TestRecoveryReportsTheStreakOnlyWhileRecovering(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	if _, _, ok := e.Recovery(1); ok {
		t.Error("an unseen monitor reports a recovery streak")
	}
	observe(e, c, false, 1, 3)
	if _, _, ok := e.Recovery(1); ok {
		t.Error("a down monitor reports a recovery streak")
	}

	tr := observe(e, c, true, 1, 3)
	if tr.RecoveryThreshold != 3 {
		t.Errorf("transition recovery threshold = %d, want 3", tr.RecoveryThreshold)
	}
	if passes, threshold, ok := e.Recovery(1); !ok || passes != 1 || threshold != 3 {
		t.Errorf("after one pass: Recovery = %d, %d, %v, want 1, 3, true", passes, threshold, ok)
	}
	observe(e, c, true, 1, 3)
	if passes, _, ok := e.Recovery(1); !ok || passes != 2 {
		t.Errorf("after two passes: Recovery = %d, %v, want 2, true", passes, ok)
	}

	observe(e, c, false, 1, 3)
	if _, _, ok := e.Recovery(1); ok {
		t.Error("a failure while recovering left a recovery streak behind")
	}
	observe(e, c, true, 1, 3)
	observe(e, c, true, 1, 3)
	tr = observe(e, c, true, 1, 3)
	if tr.To != StatusUp {
		t.Fatalf("status after three passes = %q, want up", tr.To)
	}
	if tr.RecoveryThreshold != 0 {
		t.Errorf("a resolving transition carries recovery threshold %d, want 0", tr.RecoveryThreshold)
	}
	if _, _, ok := e.Recovery(1); ok {
		t.Error("a resolved monitor reports a recovery streak")
	}
}

// Every threshold the API accepts, 1 to 10: a confirmed incident spends
// threshold-1 passes recovering, silently, and the pass that meets the
// threshold resolves it, alerts, and dates the recovery to the first pass.
func TestRecoveryThresholdTable(t *testing.T) {
	for threshold := 1; threshold <= 10; threshold++ {
		t.Run(fmt.Sprintf("threshold %d", threshold), func(t *testing.T) {
			c := newClock()
			e := New(Options{Now: c.Now, FlapThreshold: 99})

			if tr := observe(e, c, false, 1, threshold); tr.Event != EventIncidentConfirmed {
				t.Fatalf("first failure: event = %q, want confirmed", tr.Event)
			}

			var first time.Time
			for pass := 1; pass < threshold; pass++ {
				tr := observe(e, c, true, 1, threshold)
				if pass == 1 {
					first = tr.At
				}
				if tr.To != StatusRecovering || tr.Event != EventNone || tr.Notify {
					t.Fatalf("pass %d: to=%q event=%q notify=%v, want recovering, no event, silent", pass, tr.To, tr.Event, tr.Notify)
				}
				if tr.ConsecutiveOKs != pass || tr.RecoveryThreshold != threshold {
					t.Errorf("pass %d: oks=%d threshold=%d, want %d, %d", pass, tr.ConsecutiveOKs, tr.RecoveryThreshold, pass, threshold)
				}
			}

			tr := observe(e, c, true, 1, threshold)
			if threshold == 1 {
				first = tr.At
			}
			if tr.To != StatusUp || tr.Event != EventIncidentResolved || !tr.Notify {
				t.Fatalf("pass %d: to=%q event=%q notify=%v, want up, resolved, notify", threshold, tr.To, tr.Event, tr.Notify)
			}
			if !tr.RecoveredAt.Equal(first) {
				t.Errorf("recovered at %v, want the first pass at %v", tr.RecoveredAt, first)
			}
		})
	}
}

// Recovery while flapping suppression is active. A recovering pass has no
// notification to hold back and must not end the suppression; the resolve at
// the end of the streak is held back like any other flip; and a failure that
// breaks the streak neither alerts nor counts as a flip.
func TestRecoveryWhileFlapping(t *testing.T) {
	type step struct {
		name                         string
		ok                           bool
		to                           Status
		event                        Event
		notify, suppressed, flapping bool
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "enter recovery and resolve",
			steps: []step{
				{"pass enters recovery", true, StatusRecovering, EventNone, false, false, true},
				{"pass meets the threshold", true, StatusUp, EventIncidentResolved, false, true, true},
			},
		},
		{
			name: "enter recovery and fall back",
			steps: []step{
				{"pass enters recovery", true, StatusRecovering, EventNone, false, false, true},
				{"failure leaves recovery", false, StatusDown, EventNone, false, false, true},
				{"pass starts a new streak", true, StatusRecovering, EventNone, false, false, true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClock()
			e := New(Options{Now: c.Now, FlapWindow: time.Hour, FlapThreshold: 3})

			// Down, resolved, down again: the third flip trips flapping and
			// leaves the monitor down with a confirmed incident.
			observe(e, c, false, 1, 2)
			observe(e, c, true, 1, 2)
			observe(e, c, true, 1, 2)
			if tr := observe(e, c, false, 1, 2); tr.To != StatusDown || !tr.Flapping {
				t.Fatalf("setup: to=%q flapping=%v, want down and flapping", tr.To, tr.Flapping)
			}
			flips := len(e.state[1].changes)

			for _, s := range tc.steps {
				tr := observe(e, c, s.ok, 1, 2)
				if tr.To != s.to || tr.Event != s.event || tr.Notify != s.notify || tr.Suppressed != s.suppressed || tr.Flapping != s.flapping {
					t.Fatalf("%s: to=%q event=%q notify=%v suppressed=%v flapping=%v, want %q, %q, %v, %v, %v",
						s.name, tr.To, tr.Event, tr.Notify, tr.Suppressed, tr.Flapping, s.to, s.event, s.notify, s.suppressed, s.flapping)
				}
				if s.event == EventNone && len(e.state[1].changes) != flips {
					t.Errorf("%s: flips = %d, want %d: entering or leaving recovery is not a flip", s.name, len(e.state[1].changes), flips)
				}
				flips = len(e.state[1].changes)
			}
		})
	}
}
