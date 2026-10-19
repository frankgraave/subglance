package scheduler

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// queuedAt loads jobs into a fresh scheduler at now, as a start does, and
// returns when each monitor is first due.
func queuedAt(t *testing.T, now time.Time, jitter float64, jobs ...Job) map[int64]time.Time {
	t.Helper()
	s := New(Options{
		Registry:       staticRegistry(jobs...),
		Checkers:       map[checker.Type]checker.Checker{},
		OnResult:       func(Outcome) {},
		Now:            func() time.Time { return now },
		JitterFraction: jitter,
		Log:            testLogger(),
	})
	if err := s.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	due := make(map[int64]time.Time, s.queue.Len())
	for _, item := range *s.queue {
		due[item.job.Monitor.ID] = item.next
	}
	return due
}

func checkedAt(id int64, interval time.Duration, last time.Time) Job {
	j := job(id, interval)
	j.LastCheck = last
	return j
}

// A restart does not move a checked monitor: it is due one interval after its
// last check, not one interval after the start. Before, every start put every
// monitor a full interval out, so a monitor restarted more often than its
// interval was never checked again.
func TestStartKeepsAMonitorsPlace(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name     string
		interval time.Duration
		last     time.Time
		want     time.Duration // after now
	}{
		{"one-minute monitor 40 s after its check", time.Minute, now.Add(-40 * time.Second), 20 * time.Second},
		{"hourly monitor 55 min after its check", time.Hour, now.Add(-55 * time.Minute), 5 * time.Minute},
		{"daily monitor 20 h after its check", 24 * time.Hour, now.Add(-20 * time.Hour), 4 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			due := queuedAt(t, now, -1, checkedAt(1, tc.interval, tc.last))
			if got := due[1].Sub(now); got != tc.want {
				t.Errorf("first check in %v, want %v (one interval after the last check)", got, tc.want)
			}
		})
	}
}

// With jitter on, a kept place is spread the way any scheduled check is: no
// earlier than its due time, and no later than the jitter fraction after it.
func TestKeptPlaceTakesTheUsualJitter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	last := now.Add(-50 * time.Minute)
	dueAt := last.Add(time.Hour)
	seen := map[time.Time]bool{}
	for range 40 {
		at := queuedAt(t, now, 0.1, checkedAt(1, time.Hour, last))[1]
		if at.Before(dueAt) || at.After(dueAt.Add(6*time.Minute)) {
			t.Fatalf("first check at %v after its due time, want within [0, 6m]", at.Sub(dueAt))
		}
		seen[at] = true
	}
	if len(seen) < 30 {
		t.Errorf("only %d distinct times out of 40: the kept place is not jittered", len(seen))
	}
}

// A monitor already overdue at the start (the instance was down longer than
// its interval) runs within seconds, and a batch of them is spread over at
// most catchUpSpread instead of firing at once or waiting out their own
// jitter windows, which for a daily monitor is 2.4 hours.
func TestOverdueMonitorsCatchUpSpreadOut(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var jobs []Job
	for id := int64(1); id <= 50; id++ {
		jobs = append(jobs, checkedAt(id, 24*time.Hour, now.Add(-3*24*time.Hour)))
	}
	due := queuedAt(t, now, 0.1, jobs...)
	seen := map[time.Time]bool{}
	for id, at := range due {
		wait := at.Sub(now)
		if wait < firstCheckDelay || wait > firstCheckDelay+catchUpSpread {
			t.Errorf("monitor %d: overdue check in %v, want within [%v, %v]", id, wait, firstCheckDelay, firstCheckDelay+catchUpSpread)
		}
		seen[at] = true
	}
	if len(seen) < 40 {
		t.Errorf("only %d distinct times for 50 overdue monitors: they would fire together", len(seen))
	}

	// Without jitter an overdue monitor runs after firstCheckDelay exactly.
	if got := queuedAt(t, now, -1, jobs[0])[1].Sub(now); got != firstCheckDelay {
		t.Errorf("overdue check without jitter in %v, want %v", got, firstCheckDelay)
	}
}

