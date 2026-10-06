package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// expiringOutcome is a passing ssl check whose certificate expires in six
// days, against a monitor whose threshold is 21: the case from the demo.
func expiringOutcome(m store.Monitor, ts time.Time) scheduler.Outcome {
	return scheduler.Outcome{Monitor: toCheckerMonitor(m), Result: checker.Result{
		OK: true, Expiring: true, Latency: 5 * time.Millisecond, CheckedAt: ts,
		Kind:  checker.FailCertExpiry,
		Error: "certificate expires in 6 days (on 2026-10-12)",
	}}
}

func expiringMonitor(t *testing.T, db *store.DB) store.Monitor {
	t.Helper()
	return expiringMonitorRecovering(t, db, 0)
}

// expiringMonitorRecovering is expiringMonitor with a recovery threshold;
// zero takes the schema default.
func expiringMonitorRecovering(t *testing.T, db *store.DB, recovery int) store.Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "TLS api", Type: "ssl", Target: "api.example.com:443",
		IntervalS: 60, TimeoutS: 5, Retries: 2, RecoveryThreshold: recovery,
		SSLWarnDays: 21, Enabled: true, RepeatAfterS: 60,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

// End to end: the notice opens one incident marked as a notice and sends one
// alert, the checks are stored as up so uptime stays at 100%, and a renewed
// certificate resolves it with one more message.
func TestAnExpiringCertificateAlertsWithoutDowntime(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m := expiringMonitor(t, db)

	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := range 5 {
		if err := r.recordOutcome(expiringOutcome(m, start.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}

	if got := r.Engine().Status(m.ID); got != state.StatusExpiring {
		t.Errorf("status = %q, want expiring", got)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("no open incident for the notice: %v", err)
	}
	if !inc.Notice || !inc.Confirmed() || inc.Cause != string(checker.FailCertExpiry) {
		t.Errorf("incident = %+v, want a confirmed notice with cause cert_expiry", inc)
	}
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed || !rec.alerts[0].Incident.Notice {
		t.Fatalf("alerts = %v, want exactly one, about the notice", got)
	}

	stats, err := db.Uptime(ctx, m.ID, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Up != 5 || stats.Down != 0 || stats.Percentage != 100 {
		t.Errorf("uptime = %+v, want five checks up and 100%%", stats)
	}
	hb, err := db.LatestHeartbeat(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hb.OK || hb.Assessment != "up" || hb.Error != "" || hb.FailureKind != "" {
		t.Errorf("heartbeat = %+v, want a plain pass stored as up", hb)
	}

	renewed := outcomeFor(m, m.Target, true)
	renewed.Result.CheckedAt = start.Add(10 * time.Minute)
	if err := r.recordOutcome(renewed); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 2 || got[1] != state.EventIncidentResolved || !rec.alerts[1].Incident.Notice {
		t.Fatalf("alerts after renewal = %v, want the notice resolved", got)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("the notice is still open after renewal")
	}
}

// The 7- and 30-day figures read the hourly rollup once raw heartbeats age
// out, so a notice has to count as up there as well, not only in the raw
// rows the 24-hour figure reads.
func TestAnExpiringCertificateIsUpInTheRollups(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger(), Notify: (&alertRecorder{}).record})
	m := expiringMonitor(t, db)

	start := time.Now().Add(-72 * time.Hour).Truncate(time.Hour)
	for i := range 5 {
		if err := r.recordOutcome(expiringOutcome(m, start.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	rolled, err := db.RollupHeartbeats(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Heartbeats != 5 {
		t.Fatalf("rolled up %d heartbeats, want all 5", rolled.Heartbeats)
	}
	for _, window := range []time.Duration{7 * 24 * time.Hour, 30 * 24 * time.Hour} {
		stats, err := db.Uptime(ctx, m.ID, window)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Up != 5 || stats.Down != 0 || stats.Percentage != 100 {
			t.Errorf("uptime over %v = %+v, want five checks up and 100%%", window, stats)
		}
	}
}

// A notice is restored as a notice across a restart: no second alert, still
// expiring, and the first failure afterwards is a warning rather than an
// outage confirmed from a streak counted over the notice's lifetime. The blip
// before the restart was ended by a pass, so it is no streak.
func TestANoticeSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	m := expiringMonitor(t, db)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)

	first := New(Options{DB: db, Log: quietLogger()})
	if err := first.recordOutcome(expiringOutcome(m, start)); err != nil {
		t.Fatal(err)
	}
	// A blip during the notice leaves a failed heartbeat behind it.
	if err := first.recordOutcome(at(outcomeFor(m, m.Target, false), start.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := first.recordOutcome(expiringOutcome(m, start.Add(90*time.Second))); err != nil {
		t.Fatal(err)
	}

	rec := &alertRecorder{}
	second := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	if err := second.restore(ctx); err != nil {
		t.Fatal(err)
	}
	if got := second.Engine().Status(m.ID); got != state.StatusExpiring {
		t.Errorf("restored status = %q, want expiring", got)
	}
	if err := second.recordOutcome(at(outcomeFor(m, m.Target, false), start.Add(2*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("alerts after one failure post-restart = %v, want none: that is a blip", got)
	}
	if err := second.recordOutcome(expiringOutcome(m, start.Add(3*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("alerts = %v, want none: the notice was already announced", got)
	}
}

// A restart in the middle of a streak against a notice keeps that streak:
// the failures after the latest passing check count, and an older blip that a
// pass already ended does not.
func TestANoticeRestoresAStreakInProgress(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	m := expiringMonitor(t, db)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	failedAt := start.Add(3 * time.Minute)

	first := New(Options{DB: db, Log: quietLogger()})
	for _, o := range []scheduler.Outcome{
		expiringOutcome(m, start),
		at(outcomeFor(m, m.Target, false), start.Add(time.Minute)),
		expiringOutcome(m, start.Add(2*time.Minute)),
		at(outcomeFor(m, m.Target, false), failedAt),
	} {
		if err := first.recordOutcome(o); err != nil {
			t.Fatal(err)
		}
	}

	rec := &alertRecorder{}
	second := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	if err := second.restore(ctx); err != nil {
		t.Fatal(err)
	}
	if got := second.Engine().Status(m.ID); got != state.StatusWarning {
		t.Errorf("restored status = %q, want warning: a streak was running", got)
	}
	if err := second.recordOutcome(at(outcomeFor(m, m.Target, false), start.Add(4*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed || rec.alerts[0].Incident.Notice {
		t.Fatalf("alerts = %v, want the outage confirmed on the second failure of the streak", got)
	}
	outage, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outage.Notice || !outage.StartedAt.Equal(failedAt.UTC()) {
		t.Errorf("outage = %+v, want an outage starting at the streak's first failure %v", outage, failedAt.UTC())
	}
}

// An expiring pass streams the notice's text and cause, so a live row that
// showed a blip against the notice gets the notice back. The stored
// heartbeat stays a plain pass.
func TestAnExpiringHeartbeatCarriesTheNotice(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	bus := events.NewBus(8)
	sub := bus.Subscribe()
	defer sub.Close()
	r := New(Options{DB: db, Log: quietLogger(), Bus: bus})
	m := expiringMonitor(t, db)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)

	frame := func(o scheduler.Outcome) map[string]any {
		t.Helper()
		if err := r.recordOutcome(o); err != nil {
			t.Fatal(err)
		}
		for {
			e := <-sub.C()
			if e.Kind != events.KindHeartbeat {
				continue
			}
			data, err := json.Marshal(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			return payload
		}
	}

	frame(expiringOutcome(m, start))
	if got := frame(at(outcomeFor(m, m.Target, false), start.Add(time.Minute))); got["expiring"] != nil {
		t.Errorf("a failing check carries expiring %v", got["expiring"])
	}
	got := frame(expiringOutcome(m, start.Add(2*time.Minute)))
	if got["expiring"] != true || got["assessment"] != "up" ||
		got["error"] != "certificate expires in 6 days (on 2026-10-12)" ||
		got["failure_kind"] != string(checker.FailCertExpiry) {
		t.Errorf("expiring frame = %v, want expiring, assessed up, with the notice's text and cause", got)
	}
	hb, err := db.LatestHeartbeat(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hb.Error != "" || hb.FailureKind != "" {
		t.Errorf("heartbeat = %+v, want a plain pass with no error stored", hb)
	}
}

// An outage during a notice is its own incident: the notice ends where the
// outage began, the outage is confirmed and alerted as down, and it is
// charged to uptime like any outage.
func TestAnOutageDuringANoticeIsItsOwnIncident(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m := expiringMonitor(t, db)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)

	if err := r.recordOutcome(expiringOutcome(m, start)); err != nil {
		t.Fatal(err)
	}
	failedAt := start.Add(time.Minute)
	for i := range 2 {
		if err := r.recordOutcome(at(outcomeFor(m, m.Target, false), failedAt.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}

	incs, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(incs) != 2 {
		t.Fatalf("incidents = %+v, want the notice and the outage", incs)
	}
	outage, notice := incs[0], incs[1]
	if outage.Notice || !outage.Confirmed() || outage.Resolved() || !outage.StartedAt.Equal(failedAt.UTC()) {
		t.Errorf("outage = %+v, want an open confirmed outage starting at %v", outage, failedAt.UTC())
	}
	if !notice.Notice || !notice.ResolvedAt.Equal(failedAt.UTC()) {
		t.Errorf("notice = %+v, want it closed where the outage began", notice)
	}
	if got := rec.events(); len(got) != 2 || got[1] != state.EventIncidentConfirmed || rec.alerts[1].Incident.Notice {
		t.Errorf("alerts = %v, want the notice and then the outage", got)
	}
}

// A check that ends an outage while its certificate is expiring says both
// things, in order: the outage is over, and then the notice.
func TestAnOutageThatEndsOnAnExpiringCertificateOpensTheNotice(t *testing.T) {
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record})
	m := expiringMonitorRecovering(t, db, 1)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)

	for i := range 2 {
		if err := r.recordOutcome(at(outcomeFor(m, m.Target, false), start.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.recordOutcome(expiringOutcome(m, start.Add(2*time.Minute))); err != nil {
		t.Fatal(err)
	}

	got := rec.events()
	want := []state.Event{state.EventIncidentConfirmed, state.EventIncidentResolved, state.EventIncidentConfirmed}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || !rec.alerts[2].Incident.Notice || rec.alerts[1].Incident.Notice {
		t.Fatalf("alerts = %v, want down, back up, then the notice", got)
	}
	if st := r.Engine().Status(m.ID); st != state.StatusExpiring {
		t.Errorf("status = %q, want expiring", st)
	}
}
