package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// switchableNet stands in for the host's uplink: while it is down, every
// canary dial fails. No test here touches the real network.
type switchableNet struct {
	up    atomic.Bool
	dials atomic.Int64
}

func (n *switchableNet) dial(context.Context, string, string) (net.Conn, error) {
	n.dials.Add(1)
	if !n.up.Load() {
		return nil, errors.New("network is unreachable")
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

// canaryClock is the canary's clock in these tests. It stands still unless a
// test moves it, so every failure a test records falls in one cache window.
// It starts two windows behind the wall clock, so a check stamped now is
// newer than any episode the canary starts.
type canaryClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *canaryClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *canaryClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// proveTargets runs the sweep a starting canary runs on a host that is online,
// so its targets have answered once, then puts the uplink back, moves the
// clock past that round's cache window and forgets its dials.
func proveTargets(t *testing.T, canary *connectivity.Canary, uplink *switchableNet, clk *canaryClock) {
	t.Helper()
	was := uplink.up.Load()
	uplink.up.Store(true)
	canary.Sweep(context.Background())
	uplink.up.Store(was)
	if _, _, unusable := canary.TargetsUnusable(); unusable {
		t.Fatal("reachable targets were found unusable")
	}
	clk.advance(connectivity.DefaultCacheFor)
	uplink.dials.Store(0)
}

type restoredLog struct {
	mu    sync.Mutex
	spans [][2]time.Time
}

func (l *restoredLog) record(from, to time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spans = append(l.spans, [2]time.Time{from, to})
}

func (l *restoredLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.spans)
}

func newCanaryRunner(t *testing.T, db *store.DB, uplink *switchableNet, alerts *alertRecorder, restored *restoredLog) *Runner {
	t.Helper()
	clk := &canaryClock{t: time.Now().Add(-2 * connectivity.DefaultCacheFor)}
	canary, err := connectivity.New(connectivity.Options{
		Targets:    []string{"canary-a:53", "canary-b:53"},
		Dial:       uplink.dial,
		Now:        clk.now,
		OnRestored: restored.record,
	})
	if err != nil {
		t.Fatal(err)
	}
	proveTargets(t, canary, uplink, clk)
	return New(Options{DB: db, Log: quietLogger(), Notify: alerts.record, Connectivity: canary})
}

func failWith(m store.Monitor, kind checker.FailureKind) scheduler.Outcome {
	o := outcomeFor(m, m.Target, false)
	o.Result.StatusCode = 0
	o.Result.Kind = kind
	o.Result.Error = "dial tcp: network is unreachable"
	if kind == checker.FailStatus {
		o.Result.StatusCode = 503
		o.Result.Error = "status 503, expected 2xx"
	}
	return o
}

// The ticket's first acceptance test: every outbound connection is gone.
// Twenty monitors fail on network errors for many rounds; not one incident
// is confirmed, not one alert goes out, no uptime is lost, and one notice
// follows when the connection returns.
func TestHostOfflineConfirmsNothing(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	restored := &restoredLog{}
	r := newCanaryRunner(t, db, uplink, alerts, restored)

	var monitors []store.Monitor
	for i := range 20 {
		m, err := db.CreateMonitor(ctx, store.Monitor{Name: fmt.Sprintf("m%d", i), Type: "http",
			Target: "https://example.invalid", Enabled: true, Retries: 2, IntervalS: 60})
		if err != nil {
			t.Fatal(err)
		}
		monitors = append(monitors, m)
	}

	kinds := []checker.FailureKind{checker.FailConnection, checker.FailDNS, checker.FailTimeout}
	for round := range 5 {
		for i, m := range monitors {
			r.record(failWith(m, kinds[(i+round)%len(kinds)]))
		}
	}

	if got := alerts.events(); len(got) != 0 {
		t.Fatalf("alerts while the host was offline: %v", got)
	}
	open, err := db.ListOpenIncidents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, inc := range open {
		if inc.Confirmed() {
			t.Fatalf("incident confirmed while the host was offline: %+v", inc)
		}
	}
	for _, m := range monitors {
		if s := r.Engine().Status(m.ID); s != state.StatusWarning {
			t.Fatalf("%s is %s, want warning", m.Name, s)
		}
		stats, err := db.Uptime(ctx, m.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Down != 0 || stats.Total != 0 {
			t.Fatalf("%s lost uptime to the host's outage: %+v", m.Name, stats)
		}
	}
	hbs, err := db.ListHeartbeats(ctx, monitors[0].ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var local int
	for _, hb := range hbs {
		if hb.FailureKind == state.CauseLocalNetwork {
			local++
		}
	}
	if local == 0 {
		t.Fatalf("no heartbeat is filed under %s: %+v", state.CauseLocalNetwork, hbs)
	}
	// One canary round per cache window, not one per monitor.
	if got := uplink.dials.Load(); got != 2 {
		t.Fatalf("canary dialled %d times for 100 failures inside one window, want one round of 2", got)
	}

	uplink.up.Store(true)
	r.canary.Sweep(ctx)
	if got := restored.count(); got != 1 {
		t.Fatalf("restored notices = %d, want 1", got)
	}
}

// The second acceptance test: one real target down, canary healthy. The
// behaviour is exactly as without a canary, and the canary is asked only on
// the failure that confirms.
func TestTargetDownWithHealthyHostConfirmsAsBefore(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	uplink.up.Store(true)
	alerts := &alertRecorder{}
	r := newCanaryRunner(t, db, uplink, alerts, &restoredLog{})

	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "down", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 3, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	r.record(failWith(m, checker.FailConnection))
	r.record(failWith(m, checker.FailConnection))
	if got := uplink.dials.Load(); got != 0 {
		t.Fatalf("canary dialled %d times before a failure could confirm", got)
	}
	r.record(failWith(m, checker.FailConnection))

	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if uplink.dials.Load() == 0 {
		t.Fatal("canary was not consulted on the confirming failure")
	}
}

// A status code proves the target answered, so it is never suppressed —
// even with the canary reporting the host offline — and the canary is not
// dialled for it at all.
func TestAnsweredFailureIsNeverSuppressed(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	r := newCanaryRunner(t, db, uplink, alerts, &restoredLog{})

	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "503", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 1, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	r.record(failWith(m, checker.FailStatus))
	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if got := uplink.dials.Load(); got != 0 {
		t.Fatalf("canary dialled %d times for a status failure", got)
	}
}

// With the check turned off, a host outage is reported as before.
func TestNoCanaryConfirmsNetworkFailures(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	alerts := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "off", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 1, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	r.record(failWith(m, checker.FailConnection))
	if got := alerts.events(); len(got) != 1 {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
}

// A restart while the host is offline restores the streak the engine held,
// not one inflated by the local-network failures on disk. Two real failures
// and four local-network ones must come back as a streak of two.
func TestRestartDoesNotCountLocalNetworkFailures(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	r := newCanaryRunner(t, db, uplink, alerts, &restoredLog{})
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "held", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 3, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		r.record(failWith(m, checker.FailConnection))
	}
	if got := alerts.events(); len(got) != 0 {
		t.Fatalf("alerts while offline: %v", got)
	}

	r = newCanaryRunner(t, db, uplink, alerts, &restoredLog{})
	if err := r.restore(ctx); err != nil {
		t.Fatal(err)
	}
	// A streak of two: one more failure reaches three, not seven.
	if r.Engine().WouldConfirm(m.ID, 7) {
		t.Fatal("restored streak counts the local-network failures")
	}
	if !r.Engine().WouldConfirm(m.ID, 3) {
		t.Fatal("restored streak lost the real failures")
	}
}

// newUnprovenCanaryRunner is newCanaryRunner on a host whose targets have
// never answered: what Run's first sweep finds in an internal Docker network
// or behind a firewall that drops outbound port 53.
func newUnprovenCanaryRunner(t *testing.T, db *store.DB, uplink *switchableNet, alerts *alertRecorder,
	restored *restoredLog) (*Runner, *canaryClock) {
	t.Helper()
	clk := &canaryClock{t: time.Now().Add(-2 * connectivity.DefaultCacheFor)}
	canary, err := connectivity.New(connectivity.Options{
		Targets:    []string{"canary-a:53", "canary-b:53"},
		Dial:       uplink.dial,
		Now:        clk.now,
		OnRestored: restored.record,
	})
	if err != nil {
		t.Fatal(err)
	}
	canary.Sweep(context.Background())
	return New(Options{DB: db, Log: quietLogger(), Notify: alerts.record, Connectivity: canary}), clk
}

// Setup A of SUB-333: the targets have never answered. A monitor on a closed
// port confirms after its retries and alerts, exactly as with the check off.
func TestTargetsThatNeverAnsweredDoNotSilenceAnOutage(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	r, _ := newUnprovenCanaryRunner(t, db, uplink, alerts, &restoredLog{})

	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "closed port", Type: "tcp",
		Target: "10.0.0.5:8080", Enabled: true, Retries: 2, IntervalS: 20})
	if err != nil {
		t.Fatal(err)
	}
	r.record(failWith(m, checker.FailConnection))
	r.record(failWith(m, checker.FailConnection))

	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if s := r.Engine().Status(m.ID); s != state.StatusDown {
		t.Fatalf("status = %s, want down", s)
	}
	hbs, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, hb := range hbs {
		if hb.FailureKind == state.CauseLocalNetwork {
			t.Fatalf("a heartbeat was filed under %s by targets that never answered: %+v", state.CauseLocalNetwork, hb)
		}
	}
}

// publicPass is a passing check that connected to a public address.
func publicPass(m store.Monitor, at time.Time) scheduler.Outcome {
	o := outcomeFor(m, m.Target, true)
	o.Result.CheckedAt = at
	o.Result.Peer = netip.MustParseAddr("93.184.215.14")
	return o
}

// Setup B of SUB-333: the targets answered once, then a firewall started
// dropping them. A check that reaches a public address ends the episode
// without a "connectivity restored" notice, and the next network failure of
// another monitor confirms as it would have without the canary.
func TestAPublicCheckEndsAnEpisodeTheTargetsCannotSee(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	restored := &restoredLog{}
	r := newCanaryRunner(t, db, uplink, alerts, restored)

	internal, err := db.CreateMonitor(ctx, store.Monitor{Name: "closed port", Type: "tcp",
		Target: "10.0.0.5:8080", Enabled: true, Retries: 1, IntervalS: 20})
	if err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateMonitor(ctx, store.Monitor{Name: "Internal app", Type: "http",
		Target: "https://example.com/", Enabled: true, Retries: 1, IntervalS: 20})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateMonitor(ctx, store.Monitor{Name: "other port", Type: "tcp",
		Target: "10.0.0.6:8080", Enabled: true, Retries: 1, IntervalS: 20})
	if err != nil {
		t.Fatal(err)
	}

	// The targets are dropped: the first failure is held back, as today.
	r.record(failWith(internal, checker.FailConnection))
	if got := alerts.events(); len(got) != 0 {
		t.Fatalf("alerts = %v, want the first failure held back", got)
	}
	if _, offline := r.canary.OfflineSince(); !offline {
		t.Fatal("the canary did not find the host offline")
	}

	// A check reaches the internet.
	r.record(publicPass(site, time.Now().Add(time.Second)))
	if _, offline := r.canary.OfflineSince(); offline {
		t.Fatal("the episode survived a check that reached a public address")
	}
	if got := restored.count(); got != 0 {
		t.Fatalf("restored notices = %d; the host was never offline", got)
	}

	r.record(failWith(other, checker.FailConnection))
	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want the next failure confirmed", got)
	}
}

