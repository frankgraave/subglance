package auth

import (
	"runtime/metrics"
	"sync"
	"testing"
	"time"
)

// The tests in this file are about what happens after an argon2 call: the
// 64 MiB it allocated goes back to the operating system within seconds, and a
// flood of logins does not buy a forced collection per attempt.

// cheapKey runs idKey with parameters small enough to call thousands of times.
// The release logic does not look at the parameters, only at the slot count.
func cheapKey() {
	idKey([]byte("pw"), []byte("salt-salt-salt-1"), 1, 8, 1, 32)
}

// waitQuiet waits until no argon2 call is in flight and no release is
// pending, so a test starts from a known state rather than inheriting a timer
// from the test before it.
func waitQuiet(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		releaser.mu.Lock()
		quiet := releaser.pending == nil && inFlightHashes.Load() == 0
		releaser.mu.Unlock()
		if quiet {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a release was still pending after 10s")
}

// recorder replaces releaseMemory for one test and records when each release
// ran. hold, when set, blocks every release until it is closed.
type recorder struct {
	mu    sync.Mutex
	times []time.Time
	hold  chan struct{}
}

func (r *recorder) release() {
	r.mu.Lock()
	r.times = append(r.times, time.Now())
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		<-hold
	}
}

func (r *recorder) snapshot() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.times...)
}

// waitCount waits up to d for at least n releases and returns what it saw.
func (r *recorder) waitCount(n int, d time.Duration) []time.Time {
	deadline := time.Now().Add(d)
	for {
		got := r.snapshot()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// useRecorder swaps in a recorder and an interval, and puts the real ones
// back when the test ends.
func useRecorder(t *testing.T, interval time.Duration, hold chan struct{}) *recorder {
	t.Helper()
	waitQuiet(t)
	r := &recorder{hold: hold}
	releaser.mu.Lock()
	oldRelease, oldInterval := releaseMemory, releaseInterval
	releaseMemory, releaseInterval = r.release, interval
	releaser.last = time.Time{}
	releaser.mu.Unlock()
	t.Cleanup(func() {
		waitQuiet(t)
		releaser.mu.Lock()
		releaseMemory, releaseInterval = oldRelease, oldInterval
		releaser.mu.Unlock()
	})
	return r
}

// TestArgon2ReleaseFollowsTheLastCall: one login is followed by a release, and
// the release runs off the request's goroutine, so the person signing in does
// not wait for a garbage collection.
func TestArgon2ReleaseFollowsTheLastCall(t *testing.T) {
	hold := make(chan struct{})
	r := useRecorder(t, 50*time.Millisecond, hold)

	returned := make(chan struct{})
	go func() {
		cheapKey()
		close(returned)
	}()

	if got := r.waitCount(1, 2*time.Second); len(got) != 1 {
		close(hold)
		t.Fatalf("releases after one argon2 call = %d, want 1", len(got))
	}
	// The release is now running and held. The call that scheduled it must
	// have returned regardless.
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		close(hold)
		t.Fatal("the argon2 call waited for the memory release to finish")
	}
	close(hold)
}

// TestArgon2ReleaseIsRateLimited is the login flood: thousands of calls, each
// taking the slot count back to zero, and still no more than one release per
// interval, with one release after the last call so its memory goes back too.
func TestArgon2ReleaseIsRateLimited(t *testing.T) {
	const interval = 100 * time.Millisecond
	r := useRecorder(t, interval, nil)

	start := time.Now()
	for time.Since(start) < 5*interval {
		cheapKey()
	}
	end := time.Now()

	// Whatever ran during the flood, one more release has to follow it.
	got := r.waitCount(1, 3*interval)
	deadline := time.Now().Add(3 * interval)
	for time.Now().Before(deadline) && (len(got) == 0 || got[len(got)-1].Before(end)) {
		time.Sleep(5 * time.Millisecond)
		got = r.snapshot()
	}
	if len(got) == 0 || got[len(got)-1].Before(end) {
		t.Fatalf("no release after the last argon2 call (releases: %d)", len(got))
	}

	// Ten intervals of flood and wait leave room for at most about ten; a
	// release per call would be thousands.
	elapsed := time.Since(start)
	if limit := int(elapsed/interval) + 2; len(got) > limit {
		t.Errorf("releases = %d in %v, want at most %d (one per %v)",
			len(got), elapsed.Round(time.Millisecond), limit, interval)
	}
	for i := 1; i < len(got); i++ {
		if gap := got[i].Sub(got[i-1]); gap < interval/2 {
			t.Errorf("releases %d and %d were %v apart, want about %v or more",
				i-1, i, gap, interval)
		}
	}
}

// TestArgon2ReleaseWaitsForCallsInFlight: a release that comes due while a
// call holds a slot does nothing, and the call that finishes last schedules
// the one that counts.
func TestArgon2ReleaseWaitsForCallsInFlight(t *testing.T) {
	const interval = 30 * time.Millisecond
	r := useRecorder(t, interval, nil)

	inFlightHashes.Add(1) // a hash still running
	scheduleRelease()
	time.Sleep(5 * interval)
	if got := r.snapshot(); len(got) != 0 {
		inFlightHashes.Add(-1)
		t.Fatalf("released %d times while a hash was in flight, want 0", len(got))
	}
	inFlightHashes.Add(-1)

	cheapKey()
	if got := r.waitCount(1, 2*time.Second); len(got) != 1 {
		t.Fatalf("releases after the last call finished = %d, want 1", len(got))
	}
}

// retained is the memory the runtime holds and has not handed back to the
// operating system: everything it has mapped, minus what it has released.
// It is the part of the process's RSS the Go runtime controls.
func retained() uint64 {
	s := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(s)
	return s[0].Value.Uint64() - s[1].Value.Uint64()
}

// TestArgon2MemoryIsReturned measures the real thing: a full-size password
// hash, and the memory it allocated back with the operating system within a
// few seconds instead of the minutes the background scavenger takes.
func TestArgon2MemoryIsReturned(t *testing.T) {
	waitQuiet(t)
	before := retained()

	if _, err := HashPassword("correct-horse-battery-staple"); err != nil {
		t.Fatalf("hash: %v", err)
	}

	// A quarter of what one hash allocates. The hash adds 64 MiB; anything
	// under this means it went back.
	const slack = uint64(argonMemory) * 1024 / 4
	var now uint64
	deadline := time.Now().Add(releaseInterval + 3*time.Second)
	for time.Now().Before(deadline) {
		if now = retained(); now <= before+slack {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("retained %d MiB after a hash, %d MiB before it: the %d MiB argon2 "+
		"allocated was not returned", now>>20, before>>20, argonMemory/1024)
}