// A last check later than now means the clock went back. Counting from it
// would postpone the next check by however far the clock moved; the wait is
// capped at what a start without a last check gives.
func TestLastCheckInTheFutureWaitsNoLongerThanOneInterval(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, ahead := range []time.Duration{time.Second, 30 * time.Minute, 48 * time.Hour} {
		due := queuedAt(t, now, -1, checkedAt(1, time.Hour, now.Add(ahead)))
		if got := due[1].Sub(now); got > time.Hour {
			t.Errorf("last check %v ahead: first check in %v, want at most one interval", ahead, got)
		}
	}
}

// What did not change: a never-checked monitor still gets its quick first
// check, and a monitor whose last check is unknown waits one interval.
func TestFirstRunOfNewAndUnknownMonitors(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	fresh := job(1, time.Hour)
	fresh.NeverChecked = true
	unknown := job(2, time.Hour)
	due := queuedAt(t, now, -1, fresh, unknown)
	if got := due[1].Sub(now); got != firstCheckDelay {
		t.Errorf("never-checked monitor: first check in %v, want %v", got, firstCheckDelay)
	}
	if got := due[2].Sub(now); got != time.Hour {
		t.Errorf("monitor without a known last check: first check in %v, want one interval", got)
	}
}

// The place is counted on the monitor's cadence, not its bare interval: a
// failing monitor is due a recheck minute after its last check, and a domain
// monitor keeps its six-hour floor.
func TestKeptPlaceFollowsTheCadence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	failing := checkedAt(1, time.Hour, now.Add(-20*time.Second))
	failing.Failing = true
	domain := checkedAt(2, time.Minute, now.Add(-time.Hour))
	domain.Monitor.Type = checker.TypeDomain
	due := queuedAt(t, now, -1, failing, domain)
	if got := due[1].Sub(now); got != 40*time.Second {
		t.Errorf("failing monitor: first check in %v, want 40s (one recheck minute after its last check)", got)
	}
	if got := due[2].Sub(now); got != checker.MinDomainInterval-time.Hour {
		t.Errorf("domain monitor: first check in %v, want %v (its floor after the last check)", got, checker.MinDomainInterval-time.Hour)
	}
}

// A monitor already in the queue keeps its scheduled time on a reload, as
// before: LastCheck places a monitor only when it enters the queue.
func TestReloadKeepsAQueuedMonitorsTime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	current := checkedAt(1, time.Hour, now.Add(-10*time.Minute))
	s := New(Options{
		Registry:       RegistryFunc(func(context.Context) ([]Job, error) { return []Job{current}, nil }),
		Checkers:       map[checker.Type]checker.Checker{},
		OnResult:       func(Outcome) {},
		Now:            func() time.Time { return now },
		JitterFraction: -1,
		Log:            testLogger(),
	})
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := (*s.queue)[0].next
	current.LastCheck = now.Add(-59 * time.Minute)
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := (*s.queue)[0].next; !after.Equal(before) {
		t.Errorf("a reload moved a queued monitor from %v to %v", before.Sub(now), after.Sub(now))
	}
}

// End to end on a fake clock: a one-minute monitor whose process is stopped
// and started again every 40 seconds is still checked about once a minute.
// Before, each start pushed it a minute out and it was never checked.
func TestRestartsMoreOftenThanTheIntervalStillCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeChecker{}
		var last time.Time
		for range 6 { // 6 x 40 s = 4 minutes
			j := job(1, time.Minute)
			j.LastCheck = last
			j.NeverChecked = last.IsZero() && fake.callCount() == 0
			s := New(Options{
				Registry: staticRegistry(j),
				Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: fake},
				OnResult: func(o Outcome) {
					if o.Result.OK {
						last = time.Now()
					}
				},
				JitterFraction: -1,
				Log:            testLogger(),
			})
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			_ = s.Run(ctx)
			cancel()
			synctest.Wait()
		}
		// First check at 2 s, then 62, 122, 182 s: four in 240 s.
		if n := fake.callCount(); n < 4 {
			t.Errorf("%d checks in 4 minutes of 40-second restarts, want at least 4 for a one-minute monitor", n)
		}
	})
}
