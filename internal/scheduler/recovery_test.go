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
