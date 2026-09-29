package housekeeping

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// fakeStore records passes in memory and lets a test hold a pass open.
type fakeStore struct {
	mu      sync.Mutex
	runAt   store.ClockTime
	last    *store.RetentionPass
	applied int
	fail    error
	// block, when set, holds ApplyRetention until it is closed.
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeStore) ResolveRetention(context.Context, store.RetentionPins) (store.EffectiveRetention, error) {
	return store.EffectiveRetention{}, nil
}

func (f *fakeStore) ApplyRetention(context.Context, store.RetentionPolicy) (store.RetentionResult, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied++
	return store.RetentionResult{Deliveries: 3, ReclaimedBytes: 4096}, f.fail
}

func (f *fakeStore) ResolveRetentionRunAt(context.Context, *store.RetentionRunAtPin) (store.RetentionRunAt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return store.RetentionRunAt{Value: f.runAt}, nil
}

func (f *fakeStore) LastRetentionPass(context.Context) (*store.RetentionPass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		return nil, nil
	}
	p := *f.last
	return &p, nil
}

func (f *fakeStore) SaveRetentionPass(_ context.Context, p store.RetentionPass) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = &p
	return nil
}

func (f *fakeStore) passes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied
}

// clock is a fake wall clock that the scheduler's Wait advances. It stops
// the schedule once until is reached.
type clock struct {
	mu    sync.Mutex
	now   time.Time
	until time.Time
	// after, when set, is called with the new time after every wait.
	after func(now time.Time)
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Wait(_ context.Context, d time.Duration) bool {
	c.mu.Lock()
	if !c.now.Before(c.until) {
		c.mu.Unlock()
		return false
	}
	c.now = c.now.Add(d)
	now, after := c.now, c.after
	c.mu.Unlock()
	if after != nil {
		after(now)
	}
	return true
}

func newScheduler(f *fakeStore, c *clock) *Retention {
	return New(Options{Store: f, Location: time.UTC, Now: c.Now, Wait: c.Wait})
}

var at0330 = store.ClockTime{Hour: 3, Minute: 30}

func TestStartCatchesUpAMissedPass(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last *store.RetentionPass
		want int
	}{
		{"never ran", nil, 1},
		{"last pass before today's time", &store.RetentionPass{StartedAt: start.Add(-8 * time.Hour)}, 1},
		{"last pass failed", &store.RetentionPass{StartedAt: start.Add(-time.Hour), Error: "boom"}, 1},
		{"ran after today's time", &store.RetentionPass{StartedAt: start.Add(-6 * time.Hour)}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeStore{runAt: at0330, last: tc.last}
			// Stop before the next scheduled time, so only the catch-up counts.
			c := &clock{now: start, until: start.Add(time.Hour)}
			newScheduler(f, c).Start(context.Background())
			if got := f.passes(); got != tc.want {
				t.Fatalf("passes = %d, want %d", got, tc.want)
			}
			if tc.want == 1 && f.last.Trigger != TriggerStartup {
				t.Errorf("trigger = %q, want %q", f.last.Trigger, TriggerStartup)
			}
		})
	}
}

func TestStartRunsOnceADayAtTheConfiguredTime(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	f := &fakeStore{runAt: at0330, last: &store.RetentionPass{StartedAt: start.Add(-6 * time.Hour)}}
	var started []time.Time
	c := &clock{now: start, until: start.Add(3 * 24 * time.Hour)}
	s := newScheduler(f, c)
	s.opts.Store = recordingStore{f, &started, c}
	s.Start(context.Background())

	want := []time.Time{
		time.Date(2026, 9, 30, 3, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 3, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 2, 3, 30, 0, 0, time.UTC),
	}
	if len(started) != len(want) {
		t.Fatalf("passes at %v, want %v", started, want)
	}
	for i := range want {
		if !started[i].Equal(want[i]) {
			t.Errorf("pass %d at %v, want %v", i, started[i], want[i])
		}
	}
	if f.last.Trigger != TriggerSchedule {
		t.Errorf("trigger = %q, want %q", f.last.Trigger, TriggerSchedule)
	}
}

