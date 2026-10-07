package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func domainMonitorFor(t *testing.T, db *store.DB) store.Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "example.com registration", Type: store.TypeDomain, Target: "example.com",
		IntervalS: 86400, TimeoutS: 10, Enabled: true, DomainWarnDays: 30,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

func domainOutcome(m store.Monitor, ts time.Time, res checker.Result) scheduler.Outcome {
	res.CheckedAt = ts
	return scheduler.Outcome{Monitor: toCheckerMonitor(m), Result: res}
}

func unknownResult(reason string) checker.Result {
	return checker.Result{Kind: checker.FailUnknown, Error: reason}
}

// A check that could not find out leaves nothing behind but its reason: no
// heartbeat (so no bar and no dent in uptime), no observation (so no incident
// opened, confirmed or resolved), and one live frame that says "unknown".
func TestAnUnknownDomainCheckIsNeitherUpNorDown(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	bus := events.NewBus(16)
	sub := bus.Subscribe()
	defer sub.Close()
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record, Bus: bus})
	m := domainMonitorFor(t, db)
	now := time.Now().Truncate(time.Second)

	for i := range 3 {
		if err := r.recordOutcome(domainOutcome(m, now.Add(time.Duration(i)*time.Minute),
			unknownResult("the .nl registry publishes no RDAP service"))); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.LatestHeartbeat(ctx, m.ID); err == nil {
		t.Error("an unknown check was stored as a heartbeat")
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("an unknown check opened an incident")
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("alerts = %v, want none", got)
	}
	if got := r.Engine().Status(m.ID); got != state.StatusUnknown {
		t.Errorf("engine status = %q, want it never to have observed the check", got)
	}
	c, found, err := db.LatestUnknownCheck(ctx, m.ID)
	if err != nil || !found || c.Reason != "the .nl registry publishes no RDAP service" || !c.At.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("unknown check = %+v %v %v", c, found, err)
	}
	if got := r.ChecksCompleted(); got != 3 {
		t.Errorf("ChecksCompleted = %d, want 3: the pipeline did its job", got)
	}

	select {
	case e := <-sub.C():
		p, ok := e.Payload.(heartbeatPayload)
		if e.Kind != events.KindHeartbeat || !ok || !p.Unknown || p.OK || p.Assessment != "" || p.Error == "" {
			t.Errorf("live frame = %+v %+v, want an unknown frame with its reason", e, p)
		}
	default:
		t.Error("no live frame for the unknown check")
	}

	// The next check that finds out clears it.
	if err := r.recordOutcome(domainOutcome(m, now.Add(time.Hour), checker.Result{OK: true})); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.LatestUnknownCheck(ctx, m.ID); found {
		t.Error("the unknown check outlived a check that found out")
	}
}

// An unknown check during an open notice neither resolves it nor confirms
// anything: the registry being slow today says nothing about the date.
func TestAnUnknownDomainCheckLeavesTheNoticeOpen(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m := domainMonitorFor(t, db)
	now := time.Now().Truncate(time.Second)

	expiring := checker.Result{OK: true, Expiring: true, Kind: checker.FailDomainExpiry,
		Error: "domain registration of example.com expires in 12 days (on 2026-10-19)"}
	if err := r.recordOutcome(domainOutcome(m, now, expiring)); err != nil {
		t.Fatal(err)
	}
	if err := r.recordOutcome(domainOutcome(m, now.Add(time.Minute), unknownResult("HTTP 429"))); err != nil {
		t.Fatal(err)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil || !inc.Notice || inc.Cause != string(checker.FailDomainExpiry) {
		t.Fatalf("incident = %+v (%v), want the domain notice still open", inc, err)
	}
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Errorf("alerts = %v, want only the notice", got)
	}
	if !rec.alerts[0].Incident.Notice || rec.alerts[0].Monitor.Type != store.TypeDomain {
		t.Errorf("alert = %+v, want a notice about a domain monitor", rec.alerts[0])
	}
}

// An expired registration is down on the first check: the date is read off
// the registry, so there is no blip to wait out, and the API's default of no
// retries for a domain monitor is what makes that so.
func TestAnExpiredDomainConfirmsAtOnce(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "lapsed", Type: store.TypeDomain, Target: "example.com",
		IntervalS: 86400, TimeoutS: 10, Enabled: true, DomainWarnDays: 30, Retries: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := checker.Result{Kind: checker.FailDomainExpiry, Error: "domain registration of example.com expired on 2026-10-01"}
	if err := r.recordOutcome(domainOutcome(m, time.Now(), expired)); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed || rec.alerts[0].Incident.Notice {
		t.Fatalf("alerts = %v, want one confirmed outage", got)
	}
}

// A domain monitor reaches the scheduler as a domain job with its threshold;
// the six-hour floor while down is the scheduler's
// (TestRecoveryCadenceKeepsATypesFloor).
func TestDomainJobsCarryTheirThreshold(t *testing.T) {
	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger()})
	domainMonitorFor(t, db)
	jobs, err := r.jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Monitor.Type != checker.TypeDomain || jobs[0].Monitor.DomainWarnDays != 30 {
		t.Fatalf("jobs = %+v, want the domain monitor with its threshold", jobs)
	}
}
