package state

import (
	"testing"
	"time"
)

func TestWarningTransitions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold int
		oks       []bool
		want      []Status
		notifies  []bool
	}{
		{"unknown-warning-up", 2, []bool{false, true}, []Status{"warning", StatusUp}, []bool{false, false}},
		{"up-warning-down-up", 3, []bool{true, false, false, false, true}, []Status{StatusUp, "warning", "warning", StatusDown, StatusUp}, []bool{false, false, false, true, true}},
		{"immediate", 1, []bool{false, true}, []Status{StatusDown, StatusUp}, []bool{true, true}},
		{"warning-oscillation", 2, []bool{false, true, false, true, false, true, false, true}, []Status{"warning", StatusUp, "warning", StatusUp, "warning", StatusUp, "warning", StatusUp}, []bool{false, false, false, false, false, false, false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(Options{})
			at := time.Now()
			for i, ok := range tc.oks {
				tr := e.Observe(Observation{MonitorID: 1, OK: ok, At: at.Add(time.Duration(i) * time.Second), FailureThreshold: tc.threshold})
				if tr.To != tc.want[i] || tr.Notify != tc.notifies[i] || tr.Flapping {
					t.Fatalf("step %d: %+v", i, tr)
				}
			}
		})
	}
}

func TestWarningDoesNotNotifyWhenFlappingExpires(t *testing.T) {
	e := New(Options{FlapThreshold: 2, FlapWindow: time.Minute})
	at := time.Now()
	for i, ok := range []bool{false, true} {
		e.Observe(Observation{MonitorID: 1, OK: ok, At: at.Add(time.Duration(i) * time.Second), FailureThreshold: 1})
	}
	tr := e.Observe(Observation{MonitorID: 1, OK: false, At: at.Add(2 * time.Minute), FailureThreshold: 2})
	if tr.To != "warning" || tr.Notify {
		t.Fatalf("warning at flap expiry alerted: %+v", tr)
	}
}

func TestWarningConfirmationAtFlappingExpiryStillAlerts(t *testing.T) {
	e := New(Options{FlapThreshold: 2, FlapWindow: time.Minute})
	at := time.Now()
	for i, ok := range []bool{false, true, false} {
		threshold := 1
		if i == 2 {
			threshold = 2
		}
		e.Observe(Observation{MonitorID: 1, OK: ok, At: at.Add(time.Duration(i) * time.Second), FailureThreshold: threshold})
	}
	tr := e.Observe(Observation{MonitorID: 1, OK: false, At: at.Add(2 * time.Minute), FailureThreshold: 2})
	if tr.To != StatusDown || !tr.Notify {
		t.Fatalf("confirmation at flap expiry did not alert: %+v", tr)
	}
}
