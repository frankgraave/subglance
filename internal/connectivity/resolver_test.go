package connectivity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeResolver answers lookups without touching the network: answers says
// how each name fares, and every lookup is counted.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string]error
	silent  bool
	lookups atomic.Int64
}

// noAnswer is what a lookup that got nothing back from the resolver returns.
var noAnswer = &netDNSError{timeout: true}

func (r *fakeResolver) set(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answers == nil {
		r.answers = map[string]error{}
	}
	r.answers[name] = err
}

func (r *fakeResolver) setSilent(silent bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.silent = silent
}

func (r *fakeResolver) lookup(_ context.Context, name string) error {
	r.lookups.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.silent {
		return noAnswer.err(name)
	}
	return r.answers[name]
}

const nameA, nameB = "ref-a.example", "ref-b.example"

func newResolverCanary(t *testing.T, fn *fakeNet, fr *fakeResolver, clk *fakeClock, onRestored func(from, to time.Time)) *Canary {
	t.Helper()
	c, err := New(Options{
		Targets:            []string{targetA, targetB},
		Names:              []string{nameA, nameB},
		Dial:               fn.dial,
		Lookup:             fr.lookup,
		Now:                clk.now,
		OnResolverRestored: onRestored,
	})
	if err != nil {
		t.Fatal(err)
	}
	prove(t, c, fn, clk)
	return c
}

func TestResolverDownOnlyWhenEveryNameGetsNoAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b error
		down bool
	}{
		{"both answer", nil, nil, false},
		{"one answers", noAnswer.err(nameA), nil, false},
		{"the other answers", nil, noAnswer.err(nameB), false},
		// A name that does not exist is the resolver answering: a
		// filtering resolver that blocks a reference name is working.
		{"both do not exist", notFound(nameA), notFound(nameB), false},
		{"one does not exist, one silent", notFound(nameA), noAnswer.err(nameB), false},
		{"neither answers", noAnswer.err(nameA), noAnswer.err(nameB), true},
		{"server failures", servfail(nameA), servfail(nameB), true},
		{"not a DNS error at all", errors.New("socket: too many open files"), noAnswer.err(nameB), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeResolver{}
			fr.set(nameA, tc.a)
			fr.set(nameB, tc.b)
			c := newResolverCanary(t, &fakeNet{}, fr, &fakeClock{t: time.Unix(1000, 0)}, nil)
			if got := c.ResolverDown(context.Background()); got != tc.down {
				t.Fatalf("ResolverDown = %v, want %v", got, tc.down)
			}
			if _, down := c.ResolverDownSince(); down != tc.down {
				t.Fatalf("ResolverDownSince reports down=%v, want %v", down, tc.down)
			}
		})
	}
}

// Twenty monitors whose lookups fail together cost one round of lookups,
// and the address round is not run for them: the two are separate questions.
func TestOneResolverRoundIsSharedWithinTheCacheWindow(t *testing.T) {
	fn := &fakeNet{}
	fr := &fakeResolver{}
	fr.setSilent(true)
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newResolverCanary(t, fn, fr, clk, nil)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !c.ResolverDown(context.Background()) {
				t.Error("reported the resolver answering with every name silent")
			}
		}()
	}
	wg.Wait()
	if got := fr.lookups.Load(); got != 2 {
		t.Fatalf("20 concurrent callers looked up %d names, want one round of 2", got)
	}
	if got := fn.dials.Load(); got != 0 {
		t.Fatalf("the resolver round dialled the targets %d times", got)
	}

	clk.advance(DefaultCacheFor - time.Second)
	c.ResolverDown(context.Background())
	if got := fr.lookups.Load(); got != 2 {
		t.Fatalf("a call inside the cache window looked up again: %d lookups", got)
	}
	clk.advance(time.Second)
	c.ResolverDown(context.Background())
	if got := fr.lookups.Load(); got != 4 {
		t.Fatalf("a call after the cache window did not start a new round: %d lookups", got)
	}
}

