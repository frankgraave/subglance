package connectivity

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeNet answers dials without touching the network: reachable says which
// targets connect, and every dial is counted.
type fakeNet struct {
	mu        sync.Mutex
	reachable map[string]bool
	dials     atomic.Int64
}

func (f *fakeNet) set(target string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reachable == nil {
		f.reachable = map[string]bool{}
	}
	f.reachable[target] = ok
}

func (f *fakeNet) dial(_ context.Context, _, address string) (net.Conn, error) {
	f.dials.Add(1)
	f.mu.Lock()
	ok := f.reachable[address]
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("network is unreachable")
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const targetA, targetB = "canary-a:53", "canary-b:53"

func newTestCanary(t *testing.T, fn *fakeNet, clk *fakeClock, onRestored func(from, to time.Time)) *Canary {
	t.Helper()
	c, err := New(Options{
		Targets:    []string{targetA, targetB},
		Dial:       fn.dial,
		Now:        clk.now,
		OnRestored: onRestored,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOfflineOnlyWhenEveryTargetFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a, b    bool
		offline bool
	}{
		{"both reachable", true, true, false},
		{"one reachable", false, true, false},
		{"other reachable", true, false, false},
		{"none reachable", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &fakeNet{}
			fn.set(targetA, tc.a)
			fn.set(targetB, tc.b)
			c := newTestCanary(t, fn, &fakeClock{t: time.Unix(1000, 0)}, nil)
			if got := c.Offline(context.Background()); got != tc.offline {
				t.Fatalf("Offline = %v, want %v", got, tc.offline)
			}
		})
	}
}

// Twenty monitors failing together must cost one round, not twenty.
func TestOneRoundIsSharedWithinTheCacheWindow(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCanary(t, fn, clk, nil)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !c.Offline(context.Background()) {
				t.Error("reported online with every target down")
			}
		}()
	}
	wg.Wait()
	if got := fn.dials.Load(); got != 2 {
		t.Fatalf("20 concurrent callers dialled %d times, want one round of 2", got)
	}

	clk.advance(DefaultCacheFor - time.Second)
	c.Offline(context.Background())
	if got := fn.dials.Load(); got != 2 {
		t.Fatalf("a call inside the cache window dialled again: %d dials", got)
	}

	clk.advance(time.Second)
	c.Offline(context.Background())
	if got := fn.dials.Load(); got != 4 {
		t.Fatalf("a call after the cache window did not start a new round: %d dials", got)
	}
}

func TestRestoredIsReportedOncePerEpisodeWithItsBounds(t *testing.T) {
	fn := &fakeNet{}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	type episode struct{ from, to time.Time }
	var got []episode
	c := newTestCanary(t, fn, clk, func(from, to time.Time) { got = append(got, episode{from, to}) })
	ctx := context.Background()

	start := clk.now()
	if !c.Offline(ctx) {
		t.Fatal("not offline")
	}
	if since, ok := c.OfflineSince(); !ok || !since.Equal(start) {
		t.Fatalf("OfflineSince = %v, %v; want %v", since, ok, start)
	}

	// Still down on the next sweep: the episode keeps its start.
	clk.advance(DefaultCacheFor)
	c.Sweep(ctx)
	if since, _ := c.OfflineSince(); !since.Equal(start) {
		t.Fatalf("a second failing round moved the start to %v", since)
	}

	fn.set(targetB, true)
	clk.advance(DefaultCacheFor)
	back := clk.now()
	c.Sweep(ctx)
	if len(got) != 1 || !got[0].from.Equal(start) || !got[0].to.Equal(back) {
		t.Fatalf("restored callbacks = %+v, want one from %v to %v", got, start, back)
	}
	if _, ok := c.OfflineSince(); ok {
		t.Fatal("still offline after a passing round")
	}

	clk.advance(DefaultCacheFor)
	c.Offline(ctx)
	if len(got) != 1 {
		t.Fatalf("a second passing round reported again: %+v", got)
	}
}

