package state

import (
	"sync"
	"testing"
	"time"
)

// clock is a controllable time source. Every test here drives time explicitly
// so that flapping windows are exact rather than racy.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// step is one check fed to the engine, plus what we expect back.
type step struct {
	ok      bool
	advance time.Duration

	wantTo         Status
	wantEvent      Event
	wantNotify     bool
	wantSuppressed bool
	wantFails      int
}

// runSteps feeds a sequence through one engine and checks each transition.
func runSteps(t *testing.T, e *Engine, c *clock, threshold int, steps []step) {
	t.Helper()

	for i, s := range steps {
		if s.advance > 0 {
			c.advance(s.advance)
		}

		obs := Observation{
			MonitorID:        1,
			OK:               s.ok,
			At:               c.Now(),
			FailureThreshold: threshold,
		}
		if !s.ok {
			obs.Kind = "status"
			obs.Error = "unexpected status 500"
		}

		got := e.Observe(obs)

		if got.To != s.wantTo {
			t.Errorf("step %d: status = %q, want %q", i, got.To, s.wantTo)
		}
		if got.Event != s.wantEvent {
			t.Errorf("step %d: event = %q, want %q", i, got.Event, s.wantEvent)
		}
		if got.Notify != s.wantNotify {
			t.Errorf("step %d: notify = %v, want %v", i, got.Notify, s.wantNotify)
		}
		if got.Suppressed != s.wantSuppressed {
			t.Errorf("step %d: suppressed = %v, want %v", i, got.Suppressed, s.wantSuppressed)
		}
		if s.wantFails != 0 && got.ConsecutiveFails != s.wantFails {
			t.Errorf("step %d: consecutive fails = %d, want %d", i, got.ConsecutiveFails, s.wantFails)
		}
	}
}

func TestTransitions(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
		steps     []step
	}{
		{
			// The default path: a monitor that comes up and stays up should
			// produce exactly one transition and then go quiet.
			name:      "first success goes up once, then silent",
			threshold: 2,
			steps: []step{
				{ok: true, wantTo: StatusUp, wantEvent: EventNone},
				{ok: true, wantTo: StatusUp, wantEvent: EventNone},
				{ok: true, wantTo: StatusUp, wantEvent: EventNone},
			},
		},
		{
			// The core of product principle 5. One failure must not alert.
			name:      "single failure stays pending and does not notify",
			threshold: 2,
			steps: []step{
				{ok: true, wantTo: StatusUp},
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: true, wantTo: StatusUp, wantEvent: EventIncidentResolved, wantNotify: false},
			},
		},
		{
			name:      "two failures confirm and notify",
			threshold: 2,
			steps: []step{
				{ok: true, wantTo: StatusUp},
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: false, wantTo: StatusDown, wantEvent: EventIncidentConfirmed, wantNotify: true, wantFails: 2},
			},
		},
		{
			// Threshold 1 is "alert immediately". The incident must still be
			// opened before it is confirmed, so the record is never orphaned.
			name:      "threshold one confirms on the first failure",
			threshold: 1,
			steps: []step{
				{ok: true, wantTo: StatusUp},
				{ok: false, wantTo: StatusDown, wantEvent: EventIncidentConfirmed, wantNotify: true, wantFails: 1},
			},
		},
		{
			name:      "staying down is silent",
			threshold: 2,
			steps: []step{
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened},
				{ok: false, wantTo: StatusDown, wantEvent: EventIncidentConfirmed, wantNotify: true},
				{ok: false, wantTo: StatusDown, wantEvent: EventNone, wantFails: 3},
				{ok: false, wantTo: StatusDown, wantEvent: EventNone, wantFails: 4},
			},
		},
		{
			name:      "recovery from confirmed down notifies",
			threshold: 2,
			steps: []step{
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened},
				{ok: false, wantTo: StatusDown, wantEvent: EventIncidentConfirmed, wantNotify: true},
				{ok: true, wantTo: StatusUp, wantEvent: EventIncidentResolved, wantNotify: true},
				{ok: true, wantTo: StatusUp, wantEvent: EventNone},
			},
		},
		{
			// A failure streak that recovers before the threshold must leave
			// no trace a human hears about.
			name:      "pending recovery is silent both ways",
			threshold: 3,
			steps: []step{
				{ok: true, wantTo: StatusUp},
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: false, wantTo: StatusPending, wantEvent: EventNone, wantFails: 2},
				{ok: true, wantTo: StatusUp, wantEvent: EventIncidentResolved, wantNotify: false},
			},
		},
		{
			// The streak must reset on success, not accumulate across
			// recoveries — otherwise a monitor with occasional blips would
			// eventually alert for no reason.
			name:      "failure streak resets on success",
			threshold: 3,
			steps: []step{
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: false, wantTo: StatusPending, wantFails: 2},
				{ok: true, wantTo: StatusUp, wantEvent: EventIncidentResolved},
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: false, wantTo: StatusPending, wantFails: 2},
			},
		},
		{
			// First-ever observation being a failure: no prior "up" to fall
			// from, but the incident lifecycle must still work.
			name:      "first observation failing opens an incident",
			threshold: 2,
			steps: []step{
				{ok: false, wantTo: StatusPending, wantEvent: EventIncidentOpened, wantFails: 1},
				{ok: false, wantTo: StatusDown, wantEvent: EventIncidentConfirmed, wantNotify: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newClock()
			// A flap threshold well above anything these cases produce keeps
			// them focused on the confirmation logic.
			e := New(Options{Now: c.Now, FlapThreshold: 99})
			runSteps(t, e, c, tc.threshold, tc.steps)
		})
	}
}