// The episode ends with one notice, bounded by the first silent round and
// the first answered one, and the sweep is what notices it.
func TestResolverRestoredIsReportedOncePerEpisode(t *testing.T) {
	fn := &fakeNet{}
	fn.set(targetA, true)
	fr := &fakeResolver{}
	fr.setSilent(true)
	clk := &fakeClock{t: time.Unix(1000, 0)}
	type episode struct{ from, to time.Time }
	var got []episode
	c := newResolverCanary(t, fn, fr, clk, func(from, to time.Time) { got = append(got, episode{from, to}) })
	ctx := context.Background()

	start := clk.now()
	if !c.ResolverDown(ctx) {
		t.Fatal("not down")
	}
	clk.advance(DefaultCacheFor)
	c.Sweep(ctx)
	if since, down := c.ResolverDownSince(); !down || !since.Equal(start) {
		t.Fatalf("a second silent round moved the episode: %v, %v", since, down)
	}
	// The host is online: the sweep re-asks the resolver, not the targets.
	if got := fn.dials.Load(); got != 0 {
		t.Fatalf("sweeping a resolver episode dialled the targets %d times", got)
	}

	fr.setSilent(false)
	clk.advance(DefaultCacheFor)
	back := clk.now()
	c.Sweep(ctx)
	if len(got) != 1 || !got[0].from.Equal(start) || !got[0].to.Equal(back) {
		t.Fatalf("restored callbacks = %+v, want one from %v to %v", got, start, back)
	}
	if _, down := c.ResolverDownSince(); down {
		t.Fatal("still down after an answered round")
	}
	before := fr.lookups.Load()
	clk.advance(DefaultCacheFor)
	c.Sweep(ctx)
	if fr.lookups.Load() != before || len(got) != 1 {
		t.Fatal("a sweep with the resolver answering looked something up or reported again")
	}
}

// Without names there is no resolver round: nothing is looked up, and a
// silent resolver is left to count against the monitor.
func TestNoNamesNeverLooksAnythingUp(t *testing.T) {
	fr := &fakeResolver{}
	fr.setSilent(true)
	c, err := New(Options{Targets: []string{targetA}, Dial: (&fakeNet{}).dial, Lookup: fr.lookup})
	if err != nil {
		t.Fatal(err)
	}
	if c.ResolverDown(context.Background()) {
		t.Fatal("a canary without names reported the resolver down")
	}
	if got := fr.lookups.Load(); got != 0 {
		t.Fatalf("a canary without names looked up %d names", got)
	}
}

// The switch covers the names too: off means no lookups at all.
func TestDisabledCanaryNeverLooksAnythingUp(t *testing.T) {
	fr := &fakeResolver{}
	fr.setSilent(true)
	c, err := New(Options{Targets: []string{targetA}, Names: []string{nameA}, Disabled: true,
		Dial: (&fakeNet{}).dial, Lookup: fr.lookup})
	if err != nil {
		t.Fatal(err)
	}
	if c.ResolverDown(context.Background()) {
		t.Fatal("a disabled canary reported the resolver down")
	}
	c.Sweep(context.Background())
	if got := fr.lookups.Load(); got != 0 {
		t.Fatalf("a disabled canary looked up %d names", got)
	}
}

// Turning the check off ends a resolver episode quietly, like an address one.
func TestConfigureForgetsTheResolverEpisode(t *testing.T) {
	fr := &fakeResolver{}
	fr.setSilent(true)
	restored := 0
	c := newResolverCanary(t, &fakeNet{}, fr, &fakeClock{t: time.Unix(1000, 0)}, func(time.Time, time.Time) { restored++ })
	if !c.ResolverDown(context.Background()) {
		t.Fatal("not down")
	}
	if err := c.Configure(Settings{Enabled: false, Targets: []string{targetA}}); err != nil {
		t.Fatal(err)
	}
	if _, down := c.ResolverDownSince(); down {
		t.Fatal("the resolver episode survived turning the check off")
	}
	if restored != 0 {
		t.Fatalf("turning the check off reported the resolver back %d times", restored)
	}
}

func TestValidateNames(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", true},
		{"one.one.one.one, dns.quad9.net", true},
		{"router.lan", true},
		{"example.org.", true},
		{"_dmarc.example.org", true},
		{"1.1.1.1", false},
		{"2001:db8::1", false},
		{"example.org:53", false},
		{"-bad.example", false},
		{"bad-.example", false},
		{"a..b", false},
		{"exämple.org", false},
		{"has space.example", false},
		{strings.Repeat("a", 64) + ".example", false},
		// Names the resolver refuses without asking. 999.1.1.1 fails as
		// "no such host", which reads as an answer, so the round could
		// never find the resolver down; an onion name fails as no answer,
		// so it would find it down every time.
		{"999.1.1.1", false},
		{"10.0.1", false},
		{"canary.onion", false},
		{"one.one.one.one, canary.onion", false},
	} {
		err := ValidateNames(ParseTargets(tc.in))
		if (err == nil) != tc.ok {
			t.Errorf("ValidateNames(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
	if _, err := New(Options{Targets: []string{targetA}, Names: []string{"1.1.1.1"}}); err == nil {
		t.Fatal("New accepted an address as a reference name")
	}
}
