package state

import (
	"testing"
	"time"
)

// check feeds one check through the engine: a pass, a pass whose certificate
// expires soon, or a failure. Failures confirm on the second one.
func check(e *Engine, c *clock, kind string) Transition {
	c.advance(time.Minute)
	o := Observation{
		MonitorID: 1, At: c.Now(), FailureThreshold: 2, RecoveryThreshold: 2,
	}
	switch kind {
	case "pass":
		o.OK = true
	case "expiring":
		o.OK, o.Expiring = true, true
		o.Kind, o.Error = "cert_expiry", "certificate expires in 6 days"
	case "fail":
		o.Kind, o.Error = "status", "unexpected status 500"
	}
	return e.Observe(o)
}

// The case from the ticket: a certificate that expires in 6 days with a
// 21-day threshold. The monitor is expiring, never down; it is said once
// when the notice opens and once when the certificate is renewed, however
// many checks see the same certificate in between.
func TestAnExpiringCertificateIsANoticeNotAnOutage(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	tr := check(e, c, "expiring")
	if tr.To != StatusExpiring || tr.Event != EventIncidentConfirmed || !tr.Notify || !tr.Notice {
		t.Fatalf("first expiring check: to %q event %q notify %v notice %v, want expiring, confirmed, notified, notice",
			tr.To, tr.Event, tr.Notify, tr.Notice)
	}
	if tr.To.Confirmed() {
		t.Error("expiring counts as a confirmed outage")
	}

	for i := range 30 {
		tr = check(e, c, "expiring")
		if tr.To != StatusExpiring || tr.Event != EventNone || tr.Notify {
			t.Fatalf("expiring check %d: to %q event %q notify %v, want expiring and silent", i, tr.To, tr.Event, tr.Notify)
		}
	}

	tr = check(e, c, "pass")
	if tr.To != StatusUp || tr.Event != EventIncidentResolved || !tr.Notify || !tr.Notice {
		t.Fatalf("renewed: to %q event %q notify %v notice %v, want up, resolved, notified, about the notice",
			tr.To, tr.Event, tr.Notify, tr.Notice)
	}
	if e.Flapping(1) {
		t.Error("a notice opening and closing counted as flapping")
	}
}

// A blip during a notice is a warning, as it would be on any monitor, and the
// notice survives it: the next expiring check is back to expiring without a
// second notice going out.
func TestABlipDuringANoticeKeepsTheNotice(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})
	check(e, c, "expiring")

	tr := check(e, c, "fail")
	if tr.To != StatusWarning || tr.Event != EventNone || tr.Notify || !tr.Notice {
		t.Fatalf("one failure: to %q event %q notify %v notice %v, want a silent warning against the notice",
			tr.To, tr.Event, tr.Notify, tr.Notice)
	}
	tr = check(e, c, "expiring")
	if tr.To != StatusExpiring || tr.Event != EventNone || tr.Notify {
		t.Fatalf("after the blip: to %q event %q notify %v, want expiring and silent", tr.To, tr.Event, tr.Notify)
	}
	// The streak was reset: one more failure is a blip again, not a
	// confirmation.
	if tr := check(e, c, "fail"); tr.Event != EventNone {
		t.Errorf("the blip's failure still counted after the notice resumed: event %q", tr.Event)
	}
}

// A real outage during a notice is an outage: confirmed at the threshold,
// alerted, and dated from its own first failure rather than from the notice.
func TestAnOutageDuringANoticeReplacesIt(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})
	check(e, c, "expiring")
	check(e, c, "expiring")

	first := check(e, c, "fail")
	tr := check(e, c, "fail")
	if tr.To != StatusDown || tr.Event != EventIncidentConfirmed || !tr.Notify {
		t.Fatalf("second failure: to %q event %q notify %v, want a confirmed outage", tr.To, tr.Event, tr.Notify)
	}
	if !tr.Replaces || !tr.StartedAt.Equal(first.At) || tr.Notice {
		t.Errorf("replaces %v started %v notice %v, want an outage replacing the notice, starting at the first failure %v",
			tr.Replaces, tr.StartedAt, tr.Notice, first.At)
	}

	// From here it is an ordinary outage. An expiring pass counts towards
	// recovery like any pass, and the recovery is an outage's, not a notice's.
	tr = check(e, c, "expiring")
	if tr.To != StatusRecovering {
		t.Fatalf("first pass of the recovery: to %q, want recovering", tr.To)
	}
	tr = check(e, c, "expiring")
	if tr.Event != EventIncidentResolved || tr.Notice || tr.To != StatusUp {
		t.Fatalf("second pass: to %q event %q notice %v, want the outage resolved", tr.To, tr.Event, tr.Notice)
	}
	// And the next observation of the same certificate opens the notice.
	tr = check(e, c, "expiring")
	if tr.To != StatusExpiring || tr.Event != EventIncidentConfirmed || !tr.Notice {
		t.Errorf("after the outage: to %q event %q notice %v, want the notice reopened", tr.To, tr.Event, tr.Notice)
	}
}

// A renewed certificate closes its notice on the first check that sees it.
// The recovery threshold guards against a half-broken service's lucky pass,
// and a new expiry date is not luck.
func TestARenewalDoesNotWaitForTheRecoveryThreshold(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})
	check(e, c, "expiring")
	if tr := check(e, c, "pass"); tr.Event != EventIncidentResolved || tr.To != StatusUp {
		t.Errorf("first renewed check: to %q event %q, want resolved at once", tr.To, tr.Event)
	}
}

// A notice restored at startup is still a notice: no streak, no second alert.
func TestARestoredNoticeStaysANotice(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})
	e.Restore(1, RestoredState{Status: StatusExpiring, IncidentOpen: true, IncidentConfirmed: true, Notice: true})

	if tr := check(e, c, "expiring"); tr.Event != EventNone || tr.To != StatusExpiring || tr.Notify {
		t.Fatalf("after restore: to %q event %q notify %v, want a silent expiring check", tr.To, tr.Event, tr.Notify)
	}
	if tr := check(e, c, "pass"); tr.Event != EventIncidentResolved || !tr.Notice {
		t.Errorf("renewal after restore: event %q notice %v, want the notice resolved", tr.Event, tr.Notice)
	}
}

// Expiring only means something on a pass. A failed check is a failure,
// whatever its certificate says.
func TestExpiringIsIgnoredOnAFailure(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})
	c.advance(time.Minute)
	tr := e.Observe(Observation{MonitorID: 1, At: c.Now(), Expiring: true, Kind: "status", FailureThreshold: 1})
	if tr.To != StatusDown || tr.Notice {
		t.Errorf("a failure marked expiring: to %q notice %v, want an ordinary outage", tr.To, tr.Notice)
	}
}