// recordingStore notes the fake clock at every pass.
type recordingStore struct {
	*fakeStore
	at *[]time.Time
	c  *clock
}

func (r recordingStore) ApplyRetention(ctx context.Context, p store.RetentionPolicy) (store.RetentionResult, error) {
	*r.at = append(*r.at, r.c.Now())
	return r.fakeStore.ApplyRetention(ctx, p)
}

func TestChangingTheTimeDoesNotRunASecondPassToday(t *testing.T) {
	start := time.Date(2026, 9, 29, 2, 0, 0, 0, time.UTC)
	f := &fakeStore{runAt: at0330, last: &store.RetentionPass{StartedAt: time.Date(2026, 9, 28, 3, 30, 0, 0, time.UTC)}}
	c := &clock{now: start, until: time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC)}
	// At 02:15, before today's 03:30 comes round, the time is moved to
	// 01:00, which has already passed today. Neither time runs a pass today:
	// the old one no longer applies and the new one is tomorrow's.
	c.after = func(now time.Time) {
		if now.Equal(start.Add(15 * time.Minute)) {
			f.mu.Lock()
			f.runAt = store.ClockTime{Hour: 1}
			f.mu.Unlock()
		}
	}
	var started []time.Time
	s := newScheduler(f, c)
	s.opts.Store = recordingStore{f, &started, c}
	s.Start(context.Background())
	if len(started) != 1 || !started[0].Equal(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("passes at %v, want one at 01:00 on 30 September", started)
	}
}

func TestRunRefusesASecondConcurrentPass(t *testing.T) {
	f := &fakeStore{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	s := New(Options{Store: f})

	done := make(chan error, 1)
	go func() {
		_, err := s.Run(context.Background(), TriggerManual)
		done <- err
	}()
	<-f.entered
	if !s.Running() {
		t.Error("Running() = false while a pass is in progress")
	}
	if _, err := s.Run(context.Background(), TriggerSchedule); !errors.Is(err, ErrPassRunning) {
		t.Fatalf("second Run: err = %v, want ErrPassRunning", err)
	}
	close(f.block)
	if err := <-done; err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if s.Running() {
		t.Error("Running() = true after the pass finished")
	}
	if got := f.passes(); got != 1 {
		t.Errorf("passes = %d, want 1", got)
	}
	// And the lock is released: a later pass runs.
	f.block, f.entered = nil, nil
	if _, err := s.Run(context.Background(), TriggerManual); err != nil {
		t.Fatalf("Run after release: %v", err)
	}
}

func TestRunRecordsTheOutcome(t *testing.T) {
	f := &fakeStore{fail: errors.New("disk I/O error")}
	failures := 0
	s := New(Options{Store: f, OnFailure: func() { failures++ }})

	pass, err := s.Run(context.Background(), TriggerManual)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pass.Error != "disk I/O error" || failures != 1 {
		t.Errorf("failed pass: Error %q, failures %d", pass.Error, failures)
	}
	if f.last == nil || f.last.Error != pass.Error || f.last.Deliveries != 3 || f.last.FreedBytes != 4096 {
		t.Errorf("recorded %+v, want the failed pass with its counts", f.last)
	}

	f.fail = nil
	if pass, _ = s.Run(context.Background(), TriggerManual); !pass.Succeeded() || failures != 1 {
		t.Errorf("clean pass: %+v, failures %d", pass, failures)
	}
}

func TestStartRunsAgainstARealStore(t *testing.T) {
	db, err := store.Open(context.Background(), store.Options{Path: t.TempDir() + "/h.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	c := &clock{now: start, until: start}
	New(Options{Store: db, Location: time.UTC, Now: c.Now, Wait: c.Wait}).Start(context.Background())

	last, err := db.LastRetentionPass(context.Background())
	if err != nil || last == nil {
		t.Fatalf("no pass recorded after a first start: %+v, %v", last, err)
	}
	if !last.Succeeded() || last.Trigger != TriggerStartup || !last.StartedAt.Equal(start) {
		t.Errorf("recorded %+v", last)
	}
}