// While the host is online the canary must stay silent on its own: traffic
// only at the moment an alert would go out.
func TestSweepSendsNothingWhileOnline(t *testing.T) {
	fn := &fakeNet{}
	fn.set(targetA, true)
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCanary(t, fn, clk, nil)

	c.Sweep(context.Background())
	if got := fn.dials.Load(); got != 0 {
		t.Fatalf("a sweep with no suspected outage dialled %d times", got)
	}
	c.Offline(context.Background())
	clk.advance(DefaultCacheFor)
	before := fn.dials.Load()
	c.Sweep(context.Background())
	if got := fn.dials.Load(); got != before {
		t.Fatalf("a sweep while online dialled %d more times", got-before)
	}
}

// A round cut short by shutdown says nothing about the network.
func TestCancelledRoundDoesNotStartAnEpisode(t *testing.T) {
	fn := &fakeNet{}
	c := newTestCanary(t, fn, &fakeClock{t: time.Unix(1000, 0)}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Offline(ctx) {
		t.Fatal("a cancelled round reported the host offline")
	}
	if _, ok := c.OfflineSince(); ok {
		t.Fatal("a cancelled round started an offline episode")
	}
}

func TestValidateTargets(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"canary-a:53", true},
		{"canary-a:53, canary-b:443", true},
		{"[2001:db8::1]:53", true},
		{"", false},
		{" , ", false},
		{"canary-a", false},
		{":53", false},
		{"canary-a:0", false},
		{"canary-a:dns", false},
		{"canary-a:70000", false},
		// A host the resolver refuses without asking: every dial fails
		// before a packet is sent, so the canary would call the host
		// offline in every round.
		{"exa..mple.com:443", false},
		{"-canary.example:443", false},
		{"999.1.1.1:53", false},
		{"canary.onion:443", false},
		{"b\u00fccher.example:443", false},
		{"canary-a:53, exa..mple.com:443", false},
		{"xn--bcher-kva.example:443", true},
	} {
		err := ValidateTargets(ParseTargets(tc.in))
		if (err == nil) != tc.ok {
			t.Errorf("ValidateTargets(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("New accepted an empty target list")
	}
}

// The refusal names the target and the reason, because it reaches the
// operator as a startup error or a settings-page message.
func TestValidateTargetsSaysWhyAHostCannotBeLookedUp(t *testing.T) {
	err := ValidateTargets([]string{"canary-a:53", "exa..mple.com:443"})
	if err == nil || !strings.Contains(err.Error(), `"exa..mple.com:443"`) || !strings.Contains(err.Error(), "empty label") {
		t.Fatalf("err = %v, want the target and the empty label named", err)
	}
}

// A list saved before hosts were held to the resolver's rule is read back
// with the shape check alone, so an upgrade cannot stop the server starting:
// New and Configure take it, and still refuse what was never a target.
func TestValidateSavedTargetsChecksTheShapeOnly(t *testing.T) {
	legacy := []string{"exa..mple.com:443", "canary-a:53"}
	if err := ValidateSavedTargets(legacy); err != nil {
		t.Fatalf("ValidateSavedTargets(%q) = %v, want ok", legacy, err)
	}
	c, err := New(Options{Targets: legacy})
	if err != nil {
		t.Fatalf("New refused a saved list: %v", err)
	}
	if err := c.Configure(Settings{Enabled: true, Targets: []string{"999.1.1.1:53"}}); err != nil {
		t.Fatalf("Configure refused a saved list: %v", err)
	}
	for _, bad := range [][]string{nil, {"canary-a"}, {"canary-a:0"}, {"gate,way:53"}} {
		if err := ValidateSavedTargets(bad); err == nil {
			t.Errorf("ValidateSavedTargets(%q) accepted a list that is not host:port", bad)
		}
	}
}
