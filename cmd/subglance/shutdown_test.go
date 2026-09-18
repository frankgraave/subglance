package main

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// awaitScheduler must not return while a check is still running, whatever the
// budget says.
//
// What this exercises: the ordering that the real shutdown depends on. In run,
// db.Close() is deferred, so it executes the instant run returns — meaning the
// only thing standing between a worker's RecordHeartbeat and a closed pool is
// this wait. The test stands in for the closed pool by recording whether the
// wait returned before the "check" finished; a test that only asserted the
// happy path (fast check, generous budget) would prove nothing, so the budget
// here is deliberately far shorter than the work.
func TestAwaitSchedulerOutlastsASlowCheck(t *testing.T) {
	done := make(chan struct{})
	var checkFinished atomic.Bool

	// A check far slower than the budget, as a 60s monitor is against the
	// 15s default and Docker's 10s stop timeout.
	go func() {
		time.Sleep(150 * time.Millisecond)
		checkFinished.Store(true)
		close(done)
	}()

	returned := make(chan struct{})
	go func() {
		awaitScheduler(done, 10*time.Millisecond, quietLogger())
		close(returned)
	}()

	select {
	case <-returned:
		if !checkFinished.Load() {
			t.Fatal("awaitScheduler returned while a check was still running; " +
				"the deferred db.Close() would run underneath it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("awaitScheduler did not return after the check finished")
	}
}

// The ordinary case still returns promptly: waiting for the scheduler must not
// turn a clean restart into a wait for the whole budget.
func TestAwaitSchedulerReturnsAsSoonAsChecksFinish(t *testing.T) {
	done := make(chan struct{})
	close(done)

	start := time.Now()
	awaitScheduler(done, time.Minute, quietLogger())

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("awaitScheduler took %s on an already-stopped scheduler; "+
			"it is waiting out the budget instead of the work", elapsed)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The budget the scheduler gets to finish its checks.
//
// The defect: one 15s budget covered the HTTP drain, the scheduler wait and
// the watchdog wait in series, while a per-monitor timeout goes up to 120s.
// SIGTERM with a slow monitor mid-check blew the deadline, fell through to
// the deferred db.Close(), and left workers writing heartbeats against closed
// pools. Docker's default --stop-timeout is 10s, so that was the ordinary
// restart path for anyone with one slow monitor, not an edge case.
func TestSchedulerShutdownBudget(t *testing.T) {
	cases := []struct {
		name            string
		configured      time.Duration
		maxCheckTimeout time.Duration
		want            time.Duration
	}{
		{
			name:            "the ticket's case: a 120s monitor under the 15s default",
			configured:      15 * time.Second,
			maxCheckTimeout: 120 * time.Second,
			want:            125 * time.Second,
		},
		{
			name:            "fast monitors do not extend anything",
			configured:      15 * time.Second,
			maxCheckTimeout: 5 * time.Second,
			want:            15 * time.Second,
		},
		{
			// An operator who raised --shutdown-timeout wanted the longer
			// grace; the derived floor must never shorten it.
			name:            "a generous configured timeout wins",
			configured:      5 * time.Minute,
			maxCheckTimeout: 30 * time.Second,
			want:            5 * time.Minute,
		},
		{
			// Nothing scheduled means no constraint from the monitor set,
			// not "stop immediately".
			name:            "no monitors scheduled",
			configured:      15 * time.Second,
			maxCheckTimeout: 0,
			want:            15 * time.Second,
		},
		{
			// The margin covers recording the heartbeat and advancing the
			// state machine, which the monitor's own timeout excludes.
			name:            "the budget exceeds the slowest check itself",
			configured:      time.Second,
			maxCheckTimeout: 60 * time.Second,
			want:            65 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := schedulerShutdownBudget(tc.configured, tc.maxCheckTimeout)
			if got != tc.want {
				t.Errorf("schedulerShutdownBudget(%s, %s) = %s, want %s",
					tc.configured, tc.maxCheckTimeout, got, tc.want)
			}
			if tc.maxCheckTimeout > 0 && got <= tc.maxCheckTimeout {
				t.Errorf("budget %s does not outlast the slowest check (%s), "+
					"so the database can still be closed underneath it",
					got, tc.maxCheckTimeout)
			}
			if got < tc.configured {
				t.Errorf("budget %s is shorter than the configured %s",
					got, tc.configured)
			}
		})
	}
}
