package scheduler

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// The sizing rule, checked at counts nobody wants to instantiate.
//
// The case that matters is the ticket's: 200 monitors on a 2-vCPU box used to
// get 8 workers, because the figure came from CPU count alone. During a broad
// outage every check holds its worker for the full timeout, so 8 workers clear
// roughly 48 checks a minute against 200 due — the queue backs up and
// detection latency for the still-healthy monitors blows out to minutes.
func TestAutoWorkerCountFloorsOnMonitorCount(t *testing.T) {
	cases := []struct {
		name      string
		procs     int
		scheduled int
		want      int
	}{
		{"idle instance keeps the floor", 2, 0, minWorkers},
		{"a handful of monitors keeps the floor", 2, 10, minWorkers},
		{"cpu still wins on a big box with few monitors", 16, 4, 64},
		{"200 monitors on 2 vCPU", 2, 200, 50},
		{"500 monitors on 2 vCPU", 2, 500, 125},
		{"the ceiling holds", 2, 100_000, maxWorkers},
		{"the ceiling holds on a huge box too", 64, 100_000, maxWorkers},
		{"rounding up, not down", 2, 5, minWorkers},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoWorkerCount(tc.procs, tc.scheduled); got != tc.want {
				t.Errorf("autoWorkerCount(procs=%d, scheduled=%d) = %d, want %d",
					tc.procs, tc.scheduled, got, tc.want)
			}
		})
	}
}

// The floor has to be bounded. An auto value that grows without limit is a
// different failure on the small VPS this product targets: every running check
// holds a connection, so an unbounded pool trades a slow queue for an OOM.
func TestAutoWorkerCountIsBounded(t *testing.T) {
	for _, scheduled := range []int{0, 1, 1_000, 1_000_000} {
		got := autoWorkerCount(runtime.GOMAXPROCS(0), scheduled)
		if got < minWorkers || got > maxWorkers {
			t.Errorf("autoWorkerCount(scheduled=%d) = %d, outside [%d, %d]",
				scheduled, got, minWorkers, maxWorkers)
		}
	}
}

// The rule has to be applied to a running scheduler, not just computed: the
// pool is started before the first reload, so a floor that only exists in New
// would never see the monitor set at all.
func TestAutoPoolGrowsForTheMonitorSet(t *testing.T) {
	fake := &fakeChecker{}

	var jobs []Job
	for i := int64(1); i <= 200; i++ {
		jobs = append(jobs, job(i, time.Hour))
	}

	s := New(Options{
		Registry:       staticRegistry(jobs...),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	// Intervals are an hour, so nothing is due: this test is about sizing,
	// not about dispatch.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()

	want := autoWorkerCount(runtime.GOMAXPROCS(0), len(jobs))
	waitFor(t, 2*time.Second, func() bool { return s.Workers() >= want })

	if got := s.Workers(); got != want {
		t.Errorf("pool is %d workers for %d monitors, want %d — "+
			"the auto size ignored the monitor count",
			got, len(jobs), want)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation")
	}
}

// An operator who named a number owns it. Growing past --check-workers would
// make the flag a suggestion, and the reason someone sets it is usually a
// resource limit the scheduler cannot see.
func TestExplicitWorkerCountIsNeverExceeded(t *testing.T) {
	fake := &fakeChecker{}

	var jobs []Job
	for i := int64(1); i <= 200; i++ {
		jobs = append(jobs, job(i, time.Hour))
	}

	s := New(Options{
		Registry:       staticRegistry(jobs...),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		Workers:        3,
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()

	// Give reload every chance to have grown the pool behind our back.
	waitFor(t, 2*time.Second, func() bool { return s.Size() == len(jobs) })
	if got := s.Workers(); got != 3 {
		t.Errorf("pool is %d workers, but --check-workers named 3", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation")
	}
}

// The skip counter is the saturation signal, and it has to count rather than
// only log: at default level the per-skip warning is invisible.
//
// The check is held open by a channel rather than by a sleep, so the ordering
// this depends on — a check still running when its next turn comes round — is
// established by the test rather than hoped for from the host scheduler.
func TestSkippedChecksAreCounted(t *testing.T) {
	gate := &gatedChecker{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	s := New(Options{
		Registry:       staticRegistry(job(1, 20*time.Millisecond)),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: gate},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()

	// The first check is now running and cannot finish, so every turn after
	// this one finds it in flight.
	<-gate.started

	waitForOr(t, 5*time.Second, func() bool { return s.SkippedChecks() > 0 },
		"a check that outran its interval was skipped but not counted, "+
			"so saturation is invisible to /metrics")

	close(gate.release)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}
}

// gatedChecker blocks every check until release is closed, and announces the
// first one on started. It is local to this file rather than a flag on
// fakeChecker because only these tests need a check that never finishes on its
// own, and a shared checker that can deadlock is a trap for the next test.
type gatedChecker struct {
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

func (g *gatedChecker) Check(ctx context.Context, _ checker.Monitor) checker.Result {
	g.startOnce.Do(func() { close(g.started) })
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return checker.Result{OK: true, StatusCode: 200, Latency: time.Millisecond}
}

// waitForOr polls cond and fails with the caller's own message rather than a
// generic timeout, so the failure names the behaviour that is missing.
func waitForOr(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// MaxCheckTimeout is what shutdown sizes its budget from, so it has to read
// the live monitor set rather than any configured value: a monitor whose
// timeout was edited up must extend the shutdown wait on the next reload.
func TestMaxCheckTimeoutReportsTheSlowestScheduledMonitor(t *testing.T) {
	fast := job(1, time.Hour)
	fast.Monitor.Timeout = 5 * time.Second
	slow := job(2, time.Hour)
	slow.Monitor.Timeout = 120 * time.Second

	s := New(Options{
		Registry:       staticRegistry(fast, slow),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: &fakeChecker{}},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	// Nothing is scheduled before the first reload, and that has to read as
	// "no constraint" rather than "stop immediately".
	if got := s.MaxCheckTimeout(); got != 0 {
		t.Errorf("MaxCheckTimeout on an empty queue = %s, want 0", got)
	}

	if err := s.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if got := s.MaxCheckTimeout(); got != 120*time.Second {
		t.Errorf("MaxCheckTimeout = %s, want 120s — shutdown would size its "+
			"budget for the wrong monitor and close the database mid-check", got)
	}
}