func TestFlappingSuppression(t *testing.T) {
	c := newClock()
	e := New(Options{
		Now:           c.Now,
		FlapWindow:    10 * time.Minute,
		FlapThreshold: 4, // two full down/up cycles
	})

	obs := func(ok bool) Transition {
		c.advance(30 * time.Second)
		return e.Observe(Observation{
			MonitorID:        1,
			OK:               ok,
			At:               c.Now(),
			FailureThreshold: 1,
		})
	}

	// Cycle 1: down (change 1), up (change 2). Both notify normally.
	if tr := obs(false); !tr.Notify || tr.Event != EventIncidentConfirmed {
		t.Fatalf("first down: got event=%q notify=%v, want confirmed+notify", tr.Event, tr.Notify)
	}
	if tr := obs(true); !tr.Notify || tr.Event != EventIncidentResolved {
		t.Fatalf("first recovery: got event=%q notify=%v, want resolved+notify", tr.Event, tr.Notify)
	}

	// Cycle 2: down (change 3), up (change 4) — the fourth change trips the
	// threshold. The recovery is still reported as a resolve, because the
	// incident really did close; flapping is signalled alongside it.
	if tr := obs(false); !tr.Notify || tr.Event != EventIncidentConfirmed {
		t.Fatalf("second down: got event=%q notify=%v", tr.Event, tr.Notify)
	}

	tr := obs(true)
	if tr.Event != EventIncidentResolved {
		t.Fatalf("the incident must still resolve when flapping trips, got event=%q", tr.Event)
	}
	if !tr.Flapping || !tr.FlappingChanged {
		t.Errorf("expected flapping to start on the 4th change: flapping=%v changed=%v",
			tr.Flapping, tr.FlappingChanged)
	}
	if !tr.Notify {
		t.Error("flapping start should notify once, so the user knows why alerts stopped")
	}
	if !e.Flapping(1) {
		t.Error("engine should report the monitor as flapping")
	}

	// Further oscillation is written to state but withheld from the user.
	tr = obs(false)
	if tr.Notify {
		t.Error("notification during flapping should be withheld")
	}
	if !tr.Suppressed {
		t.Error("withheld notification must be marked Suppressed, not silently dropped")
	}
	if tr.To != StatusDown {
		t.Errorf("state must still advance during flapping: got %q", tr.To)
	}
	// The event still has to reach the persistence layer, or the incident
	// would never be recorded.
	if tr.Event != EventIncidentConfirmed {
		t.Errorf("suppression must not erase the event: got %q", tr.Event)
	}
}

