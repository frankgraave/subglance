package monitor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/store"
)

// scriptedChecker answers each check with the next result in its script, the
// last one for every check after that, and records when each check ran.
type scriptedChecker struct {
	mu     sync.Mutex
	script []bool
	ran    []time.Time
}

func (c *scriptedChecker) Check(context.Context, checker.Monitor) checker.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.script[min(len(c.ran), len(c.script)-1)]
	c.ran = append(c.ran, time.Now())
	if ok {
		return checker.Result{OK: true, StatusCode: 200, CheckedAt: time.Now()}
	}
	return checker.Result{OK: false, StatusCode: 500, Kind: checker.FailStatus,
		Error: "unexpected status 500", CheckedAt: time.Now()}
}

// offsets returns when each check ran, measured from start.
func (c *scriptedChecker) offsets(start time.Time) []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.ran))
	for i, at := range c.ran {
		out[i] = at.Sub(start)
	}
	return out
}

// runFailureCadence runs a five-minute monitor with three retries against a
// scripted target for the given time, on synctest's fake clock and with the
// jitter off, and returns the runner and the checker. The monitor has passed
// once before the scheduler starts, so its first check is a full interval in.
func runFailureCadence(t *testing.T, script []bool, run time.Duration) (*Runner, *scriptedChecker, store.Monitor, time.Time) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := testDB(t)
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "five-minute site", Type: "http",
		Target: "https://example.invalid", Enabled: true, Retries: 3, RecoveryThreshold: 1, IntervalS: 300})
	if err != nil {
		t.Fatal(err)
	}
	alerts := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
	r.record(outcomeFor(m, m.Target, true))
	fake := &scriptedChecker{script: script}
	r.sch = scheduler.New(scheduler.Options{Registry: scheduler.RegistryFunc(r.jobs),
		Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: fake}, OnResult: r.record,
		Workers: 1, JitterFraction: -1, ReloadInterval: time.Hour, Log: quietLogger()})
	start := time.Now()
	done := make(chan struct{})
	go func() { defer close(done); _ = r.sch.Run(ctx) }()
	time.Sleep(run)
	synctest.Wait()
	cancel()
	<-done
	return r, fake, m, start
}

// A failure that is not confirmed yet is rechecked every minute, not at the
// monitor's five-minute interval: three retries confirm two minutes after the
// first failure, where they used to take ten.
func TestAnUnconfirmedFailureIsRecheckedEveryMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, fake, m, start := runFailureCadence(t, []bool{false}, 7*time.Minute+30*time.Second)

		want := []time.Duration{5 * time.Minute, 6 * time.Minute, 7 * time.Minute}
		if got := fake.offsets(start); !slices.Equal(got, want) {
			t.Fatalf("checks ran at %v, want %v: the failure cadence starts at the first failure", got, want)
		}
		if got := r.Engine().Status(m.ID); got != "down" {
			t.Fatalf("status after three failures a minute apart = %s, want down", got)
		}
	})
}

// A pass while the failure is unconfirmed clears it, and the monitor goes back
// to its own interval from that pass.
func TestAPassDuringWarningRestoresTheInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, fake, m, start := runFailureCadence(t, []bool{false, true}, 12*time.Minute)

		want := []time.Duration{5 * time.Minute, 6 * time.Minute, 11 * time.Minute}
		if got := fake.offsets(start); !slices.Equal(got, want) {
			t.Fatalf("checks ran at %v, want %v: a pass must restore the interval", got, want)
		}
		if got := r.Engine().Status(m.ID); got != "up" {
			t.Fatalf("status after a pass = %s, want up", got)
		}
	})
}
