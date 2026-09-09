package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeChecker records what it was asked to check and returns a fixed result.
type fakeChecker struct {
	mu     sync.Mutex
	calls  []int64
	delay  time.Duration
	result checker.Result

	// running tracks concurrent executions so tests can assert the worker
	// pool actually bounds concurrency.
	running    atomic.Int32
	maxRunning atomic.Int32
}

func (f *fakeChecker) Check(ctx context.Context, m checker.Monitor) checker.Result {
	cur := f.running.Add(1)
	for {
		peak := f.maxRunning.Load()
		if cur <= peak || f.maxRunning.CompareAndSwap(peak, cur) {
			break
		}
	}
	defer f.running.Add(-1)

	f.mu.Lock()
	f.calls = append(f.calls, m.ID)
	f.mu.Unlock()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return checker.Result{OK: false, Kind: checker.FailInternal, Error: "cancelled"}
		}
	}
	if f.result.CheckedAt.IsZero() {
		return checker.Result{OK: true, Latency: time.Millisecond, StatusCode: 200}
	}
	return f.result
}

func (f *fakeChecker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func job(id int64, interval time.Duration) Job {
	return Job{
		Monitor: checker.Monitor{
			ID:     id,
			Name:   "monitor",
			Type:   checker.TypeHTTP,
			Target: "https://example.com",
		},
		Interval: interval,
	}
}

func staticRegistry(jobs ...Job) Registry {
	return RegistryFunc(func(context.Context) ([]Job, error) { return jobs, nil })
}

func TestRunsChecksOnInterval(t *testing.T) {
	fake := &fakeChecker{}
	var outcomes atomic.Int32

	s := New(Options{
		Registry:       staticRegistry(job(1, 50*time.Millisecond)),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) { outcomes.Add(1) },
		JitterFraction: -1, // deterministic timing
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if got := fake.callCount(); got < 3 {
		t.Errorf("ran %d checks in 350ms at a 50ms interval, want at least 3", got)
	}
	if int(outcomes.Load()) != fake.callCount() {
		t.Errorf("outcomes = %d, checks = %d: not every result was reported",
			outcomes.Load(), fake.callCount())
	}
}

func TestRespectsPerMonitorIntervals(t *testing.T) {
	fake := &fakeChecker{}
	counts := map[int64]int{}
	var mu sync.Mutex

	s := New(Options{
		Registry: staticRegistry(
			job(1, 40*time.Millisecond),
			job(2, 200*time.Millisecond),
		),
		Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult: func(o Outcome) {
			mu.Lock()
			counts[o.Monitor.ID]++
			mu.Unlock()
		},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if counts[1] <= counts[2] {
		t.Errorf("fast monitor ran %d times, slow monitor %d: intervals are not being respected",
			counts[1], counts[2])
	}
}

// A check slower than its own interval must not stack up. Queueing repeatedly
// against a struggling endpoint would multiply the load on it.
func TestSlowCheckIsNotScheduledTwice(t *testing.T) {
	fake := &fakeChecker{delay: 250 * time.Millisecond}

	s := New(Options{
		Registry:       staticRegistry(job(1, 20*time.Millisecond)),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if peak := fake.maxRunning.Load(); peak > 1 {
		t.Errorf("%d concurrent runs of the same monitor; overlapping checks must be skipped", peak)
	}
}

// The worker pool must cap concurrency: this is what keeps memory flat when a
// few hundred monitors come due at once.
func TestWorkerPoolBoundsConcurrency(t *testing.T) {
	fake := &fakeChecker{delay: 100 * time.Millisecond}

	var jobs []Job
	for i := int64(1); i <= 50; i++ {
		jobs = append(jobs, job(i, 30*time.Millisecond))
	}

	s := New(Options{
		Registry:       staticRegistry(jobs...),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		Workers:        4,
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if peak := fake.maxRunning.Load(); peak > 4 {
		t.Errorf("peak concurrency was %d with 4 workers; the pool is not bounding work", peak)
	}
	if fake.callCount() == 0 {
		t.Error("no checks ran at all")
	}
}

func TestUnsupportedTypeReportsFailure(t *testing.T) {
	var got Outcome
	var mu sync.Mutex

	j := job(1, 20*time.Millisecond)
	j.Monitor.Type = "carrier-pigeon"

	s := New(Options{
		Registry: staticRegistry(j),
		Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: &fakeChecker{}},
		OnResult: func(o Outcome) {
			mu.Lock()
			got = o
			mu.Unlock()
		},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if got.Result.OK {
		t.Fatal("an unsupported monitor type was reported as healthy")
	}
	if got.Result.Kind != checker.FailInternal {
		t.Errorf("kind = %q, want %q", got.Result.Kind, checker.FailInternal)
	}
}

// panicChecker exists to prove one bad check cannot take down the process.
type panicChecker struct{}

func (panicChecker) Check(context.Context, checker.Monitor) checker.Result {
	panic("checker exploded")
}

func TestPanicInCheckIsContained(t *testing.T) {
	var got Outcome
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	s := New(Options{
		Registry: staticRegistry(job(1, 20*time.Millisecond)),
		Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: panicChecker{}},
		OnResult: func(o Outcome) {
			mu.Lock()
			got = o
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
		},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx) // must return normally, not panic

	select {
	case <-done:
	default:
		t.Fatal("a panicking check produced no outcome")
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Result.OK {
		t.Error("a panicking check was reported as healthy")
	}
	if got.Result.Kind != checker.FailInternal {
		t.Errorf("kind = %q, want %q", got.Result.Kind, checker.FailInternal)
	}
}

func TestReloadAddsAndRemovesMonitors(t *testing.T) {
	var current atomic.Value
	current.Store([]Job{job(1, time.Hour)})

	s := New(Options{
		Registry: RegistryFunc(func(context.Context) ([]Job, error) {
			return current.Load().([]Job), nil
		}),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: &fakeChecker{}},
		OnResult:       func(Outcome) {},
		ReloadInterval: 30 * time.Millisecond,
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx) }()
	defer cancel()

	waitFor(t, 500*time.Millisecond, func() bool { return s.Size() == 1 })

	current.Store([]Job{job(1, time.Hour), job(2, time.Hour), job(3, time.Hour)})
	waitFor(t, 500*time.Millisecond, func() bool { return s.Size() == 3 })

	current.Store([]Job{job(2, time.Hour)})
	waitFor(t, 500*time.Millisecond, func() bool { return s.Size() == 1 })
}

// A shortened interval must take effect immediately, not after the old, longer
// wait has elapsed.
func TestShortenedIntervalTakesEffect(t *testing.T) {
	fake := &fakeChecker{}
	var current atomic.Value
	current.Store([]Job{job(1, time.Hour)})

	s := New(Options{
		Registry: RegistryFunc(func(context.Context) ([]Job, error) {
			return current.Load().([]Job), nil
		}),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		ReloadInterval: 25 * time.Millisecond,
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(60 * time.Millisecond)
		current.Store([]Job{job(1, 30*time.Millisecond)})
	}()

	_ = s.Run(ctx)

	if fake.callCount() == 0 {
		t.Error("the monitor never ran after its interval was shortened from 1h")
	}
}

func TestRegistryErrorDoesNotStopScheduler(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	fake := &fakeChecker{}

	s := New(Options{
		Registry: RegistryFunc(func(context.Context) ([]Job, error) {
			if fail.Load() {
				return nil, context.DeadlineExceeded
			}
			return []Job{job(1, 30*time.Millisecond)}, nil
		}),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		ReloadInterval: 25 * time.Millisecond,
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(80 * time.Millisecond)
		fail.Store(false)
	}()

	_ = s.Run(ctx)

	if fake.callCount() == 0 {
		t.Error("the scheduler never recovered after the registry started working")
	}
}

// Cancellation must wait for in-flight checks: otherwise a SIGTERM could
// interrupt a result mid-write.
func TestShutdownWaitsForInFlightChecks(t *testing.T) {
	fake := &fakeChecker{delay: 150 * time.Millisecond}
	var completed atomic.Int32

	s := New(Options{
		Registry:       staticRegistry(job(1, 20*time.Millisecond)),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) { completed.Add(1) },
		JitterFraction: -1,
		Log:            testLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()

	time.Sleep(80 * time.Millisecond) // let a check start
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation")
	}

	if fake.running.Load() != 0 {
		t.Error("Run returned while a check was still running")
	}
}

func TestJitterSpreadsScheduling(t *testing.T) {
	s := New(Options{
		Registry:       staticRegistry(),
		Checkers:       map[checker.Type]checker.Checker{},
		OnResult:       func(Outcome) {},
		JitterFraction: 0.1,
		Log:            testLogger(),
	})

	base := time.Now()
	interval := time.Minute

	seen := map[time.Time]bool{}
	for range 50 {
		seen[s.nextRun(base, interval)] = true
	}
	if len(seen) < 40 {
		t.Errorf("only %d distinct times out of 50: jitter is not spreading the load", len(seen))
	}

	for at := range seen {
		delta := at.Sub(base)
		if delta < interval {
			t.Errorf("scheduled %s early, before the interval elapsed", interval-delta)
		}
		if delta > interval+time.Duration(float64(interval)*0.1) {
			t.Errorf("jitter of %s exceeds the 10%% bound", delta-interval)
		}
	}
}

func TestJitterCanBeDisabled(t *testing.T) {
	s := New(Options{
		Registry:       staticRegistry(),
		Checkers:       map[checker.Type]checker.Checker{},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	base := time.Now()
	if got := s.nextRun(base, time.Minute); !got.Equal(base.Add(time.Minute)) {
		t.Errorf("nextRun = %s, want exactly one minute later", got.Sub(base))
	}
}

func TestNonPositiveIntervalIsSkipped(t *testing.T) {
	s := New(Options{
		Registry:       staticRegistry(job(1, 0), job(2, -time.Second), job(3, time.Minute)),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: &fakeChecker{}},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})

	if err := s.reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s.Size() != 1 {
		t.Errorf("queue holds %d monitors, want 1: invalid intervals must be skipped", s.Size())
	}
}

func TestDefaultWorkerCountIsSane(t *testing.T) {
	s := New(Options{
		Registry: staticRegistry(),
		Checkers: map[checker.Type]checker.Checker{},
		OnResult: func(Outcome) {},
		Log:      testLogger(),
	})
	if s.workers < 8 || s.workers > 128 {
		t.Errorf("default worker count = %d, want it bounded to [8, 128]", s.workers)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
