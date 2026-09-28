package state

import (
	"testing"
	"time"
)

// A failure the host's own network explains holds the monitor at warning:
// no incident, no alert, no streak, however many of them arrive.
func TestLocalNetworkFailureNeverConfirms(t *testing.T) {
	e := New(Options{})
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Two ordinary failures take a threshold-3 monitor to one short.
	for i := range 2 {
		tr := e.Observe(Observation{MonitorID: 1, At: at.Add(time.Duration(i) * time.Minute),
			Kind: "connection", FailureThreshold: 3})
		if tr.To != StatusWarning || tr.Notify {
			t.Fatalf("failure %d: %+v", i, tr)
		}
	}
	if !e.WouldConfirm(1, 3) {
		t.Fatal("WouldConfirm = false one failure short of the threshold")
	}

	for i := range 10 {
		tr := e.Observe(Observation{MonitorID: 1, At: at.Add(time.Duration(2+i) * time.Minute),
			Kind: "connection", FailureThreshold: 3, LocalNetwork: true})
		if tr.To != StatusWarning || tr.Event != EventNone || tr.Notify || tr.Suppressed {
			t.Fatalf("local-network failure %d: %+v", i, tr)
		}
		if tr.Cause != CauseLocalNetwork {
			t.Fatalf("cause = %q, want %q", tr.Cause, CauseLocalNetwork)
		}
		if tr.ConsecutiveFails != 2 {
			t.Fatalf("streak moved to %d on a local-network failure", tr.ConsecutiveFails)
		}
	}

	// Back online and the target is still failing: the held streak
	// confirms on the next real failure, as it would have without the
	// interruption.
	tr := e.Observe(Observation{MonitorID: 1, At: at.Add(20 * time.Minute),
		Kind: "connection", FailureThreshold: 3})
	if tr.To != StatusDown || tr.Event != EventIncidentConfirmed || !tr.Notify {
		t.Fatalf("first real failure after reconnecting: %+v", tr)
	}
}

// A local-network failure on a monitor with no history opens nothing.
func TestLocalNetworkFailureOpensNoIncident(t *testing.T) {
	e := New(Options{})
	tr := e.Observe(Observation{MonitorID: 1, Kind: "dns", FailureThreshold: 1, LocalNetwork: true})
	if tr.To != StatusWarning || tr.Event != EventNone || tr.Notify {
		t.Fatalf("got %+v", tr)
	}
	// And a pass afterwards is a quiet return to up, not a "resolved".
	tr = e.Observe(Observation{MonitorID: 1, OK: true})
	if tr.To != StatusUp || tr.Event != EventNone || tr.Notify {
		t.Fatalf("pass after a local-network warning: %+v", tr)
	}
}

// A monitor confirmed down before the host went offline stays down: the
// canary does not retract an incident announced while the host could see.
func TestLocalNetworkDoesNotRetractAConfirmedIncident(t *testing.T) {
	e := New(Options{})
	e.Observe(Observation{MonitorID: 1, Kind: "status", FailureThreshold: 1})
	if e.WouldConfirm(1, 1) {
		t.Fatal("WouldConfirm = true for a monitor that is already down")
	}
	tr := e.Observe(Observation{MonitorID: 1, Kind: "connection", FailureThreshold: 1, LocalNetwork: true})
	if tr.To != StatusDown || tr.Cause != "connection" {
		t.Fatalf("confirmed monitor after a local-network failure: %+v", tr)
	}
}

func TestWouldConfirm(t *testing.T) {
	e := New(Options{})
	if !e.WouldConfirm(1, 1) || !e.WouldConfirm(1, 0) {
		t.Fatal("an unseen monitor with threshold 1 confirms on its first failure")
	}
	if e.WouldConfirm(1, 2) {
		t.Fatal("an unseen monitor with threshold 2 does not confirm on its first failure")
	}
	e.Observe(Observation{MonitorID: 1, Kind: "timeout", FailureThreshold: 2})
	if !e.WouldConfirm(1, 2) {
		t.Fatal("one failure into a threshold of 2, the next one confirms")
	}
}

// Every status a local-network failure can meet, at the thresholds where the
// boundary sits. Recovering is the edge that matters: Confirmed() is true
// there, so the canary must not be consulted and the failure must take the
// ordinary path back to down — the same outage, no new alert.
func TestLocalNetworkTransitions(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fail := func(e *Engine, threshold int, local bool) Transition {
		at = at.Add(time.Minute)
		return e.Observe(Observation{MonitorID: 1, At: at, Kind: "connection",
			FailureThreshold: threshold, RecoveryThreshold: 2, LocalNetwork: local})
	}
	pass := func(e *Engine) {
		at = at.Add(time.Minute)
		e.Observe(Observation{MonitorID: 1, At: at, OK: true, RecoveryThreshold: 2})
	}

	tests := []struct {
		name      string
		threshold int
		setup     func(e *Engine, threshold int)
		// before the local-network failure
		wantWouldConfirm bool
		// after it
		wantTo    Status
		wantCause string
		wantFails int
	}{
		{"unknown, threshold 0", 0, func(*Engine, int) {}, true, StatusWarning, CauseLocalNetwork, 0},
		{"unknown, threshold 1", 1, func(*Engine, int) {}, true, StatusWarning, CauseLocalNetwork, 0},
		{"warning at threshold-1, threshold 0", 0,
			func(e *Engine, n int) { fail(e, n, true) }, true, StatusWarning, CauseLocalNetwork, 0},
		{"warning at threshold-1, threshold 1", 1,
			func(e *Engine, n int) { fail(e, n, true) }, true, StatusWarning, CauseLocalNetwork, 0},
		{"warning at threshold-1, threshold 2", 2,
			func(e *Engine, n int) { fail(e, n, false) }, true, StatusWarning, CauseLocalNetwork, 1},
		{"down, threshold 0", 0,
			func(e *Engine, n int) { fail(e, n, false) }, false, StatusDown, "connection", 2},
		{"down, threshold 1", 1,
			func(e *Engine, n int) { fail(e, n, false) }, false, StatusDown, "connection", 2},
		{"recovering, threshold 0", 0,
			func(e *Engine, n int) { fail(e, n, false); pass(e) }, false, StatusDown, "connection", 1},
		{"recovering, threshold 1", 1,
			func(e *Engine, n int) { fail(e, n, false); pass(e) }, false, StatusDown, "connection", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New(Options{})
			tc.setup(e, tc.threshold)
			if got := e.WouldConfirm(1, tc.threshold); got != tc.wantWouldConfirm {
				t.Fatalf("WouldConfirm = %v, want %v", got, tc.wantWouldConfirm)
			}
			tr := fail(e, tc.threshold, true)
			if tr.To != tc.wantTo || tr.Cause != tc.wantCause {
				t.Fatalf("to %s cause %q, want %s %q: %+v", tr.To, tr.Cause, tc.wantTo, tc.wantCause, tr)
			}
			if tr.Event != EventNone || tr.Notify {
				t.Fatalf("a local-network failure announced something: %+v", tr)
			}
			if tr.ConsecutiveFails != tc.wantFails {
				t.Fatalf("streak = %d, want %d", tr.ConsecutiveFails, tc.wantFails)
			}
			if tr.ConsecutiveOKs != 0 {
				t.Fatalf("passing streak survived a failure: %d", tr.ConsecutiveOKs)
			}
		})
	}
}