// A check that connected to a private address is no evidence: when the
// uplink fails, the LAN keeps working.
func TestAPrivateCheckDoesNotEndAnEpisode(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	alerts := &alertRecorder{}
	r := newCanaryRunner(t, db, uplink, alerts, &restoredLog{})

	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "site", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 1, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	nas, err := db.CreateMonitor(ctx, store.Monitor{Name: "nas", Type: "http",
		Target: "http://192.168.1.10", Enabled: true, Retries: 1, IntervalS: 60})
	if err != nil {
		t.Fatal(err)
	}
	r.record(failWith(m, checker.FailConnection))
	dials := uplink.dials.Load()
	for _, addr := range []string{"192.168.1.10", "10.0.0.1", "100.64.0.1", "127.0.0.1", "fd00::1"} {
		o := outcomeFor(nas, nas.Target, true)
		o.Result.CheckedAt = time.Now().Add(time.Second)
		o.Result.Peer = netip.MustParseAddr(addr)
		r.record(o)
	}
	if _, offline := r.canary.OfflineSince(); !offline {
		t.Fatal("a check on a private address ended the offline episode")
	}
	if got := uplink.dials.Load(); got != dials {
		t.Fatalf("checks on private addresses dialled the targets %d times", got-dials)
	}
	if got := alerts.events(); len(got) != 0 {
		t.Fatalf("alerts = %v, want none while the host is offline", got)
	}
}