func TestFlappingEndsAfterWindow(t *testing.T) {
	c := newClock()
	e := New(Options{
		Now:           c.Now,
		FlapWindow:    5 * time.Minute,
		FlapThreshold: 2,
	})

	// Two changes inside the window: down, then up. That trips flapping.
	e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 1})
	c.advance(time.Second)
	e.Observe(Observation{MonitorID: 1, OK: true, At: c.Now(), FailureThreshold: 1})

	if !e.Flapping(1) {
		t.Fatal("expected flapping after two rapid changes")
	}

	// Quiet for longer than the window; the old flips age out.
	c.advance(6 * time.Minute)
	tr := e.Observe(Observation{MonitorID: 1, OK: true, At: c.Now(), FailureThreshold: 1})

	if e.Flapping(1) {
		t.Error("flapping should end once old changes fall outside the window")
	}
	if tr.Flapping {
		t.Error("transition should report flapping as over")
	}
	if !tr.FlappingChanged {
		t.Error("expected the flapping status change to be signalled")
	}
	if !tr.Notify {
		t.Error("flapping ending should notify so the user knows alerts resumed")
	}
	// Nothing happened to the incident on this check, so there is no event to
	// report — flapping travels in its own fields.
	if tr.Event != EventNone {
		t.Errorf("event = %q, want none: no incident changed on this check", tr.Event)
	}
}

func TestRestoreSuppressesDuplicateAlert(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	// Simulate a restart while an incident was already confirmed.
	e.Restore(1, StatusDown, true, true)

	if got := e.Status(1); got != StatusDown {
		t.Fatalf("restored status = %q, want down", got)
	}

	// The next failed check must not re-announce an outage the user already
	// knows about.
	tr := e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 2})
	if tr.Notify {
		t.Error("a restart must not re-alert for an already-confirmed incident")
	}
	if tr.Event != EventNone {
		t.Errorf("expected no event, got %q", tr.Event)
	}

	// Recovery still resolves properly.
	tr = e.Observe(Observation{MonitorID: 1, OK: true, At: c.Now(), FailureThreshold: 2})
	if tr.Event != EventIncidentResolved || !tr.Notify {
		t.Errorf("recovery after restore: event=%q notify=%v, want resolved+notify", tr.Event, tr.Notify)
	}
}

func TestPerMonitorIsolation(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	// Monitor 1 fails twice, monitor 2 succeeds throughout. Their streaks
	// must not touch each other.
	e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 2})
	e.Observe(Observation{MonitorID: 2, OK: true, At: c.Now(), FailureThreshold: 2})
	tr := e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 2})

	if tr.Event != EventIncidentConfirmed {
		t.Errorf("monitor 1 should confirm, got %q", tr.Event)
	}
	if got := e.Status(2); got != StatusUp {
		t.Errorf("monitor 2 status = %q, want up", got)
	}
}

func TestForgetDropsState(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 1})
	if e.Status(1) != StatusDown {
		t.Fatal("expected monitor to be down")
	}

	e.Forget(1)
	if got := e.Status(1); got != StatusUnknown {
		t.Errorf("after Forget, status = %q, want unknown", got)
	}
}

