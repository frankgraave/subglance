package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	canary, err := connectivity.New(connectivity.Options{
		Targets:    []string{"canary-a:53", "canary-b:53"},
		Dial:       uplink.dial,
		OnRestored: restored.record,
	})
	if err != nil {
		t.Fatal(err)
	}
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
