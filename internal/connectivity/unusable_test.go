package connectivity

import (
	"context"
	"sync"
	"testing"
	"time"
)

// unusableLog records the OnUnusable callbacks.
type unusableLog struct {
	mu      sync.Mutex
	reasons []Unusable
}

func (l *unusableLog) record(r Unusable) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reasons = append(l.reasons, r)
}

func (l *unusableLog) get() []Unusable {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Unusable(nil), l.reasons...)
}

// Rule one, setup A of the ticket: a network that may only see the LAN, so
// the targets have never answered. Their failing says nothing about the host,
// so Offline holds nothing back, round after round, and the targets are
// reported unusable once.
func TestTargetsThatNeverAnsweredHoldNothingBack(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	log := &unusableLog{}
	c := newUnprovenCanary(t, fn, clk, nil, log.record)
	ctx := context.Background()

	start := clk.now()
	c.Sweep(ctx) // what Run does first
	for range 5 {
		if c.Offline(ctx) {
			t.Fatal("targets that never answered called the host offline")
		}
		if _, offline := c.OfflineSince(); offline {
			t.Fatal("targets that never answered started an offline episode")
		}
		clk.advance(DefaultCacheFor)
	}
	reason, since, unusable := c.TargetsUnusable()
	if !unusable || reason != Unanswered || !since.Equal(start) {
		t.Fatalf("TargetsUnusable = %q, %v, %v; want %q since %v", reason, since, unusable, Unanswered, start)
	}
	if got := log.get(); len(got) != 1 || got[0] != Unanswered {
		t.Fatalf("OnUnusable calls = %v, want one %q", got, Unanswered)
	}
	// An Offline asked inside a round's cache window reuses it, as for
	// any round: unusable targets are not dialled once per failure.
	before := fn.dials.Load()
	c.Offline(ctx)
	clk.advance(time.Second)
	c.Offline(ctx)
	if got := fn.dials.Load() - before; got != 2 {
		t.Fatalf("two questions in one window dialled %d times, want one round of 2", got)
	}
}

// Run's first sweep is the one place the canary dials with nothing failing:
// once, so that targets which answer are known to before the first outage.
// After that a healthy host sends nothing.
func TestTheStartingSweepProvesTheTargetsOnce(t *testing.T) {
	fn := &fakeNet{}
	fn.set(targetA, true)
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newUnprovenCanary(t, fn, clk, nil, nil)
	ctx := context.Background()

	c.Sweep(ctx)
	if got := fn.dials.Load(); got != 2 {
		t.Fatalf("the starting sweep dialled %d times, want one round of 2", got)
	}
	for range 3 {
		clk.advance(DefaultCacheFor)
		c.Sweep(ctx)
	}
	if got := fn.dials.Load(); got != 2 {
		t.Fatalf("sweeps on a healthy host dialled %d more times", got-2)
	}
	// Proven, so a later loss of every target is the host's, as before.
	fn.set(targetA, false)
	if !c.Offline(ctx) {
		t.Fatal("proven targets all failing did not call the host offline")
	}
}

// Targets that start answering later, once the host's network comes up after
// a cold start, speak for it from then on.
func TestTargetsThatAnswerLaterAreTrusted(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newUnprovenCanary(t, fn, clk, nil, nil)
	ctx := context.Background()

	if c.Offline(ctx) {
		t.Fatal("offline before any target answered")
	}
	fn.set(targetB, true)
	clk.advance(DefaultCacheFor)
	if c.Offline(ctx) {
		t.Fatal("offline with a target answering")
	}
	if _, _, unusable := c.TargetsUnusable(); unusable {
		t.Fatal("targets still unusable after one answered")
	}
	fn.set(targetB, false)
	clk.advance(DefaultCacheFor)
	if !c.Offline(ctx) {
		t.Fatal("trusted targets all failing did not call the host offline")
	}
}

// Rule two, setup B of the ticket: the targets answered once, then a firewall
// started dropping them. A check that reaches a public address during the
// episode ends it without a notice, and the targets stand contradicted.
func TestAPublicConnectionEndsAnEpisodeTheTargetsCannotSee(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	restored := 0
	log := &unusableLog{}
	c := newUnprovenCanary(t, fn, clk, func(time.Time, time.Time) { restored++ }, log.record)
	prove(t, c, fn, clk)
	ctx := context.Background()

	if !c.Offline(ctx) {
		t.Fatal("not offline with every target down")
	}
	clk.advance(10 * time.Second)
	before := fn.dials.Load()
	c.PublicReached(ctx, clk.now())
	if got := fn.dials.Load() - before; got != 2 {
		t.Fatalf("evidence against the episode dialled %d times, want one round of 2", got)
	}
	if _, offline := c.OfflineSince(); offline {
		t.Fatal("the episode survived a check that reached the internet")
	}
	if restored != 0 {
		t.Fatalf("a contradicted episode sent %d restored notices; the host was never offline", restored)
	}
	reason, since, unusable := c.TargetsUnusable()
	if !unusable || reason != Contradicted || !since.Equal(clk.now()) {
		t.Fatalf("TargetsUnusable = %q, %v, %v; want %q now", reason, since, unusable, Contradicted)
	}
	if got := log.get(); len(got) != 1 || got[0] != Contradicted {
		t.Fatalf("OnUnusable calls = %v, want one %q", got, Contradicted)
	}

	// The next network failure, inside the window, reads the fresh answer
	// and holds nothing back; after it, a new round still holds nothing
	// back, because the targets are unproven again.
	before = fn.dials.Load()
	if c.Offline(ctx) {
		t.Fatal("offline right after the targets were contradicted")
	}
	if fn.dials.Load() != before {
		t.Fatal("the answer just found was not reused")
	}
	clk.advance(DefaultCacheFor)
	if c.Offline(ctx) {
		t.Fatal("contradicted targets called the host offline")
	}
	// Further evidence changes nothing and dials nothing.
	before = fn.dials.Load()
	for range 10 {
		c.PublicReached(ctx, clk.now())
	}
	if fn.dials.Load() != before || len(log.get()) != 1 {
		t.Fatalf("evidence against contradicted targets dialled %d times and called back %d times",
			fn.dials.Load()-before, len(log.get()))
	}

	// The firewall is fixed: the targets answer, and speak for the host again.
	fn.set(targetA, true)
	clk.advance(DefaultCacheFor)
	c.Offline(ctx)
	if _, _, unusable := c.TargetsUnusable(); unusable {
		t.Fatal("still unusable after a target answered")
	}
	fn.set(targetA, false)
	clk.advance(DefaultCacheFor)
	if !c.Offline(ctx) {
		t.Fatal("trusted again, the targets all failing did not call the host offline")
	}
}