// Retain is what keeps the engine's map bounded. Monitors are deleted and
// paused through several code paths; reconciling against the authoritative
// set on every reload cannot drift the way per-path Forget calls would.
func TestRetainDropsMonitorsNotInLiveSet(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	for id := int64(1); id <= 5; id++ {
		e.Observe(Observation{MonitorID: id, OK: false, At: c.Now(), FailureThreshold: 1})
	}
	if got := e.Len(); got != 5 {
		t.Fatalf("tracking %d monitors, want 5", got)
	}

	// Monitors 2 and 4 survive; the rest were deleted or paused.
	e.Retain(map[int64]struct{}{2: {}, 4: {}})

	if got := e.Len(); got != 2 {
		t.Errorf("after Retain, tracking %d monitors, want 2", got)
	}
	for _, id := range []int64{2, 4} {
		if got := e.Status(id); got != StatusDown {
			t.Errorf("monitor %d status = %q, want down (it should have survived)", id, got)
		}
	}
	for _, id := range []int64{1, 3, 5} {
		if got := e.Status(id); got != StatusUnknown {
			t.Errorf("monitor %d status = %q, want unknown (it should have been dropped)", id, got)
		}
	}
}

// An empty live set means every monitor is gone — the map must empty, not be
// left untouched by a guard clause that mistakes empty for "no information".
func TestRetainWithEmptyLiveSetDropsEverything(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 1})
	e.Observe(Observation{MonitorID: 2, OK: true, At: c.Now(), FailureThreshold: 1})

	e.Retain(map[int64]struct{}{})

	if got := e.Len(); got != 0 {
		t.Errorf("tracking %d monitors after an empty Retain, want 0", got)
	}
}

// A monitor that is paused mid-incident and later resumed must start clean:
// resuming a failure streak from before the pause would alert on the first
// failure after a resume, which is exactly the false alarm this product
// promises not to send.
func TestRetainClearsFailureStreak(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now, FlapThreshold: 99})

	// Two failures with a threshold of 3: pending, one short of confirming.
	e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 3})
	tr := e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 3})
	if tr.ConsecutiveFails != 2 {
		t.Fatalf("streak = %d, want 2", tr.ConsecutiveFails)
	}

	// Paused, then resumed.
	e.Retain(map[int64]struct{}{})

	tr = e.Observe(Observation{MonitorID: 1, OK: false, At: c.Now(), FailureThreshold: 3})
	if tr.ConsecutiveFails != 1 {
		t.Errorf("streak after resume = %d, want 1 — the old streak survived", tr.ConsecutiveFails)
	}
	if tr.Notify {
		t.Error("first failure after a resume must not alert")
	}
}

func TestRetainIsSafeUnderConcurrentObserve(t *testing.T) {
	e := New(Options{})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range 200 {
			e.Observe(Observation{
				MonitorID:        int64(i % 20),
				OK:               i%2 == 0,
				At:               time.Now(),
				FailureThreshold: 2,
			})
		}
	}()

	go func() {
		defer wg.Done()
		for range 200 {
			e.Retain(map[int64]struct{}{1: {}, 2: {}, 3: {}})
			e.Len()
		}
	}()

	wg.Wait()
}

func TestCausePropagates(t *testing.T) {
	c := newClock()
	e := New(Options{Now: c.Now})

	tr := e.Observe(Observation{
		MonitorID:        1,
		OK:               false,
		At:               c.Now(),
		FailureThreshold: 1,
		Kind:             "tls",
		Error:            "certificate has expired",
	})

	// The notifier needs to say what broke, not just that something did.
	if tr.Cause != "tls" {
		t.Errorf("cause = %q, want tls", tr.Cause)
	}
	if tr.Error != "certificate has expired" {
		t.Errorf("error = %q, want the checker's message", tr.Error)
	}
}

// The scheduler reports from a worker pool, so concurrent Observe calls are
// the normal case rather than an edge case. Run with -race.
func TestConcurrentObserveIsSafe(t *testing.T) {
	e := New(Options{})

	var wg sync.WaitGroup
	for id := int64(1); id <= 20; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for i := range 50 {
				e.Observe(Observation{
					MonitorID:        id,
					OK:               i%3 != 0,
					At:               time.Now(),
					FailureThreshold: 2,
				})
			}
			e.Status(id)
			e.Flapping(id)
		}(id)
	}
	wg.Wait()
}
