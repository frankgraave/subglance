package scheduler

import (
	"context"
	"errors"
	"github.com/frankgraave/subglance/internal/checker"
	"testing"
	"testing/synctest"
	"time"
)

func TestRecoveryCadence(t *testing.T) {
	for _, interval := range []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			now := time.Now()
			job := Job{Monitor: checker.Monitor{ID: 1}, Interval: interval}
			s := New(Options{Registry: RegistryFunc(func(context.Context) ([]Job, error) { return []Job{job}, nil }), Now: func() time.Time { return now }, JitterFraction: -1, Workers: 2})
			if err := s.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.SetDown(1, true)
			want := min(interval, time.Minute)
			if got := (*s.queue)[0].next.Sub(now); got != want {
				t.Fatalf("down next = %v, want %v", got, want)
			}
			// Reload must retain state supplied by the authoritative runner.
			job.Down = true
			if err := s.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := (*s.queue)[0].next.Sub(now); got != want {
				t.Fatalf("reload next = %v", got)
			}
			s.SetDown(1, false)
			if got := (*s.queue)[0].next.Sub(now); got != interval {
				t.Fatalf("recovered next = %v, want %v", got, interval)
			}
			if s.Workers() != 2 {
				t.Fatalf("worker cap changed: %d", s.Workers())
			}
			// Restart with a confirmed incident takes the shortened first interval.
			fresh := New(Options{Registry: s.registry, Now: s.now, JitterFraction: -1})
			if err := fresh.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := (*fresh.queue)[0].next.Sub(now); got != want {
				t.Fatalf("restored down next = %v", got)
			}
		})
	}
}

func TestDownRecoveryRunsThroughBoundedWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeChecker{delay: 2 * time.Second}
		jobs := make([]Job, 8)
		for i := range jobs {
			jobs[i] = job(int64(i+1), 5*time.Minute)
			jobs[i].Down = true
		}
		s := New(Options{Registry: staticRegistry(jobs...), Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: fake}, Workers: 2, JitterFraction: -1, Log: testLogger()})
		ctx, cancel := context.WithTimeout(t.Context(), 125*time.Second)
		defer cancel()
		if err := s.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if got := fake.callCount(); got < 10 {
			t.Fatalf("down monitors did not run at recovery cadence: %d checks", got)
		}
		if got := fake.maxRunning.Load(); got != 2 {
			t.Fatalf("recovery worker concurrency = %d, want 2", got)
		}
	})
}

func TestDownTransitionDoesNotPostponeAlreadyFastCheck(t *testing.T) {
	now := time.Now()
	s := New(Options{Registry: staticRegistry(job(1, 15*time.Second)), Now: func() time.Time { return now }, JitterFraction: -1, Log: testLogger()})
	if err := s.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	due := (*s.queue)[0].next
	now = now.Add(10 * time.Second)
	s.SetDown(1, true)
	if got := (*s.queue)[0].next; !got.Equal(due) {
		t.Fatalf("down transition postponed fast check: %v, want %v", got, due)
	}
	s.SetDown(1, true)
	if got := (*s.queue)[0].next; !got.Equal(due) {
		t.Fatalf("repeated down postponed check: %v, want %v", got, due)
	}
}

// A type with a floor keeps it while down: a domain monitor's registry is not
// asked every minute because the registration has lapsed.
func TestRecoveryCadenceKeepsATypesFloor(t *testing.T) {
	now := time.Now()
	job := Job{Monitor: checker.Monitor{ID: 1, Type: checker.TypeDomain}, Interval: 24 * time.Hour}
	s := New(Options{Registry: RegistryFunc(func(context.Context) ([]Job, error) { return []Job{job}, nil }), Now: func() time.Time { return now }, JitterFraction: -1})
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.SetDown(1, true)
	if got := (*s.queue)[0].next.Sub(now); got != checker.MinDomainInterval {
		t.Fatalf("down next = %v, want the %v floor", got, checker.MinDomainInterval)
	}
	short := Job{Monitor: checker.Monitor{ID: 1, Type: checker.TypeDomain}, Interval: time.Minute}
	if got := short.cadence(); got != checker.MinDomainInterval {
		t.Errorf("cadence of a domain job set to a minute = %v, want the floor", got)
	}
}

// A monitor never comes due before its slowest check could have finished, up
// or down. It is never run twice at once, so a dispatch inside a check still
// running would be skipped and logged as a warning instead.
func TestCadenceLeavesRoomForTheTimeout(t *testing.T) {
	long := func(interval, timeout time.Duration, down bool) Job {
		return Job{Monitor: checker.Monitor{ID: 1, Type: checker.TypeHTTP, Timeout: timeout}, Interval: interval, Down: down}
	}
	cases := []struct {
		name string
		job  Job
		want time.Duration
	}{
		{"down, timeout above the recovery minute", long(5*time.Minute, 90*time.Second, true), 112500 * time.Millisecond},
		{"up, stored before the API bound", long(20*time.Second, 120*time.Second, false), 150 * time.Second},
		{"down, timeout within the recovery minute", long(5*time.Minute, 48*time.Second, true), time.Minute},
		{"up, timeout within the interval", long(time.Minute, 10*time.Second, false), time.Minute},
	}
	for _, tc := range cases {
		if got := tc.job.cadence(); got != tc.want {
			t.Errorf("%s: cadence = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// End to end: a check that runs as long as its timeout is not dispatched again
// while it runs, so the scheduler skips nothing.
func TestCheckLongerThanItsIntervalIsNotSkipped(t *testing.T) {
	fake := &fakeChecker{delay: 120 * time.Millisecond}
	j := job(1, 20*time.Millisecond)
	j.Monitor.Timeout = 120 * time.Millisecond

	s := New(Options{
		Registry:       staticRegistry(j),
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
		OnResult:       func(Outcome) {},
		JitterFraction: -1,
		Log:            testLogger(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if n := s.SkippedChecks(); n != 0 {
		t.Errorf("%d checks skipped; a monitor must not come due inside its own timeout", n)
	}
	if n := fake.callCount(); n < 2 {
		t.Errorf("%d checks ran in 700ms on a 150ms cadence, want at least 2", n)
	}
}
