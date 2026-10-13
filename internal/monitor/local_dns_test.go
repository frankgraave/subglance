package monitor

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// switchableResolver stands in for this host's resolver: while it is silent,
// every reference lookup gets no answer. No test here touches the network.
type switchableResolver struct {
	silent  atomic.Bool
	lookups atomic.Int64
}

func (r *switchableResolver) lookup(_ context.Context, name string) error {
	r.lookups.Add(1)
	if r.silent.Load() {
		return &net.DNSError{Err: "i/o timeout", Name: name, Server: "192.0.2.53:53", IsTimeout: true}
	}
	return nil
}

func newResolverRunner(t *testing.T, db *store.DB, uplink *switchableNet, resolver *switchableResolver,
	alerts *alertRecorder, restored *restoredLog) *Runner {
	t.Helper()
	canary, err := connectivity.New(connectivity.Options{
		Targets:            []string{"canary-a:53", "canary-b:53"},
		Names:              []string{"ref-a.example", "ref-b.example"},
		Dial:               uplink.dial,
		Lookup:             resolver.lookup,
		OnResolverRestored: restored.record,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{DB: db, Log: quietLogger(), Notify: alerts.record, Connectivity: canary})
}

// unansweredLookup is a check whose lookup got nothing back from this host's
// resolver, as the checker reports one.
func unansweredLookup(m store.Monitor) scheduler.Outcome {
	o := outcomeFor(m, m.Target, false)
	o.Result.StatusCode = 0
	o.Result.Kind = checker.FailDNS
	o.Result.Error = "DNS lookup timed out after 10s: no answer from 192.0.2.53"
	o.Result.ResolverFailed = true
	return o
}

func createMonitor(t *testing.T, db *store.DB, m store.Monitor) store.Monitor {
	t.Helper()
	m.Enabled, m.IntervalS = true, 60
	created, err := db.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// The ticket's first acceptance test: the uplink works and the resolver does
// not. Monitors on host names confirm nothing and lose no uptime, a monitor
// on an address is measured as usual, and one notice follows when the
// resolver answers again.
func TestSilentResolverConfirmsNothing(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	uplink := &switchableNet{}
	uplink.up.Store(true)
	resolver := &switchableResolver{}
	resolver.silent.Store(true)
	alerts := &alertRecorder{}
	restored := &restoredLog{}
	r := newResolverRunner(t, db, uplink, resolver, alerts, restored)

	var named []store.Monitor
	for i := range 3 {
		named = append(named, createMonitor(t, db, store.Monitor{Name: fmt.Sprintf("site%d", i), Type: "http",
			Target: fmt.Sprintf("https://site%d.example", i), Retries: 2}))
	}
	byAddress := createMonitor(t, db, store.Monitor{Name: "tcp", Type: "tcp", Target: "192.0.2.10:443", Retries: 2})

	for range 5 {
		for _, m := range named {
			r.record(unansweredLookup(m))
		}
		r.record(outcomeFor(byAddress, byAddress.Target, true))
	}

	if got := alerts.events(); len(got) != 0 {
		t.Fatalf("alerts while the resolver was silent: %v", got)
	}
	open, err := db.ListOpenIncidents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, inc := range open {
		if inc.Confirmed() {
			t.Fatalf("incident confirmed while the resolver was silent: %+v", inc)
		}
	}
	for _, m := range named {
		if s := r.Engine().Status(m.ID); s != state.StatusWarning {
			t.Fatalf("%s is %s, want warning", m.Name, s)
		}
		if stats, err := db.Uptime(ctx, m.ID, time.Hour); err != nil || stats.Down != 0 || stats.Total != 0 {
			t.Fatalf("%s lost uptime to the resolver's outage: %+v, %v", m.Name, stats, err)
		}
	}
	hbs, err := db.ListHeartbeats(ctx, named[0].ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var local int
	for _, hb := range hbs {
		if hb.FailureKind == state.CauseLocalDNS {
			local++
		}
	}
	if local == 0 {
		t.Fatalf("no heartbeat is filed under %s: %+v", state.CauseLocalDNS, hbs)
	}
	if s := r.Engine().Status(byAddress.ID); s != state.StatusUp {
		t.Fatalf("the monitor on an address is %s, want up", s)
	}
	// The uplink answered, so the connectivity endpoint must say resolver,
	// not offline.
	if _, offline := r.canary.OfflineSince(); offline {
		t.Fatal("a silent resolver was reported as the host being offline")
	}
	if _, down := r.canary.ResolverDownSince(); !down {
		t.Fatal("the resolver episode was not recorded")
	}

	resolver.silent.Store(false)
	r.canary.Sweep(ctx)
	if got := restored.count(); got != 1 {
		t.Fatalf("resolver restored notices = %d, want 1", got)
	}
}

// A healthy resolver saying a monitor's own name does not exist is a real
// outage (an expired domain): it confirms as before, and the resolver round
// is not even asked.
func TestNXDOMAINWithAHealthyResolverConfirms(t *testing.T) {
	db := testDB(t)
	uplink := &switchableNet{}
	uplink.up.Store(true)
	resolver := &switchableResolver{}
	resolver.silent.Store(true)
	alerts := &alertRecorder{}
	r := newResolverRunner(t, db, uplink, resolver, alerts, &restoredLog{})

	m := createMonitor(t, db, store.Monitor{Name: "expired", Type: "http", Target: "https://expired.example", Retries: 1})
	o := outcomeFor(m, m.Target, false)
	o.Result.StatusCode = 0
	o.Result.Kind = checker.FailDNS
	o.Result.Error = "DNS lookup failed: host expired.example not found"
	r.record(o)

	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if got := resolver.lookups.Load(); got != 0 {
		t.Fatalf("the resolver round looked up %d names for an NXDOMAIN", got)
	}
}

// A monitor on an address that cannot connect is that monitor's outage,
// silent resolver or not: no lookup failed, so the resolver explains nothing.
func TestConnectionFailureIsNotPutDownToTheResolver(t *testing.T) {
	db := testDB(t)
	uplink := &switchableNet{}
	uplink.up.Store(true)
	resolver := &switchableResolver{}
	resolver.silent.Store(true)
	alerts := &alertRecorder{}
	r := newResolverRunner(t, db, uplink, resolver, alerts, &restoredLog{})

	m := createMonitor(t, db, store.Monitor{Name: "tcp", Type: "tcp", Target: "192.0.2.10:443", Retries: 1})
	r.record(failWith(m, checker.FailConnection))
	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if got := resolver.lookups.Load(); got != 0 {
		t.Fatalf("the resolver round looked up %d names for a connection failure", got)
	}
}

// One reference name answering is enough: the resolver works, and the
// monitor's own silence is its outage.
func TestOneAnsweringNameMeansTheMonitorIsDown(t *testing.T) {
	db := testDB(t)
	uplink := &switchableNet{}
	uplink.up.Store(true)
	resolver := &switchableResolver{}
	alerts := &alertRecorder{}
	r := newResolverRunner(t, db, uplink, resolver, alerts, &restoredLog{})

	m := createMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://site.example", Retries: 1})
	r.record(unansweredLookup(m))
	if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
		t.Fatalf("alerts = %v, want one confirmation", got)
	}
	if resolver.lookups.Load() == 0 {
		t.Fatal("the resolver round was not asked about an unanswered lookup")
	}
}

// With the uplink gone as well, the host is offline and is reported as that:
// local_network, not local_dns, and no resolver round is spent on it.
func TestOfflineHostIsNotReportedAsAResolverFault(t *testing.T) {
	db := testDB(t)
	uplink := &switchableNet{}
	resolver := &switchableResolver{}
	resolver.silent.Store(true)
	alerts := &alertRecorder{}
	r := newResolverRunner(t, db, uplink, resolver, alerts, &restoredLog{})

	m := createMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://site.example", Retries: 1})
	r.record(unansweredLookup(m))
	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hbs) != 1 || hbs[0].FailureKind != state.CauseLocalNetwork {
		t.Fatalf("heartbeats = %+v, want one filed under %s", hbs, state.CauseLocalNetwork)
	}
	if got := resolver.lookups.Load(); got != 0 {
		t.Fatalf("the resolver round looked up %d names with the host offline", got)
	}
}