// When the targets answer again by the time the evidence comes in, the
// episode was real and ends the usual way, with its notice.
func TestAPublicConnectionWithTheTargetsBackEndsTheEpisodeWithItsNotice(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	restored := 0
	c := newTestCanary(t, fn, clk, func(time.Time, time.Time) { restored++ })
	ctx := context.Background()

	c.Offline(ctx)
	fn.set(targetA, true)
	clk.advance(5 * time.Second)
	c.PublicReached(ctx, clk.now())
	if _, offline := c.OfflineSince(); offline || restored != 1 {
		t.Fatalf("offline=%v restored=%d, want the episode over with one notice", offline, restored)
	}
	if _, _, unusable := c.TargetsUnusable(); unusable {
		t.Fatal("targets that answered were marked unusable")
	}
}

// Evidence is only evidence when it is newer than what it contradicts, and
// only asked for when there is something to contradict.
func TestAPublicConnectionIsOnlyEvidenceAgainstAnOlderEpisode(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCanary(t, fn, clk, nil)
	ctx := context.Background()

	// Healthy: nothing to contradict, nothing dialled, however many checks pass.
	fn.set(targetA, true)
	for range 10 {
		c.PublicReached(ctx, clk.now())
	}
	if got := fn.dials.Load(); got != 0 {
		t.Fatalf("passing checks on a healthy host dialled %d times", got)
	}

	fn.set(targetA, false)
	clk.advance(DefaultCacheFor)
	started := clk.now()
	if !c.Offline(ctx) {
		t.Fatal("not offline")
	}
	// A check that began before the episode may have connected before the
	// connection went.
	before := fn.dials.Load()
	c.PublicReached(ctx, started.Add(-time.Second))
	c.PublicReached(ctx, started)
	if fn.dials.Load() != before {
		t.Fatal("a check older than the episode was taken as evidence")
	}
	if _, offline := c.OfflineSince(); !offline {
		t.Fatal("a check older than the episode ended it")
	}
}

// Targets that never answered and then fail while a check reaches the
// internet are named for the stronger reason.
func TestUnansweredTargetsContradictedByAPublicConnection(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	log := &unusableLog{}
	c := newUnprovenCanary(t, fn, clk, nil, log.record)
	ctx := context.Background()

	c.Sweep(ctx)
	clk.advance(time.Second)
	c.PublicReached(ctx, clk.now())
	if reason, _, _ := c.TargetsUnusable(); reason != Contradicted {
		t.Fatalf("reason = %q, want %q", reason, Contradicted)
	}
	if got := log.get(); len(got) != 2 || got[0] != Unanswered || got[1] != Contradicted {
		t.Fatalf("OnUnusable calls = %v, want %q then %q", got, Unanswered, Contradicted)
	}
	if c.Offline(ctx) {
		t.Fatal("offline with contradicted targets")
	}
}

// New targets start unproven and the next sweep tries them; switching the
// check off and on keeps what the same targets proved.
func TestConfigureAndTheTargetsStanding(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newUnprovenCanary(t, fn, clk, nil, nil)
	ctx := context.Background()

	c.Sweep(ctx)
	if _, _, unusable := c.TargetsUnusable(); !unusable {
		t.Fatal("not unusable")
	}
	if err := c.Configure(Settings{Enabled: false, Targets: []string{targetA, targetB}}); err != nil {
		t.Fatal(err)
	}
	if _, _, unusable := c.TargetsUnusable(); unusable {
		t.Fatal("a switched-off check reports its targets unusable")
	}

	fn.set("gateway:443", true)
	if err := c.Configure(Settings{Enabled: true, Targets: []string{"gateway:443"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, unusable := c.TargetsUnusable(); unusable {
		t.Fatal("new targets inherited the old ones' standing")
	}
	before := fn.dials.Load()
	c.Sweep(ctx)
	if got := fn.dials.Load() - before; got != 1 {
		t.Fatalf("the sweep after a change dialled %d times, want one round of 1", got)
	}

	fn.set("gateway:443", false)
	if err := c.Configure(Settings{Enabled: false, Targets: []string{"gateway:443"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Configure(Settings{Enabled: true, Targets: []string{"gateway:443"}}); err != nil {
		t.Fatal(err)
	}
	clk.advance(DefaultCacheFor)
	if !c.Offline(ctx) {
		t.Fatal("switched off and on, targets that had answered no longer speak for the host")
	}
}
