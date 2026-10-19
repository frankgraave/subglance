package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// mutableRegistry is a monitor set a test changes while the scheduler runs,
// counting how often the scheduler reads it.
type mutableRegistry struct {
	mu    sync.Mutex
	jobs  []Job
	err   error
	reads atomic.Int32
}

func (m *mutableRegistry) set(err error, jobs ...Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs, m.err = jobs, err
}

func (m *mutableRegistry) Jobs(context.Context) ([]Job, error) {
	m.reads.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return append([]Job(nil), m.jobs...), nil
}

// runWithTick runs a scheduler over reg with the production reload tick of 30
// seconds, so anything that happens well inside that did not come from the
// tick. It returns the scheduler and a stop function that waits for Run.
func runWithTick(reg Registry, c checker.Checker) (*Scheduler, func()) {
	s := New(Options{
		Registry:       reg,
		Checkers:       map[checker.Type]checker.Checker{checker.TypeHTTP: c},
		OnResult:       func(Outcome) {},
		ReloadInterval: 30 * time.Second,
		JitterFraction: -1,
		Log:            testLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()
	return s, func() {
		cancel()
		<-done
	}
}

func neverChecked(id int64, interval time.Duration) Job {
	j := job(id, interval)
	j.NeverChecked = true
	return j
}

// A monitor added while the scheduler runs is checked firstCheckDelay after
// the settle time that follows ReloadSoon, not after the next reload tick.
// Before, nothing asked for a read, and a monitor saved through the API waited
// up to 30 seconds for its first check.
func TestReloadSoonSchedulesANewMonitorNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &mutableRegistry{}
		fake := &fakeChecker{}
		s, stop := runWithTick(reg, fake)
		defer stop()

		time.Sleep(5 * time.Second)
		synctest.Wait()
		reg.set(nil, neverChecked(1, time.Hour))
		s.ReloadSoon()

		time.Sleep(reloadSettle + firstCheckDelay + time.Millisecond)
		synctest.Wait()
		if n := fake.callCount(); n != 1 {
			t.Errorf("%d checks %v after ReloadSoon, want 1: the new monitor waits for the 30-second tick",
				n, reloadSettle+firstCheckDelay)
		}
	})
}

// A monitor removed from the set (deleted or paused) stops being checked as
// soon as the read that ReloadSoon asks for has happened. Before, it went on
// being checked, and could alert, until the next tick.
func TestReloadSoonDropsARemovedMonitorNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &mutableRegistry{}
		reg.set(nil, neverChecked(1, 20*time.Second))
		fake := &fakeChecker{}
		s, stop := runWithTick(reg, fake)
		defer stop()

		// First check at 2 s; the next is due at 22 s, before the 30 s tick.
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if n := fake.callCount(); n != 1 {
			t.Fatalf("%d checks in the first 5 s, want the first check", n)
		}
		reg.set(nil)
		s.ReloadSoon()

		time.Sleep(20 * time.Second)
		synctest.Wait()
		if n := fake.callCount(); n != 1 {
			t.Errorf("%d checks, want 1: a removed monitor was checked again at 22 s", n)
		}
		if got := s.Size(); got != 0 {
			t.Errorf("%d monitors scheduled after removal, want 0", got)
		}
	})
}

// An edited interval applies once the read has happened, not at the tick.
func TestReloadSoonAppliesAChangedIntervalNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &mutableRegistry{}
		reg.set(nil, neverChecked(1, time.Hour))
		fake := &fakeChecker{}
		s, stop := runWithTick(reg, fake)
		defer stop()

		time.Sleep(5 * time.Second) // first check at 2 s; next due in an hour
		synctest.Wait()
		reg.set(nil, job(1, 20*time.Second))
		s.ReloadSoon()

		// The new interval counts from the read, at 5.2 s: due at 25.2 s.
		time.Sleep(21 * time.Second)
		synctest.Wait()
		if n := fake.callCount(); n != 2 {
			t.Errorf("%d checks at 26 s, want 2: the shortened interval waited for the tick", n)
		}
	})
}

// Requests that arrive while one is waiting are served by the same read, so a
// bulk action sent as one request per monitor, or an import, does not read
// the whole monitor set once per monitor.
func TestReloadSoonFoldsABurstIntoOneRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &mutableRegistry{}
		reg.set(nil, job(1, time.Hour))
		s, stop := runWithTick(reg, &fakeChecker{})
		defer stop()

		time.Sleep(time.Second)
		synctest.Wait()
		before := reg.reads.Load()

		// Twenty requests 5 ms apart, as a bulk pause of twenty monitors
		// arrives: all inside one settle time.
		for range 20 {
			s.ReloadSoon()
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(reloadSettle)
		synctest.Wait()
		if got := reg.reads.Load() - before; got != 1 {
			t.Errorf("%d reads for a burst of 20 requests, want 1", got)
		}

		// A request after that read gets a read of its own.
		s.ReloadSoon()
		time.Sleep(reloadSettle + time.Millisecond)
		synctest.Wait()
		if got := reg.reads.Load() - before; got != 2 {
			t.Errorf("%d reads after a later request, want 2", got)
		}
	})
}

// ReloadSoon is called on a request's path, so it must return at once whether
// or not Run is there to hear it: before Run starts, while a read is
// waiting, and after Run has returned.
func TestReloadSoonNeverBlocks(t *testing.T) {
	s := New(Options{
		Registry: staticRegistry(),
		Checkers: map[checker.Type]checker.Checker{},
		OnResult: func(Outcome) {},
		Log:      testLogger(),
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			s.ReloadSoon()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReloadSoon blocked without a running scheduler")
	}
}

// A read that fails keeps the queue as it was and is logged; the tick tries
// again. The request that asked for it has already been answered.
func TestReloadSoonFailureKeepsTheQueueAndTheTickRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &mutableRegistry{}
		reg.set(nil, job(1, time.Hour), job(2, time.Hour))
		s, stop := runWithTick(reg, &fakeChecker{})
		defer stop()

		time.Sleep(time.Second)
		synctest.Wait()
		reg.set(errors.New("database is locked"), job(1, time.Hour))
		s.ReloadSoon()
		time.Sleep(reloadSettle + time.Millisecond)
		synctest.Wait()
		if got := s.Size(); got != 2 {
			t.Fatalf("%d monitors scheduled after a failed read, want the 2 it had", got)
		}

		reg.set(nil, job(1, time.Hour))
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if got := s.Size(); got != 1 {
			t.Errorf("%d monitors scheduled after the tick, want 1", got)
		}
	})
}
