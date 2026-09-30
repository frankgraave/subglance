package connectivity

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A canary switched off must not dial at all: the operator turned it off
// because they do not want this host reaching those addresses.
func TestDisabledCanaryNeverDialsAndNeverHoldsAFailureBack(t *testing.T) {
	fn := &fakeNet{}
	c, err := New(Options{
		Targets:  []string{targetA, targetB},
		Disabled: true,
		Dial:     fn.dial,
		Now:      (&fakeClock{t: time.Unix(1000, 0)}).now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled() {
		t.Fatal("Disabled: true built an enabled canary")
	}
	if c.Offline(context.Background()) {
		t.Fatal("a disabled canary reported the host offline")
	}
	c.Sweep(context.Background())
	if got := fn.dials.Load(); got != 0 {
		t.Fatalf("a disabled canary dialled %d times", got)
	}
}

// Changing the targets forgets what the old ones said, and ends an episode
// quietly: nothing was reached, so a "back online" notice would be false.
func TestConfigureForgetsTheOldAnswer(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	restored := 0
	c := newTestCanary(t, fn, clk, func(time.Time, time.Time) { restored++ })
	ctx := context.Background()

	if !c.Offline(ctx) {
		t.Fatal("not offline with every target down")
	}
	fn.set("gateway:443", true)
	if err := c.Configure(Settings{Enabled: true, Targets: []string{"gateway:443"}}); err != nil {
		t.Fatal(err)
	}
	if _, offline := c.OfflineSince(); offline {
		t.Fatal("the episode measured against the old targets survived a change of targets")
	}
	if restored != 0 {
		t.Fatalf("a change of targets reported the host back online %d times", restored)
	}
	// Inside the old cache window, and still a fresh round against the new
	// target: the cached answer was about the old ones.
	before := fn.dials.Load()
	if c.Offline(ctx) {
		t.Fatal("offline with the new target reachable")
	}
	if got := fn.dials.Load() - before; got != 1 {
		t.Fatalf("the first question after a change dialled %d times, want one round of 1", got)
	}
	if got := c.Targets(); len(got) != 1 || got[0] != "gateway:443" {
		t.Fatalf("Targets() = %v", got)
	}
}

// Configuring what is already configured changes nothing, so a save of an
// unrelated field cannot cut a real episode short.
func TestConfigureWithTheSameSettingsKeepsTheEpisode(t *testing.T) {
	fn := &fakeNet{}
	c := newTestCanary(t, fn, &fakeClock{t: time.Unix(1000, 0)}, nil)
	c.Offline(context.Background())
	start, _ := c.OfflineSince()
	if err := c.Configure(c.Settings()); err != nil {
		t.Fatal(err)
	}
	if since, offline := c.OfflineSince(); !offline || !since.Equal(start) {
		t.Fatalf("an unchanged Configure ended the episode: %v %v", since, offline)
	}
}

func TestConfigureOffAndOnAgain(t *testing.T) {
	fn := &fakeNet{}
	c := newTestCanary(t, fn, &fakeClock{t: time.Unix(1000, 0)}, nil)
	ctx := context.Background()
	c.Offline(ctx)

	if err := c.Configure(Settings{Enabled: false, Targets: []string{targetA, targetB}}); err != nil {
		t.Fatal(err)
	}
	if _, offline := c.OfflineSince(); offline {
		t.Fatal("still offline after the check was turned off")
	}
	before := fn.dials.Load()
	if c.Offline(ctx) {
		t.Fatal("offline while switched off")
	}
	if fn.dials.Load() != before {
		t.Fatal("dialled while switched off")
	}

	if err := c.Configure(Settings{Enabled: true, Targets: []string{targetA, targetB}}); err != nil {
		t.Fatal(err)
	}
	if !c.Offline(ctx) {
		t.Fatal("turned back on, the canary did not measure again")
	}
}

// A bad list is refused whole, and the canary keeps what it had. It is judged
// even when turning the check off, so it can never be turned back on with a
// list it would refuse.
func TestConfigureRefusesABadListAndKeepsTheOldOne(t *testing.T) {
	c := newTestCanary(t, &fakeNet{}, &fakeClock{t: time.Unix(1000, 0)}, nil)
	for _, bad := range [][]string{nil, {"gateway"}, {"gate,way:53"}, make([]string, MaxTargets+1)} {
		if err := c.Configure(Settings{Enabled: false, Targets: bad}); err == nil {
			t.Fatalf("Configure accepted %q", bad)
		}
	}
	if s := c.Settings(); !s.Enabled || len(s.Targets) != 2 {
		t.Fatalf("a refused Configure changed the canary: %+v", s)
	}
}

// A round that was dialling the old targets when the change landed must not
// be recorded against the new ones.
func TestARoundInFlightDuringConfigureIsDiscarded(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "canary-a") && first.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
		return nil, context.DeadlineExceeded
	}
	c, err := New(Options{
		Targets: []string{targetA},
		Dial:    dial,
		Now:     (&fakeClock{t: time.Unix(1000, 0)}).now,
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan bool)
	go func() { done <- c.Offline(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the round never dialled")
	}
	if err := c.Configure(Settings{Enabled: true, Targets: []string{"gateway:443"}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if <-done {
		t.Fatal("a round against the old targets reported the host offline after the change")
	}
	if _, offline := c.OfflineSince(); offline {
		t.Fatal("a round against the old targets started an episode after the change")
	}
}
