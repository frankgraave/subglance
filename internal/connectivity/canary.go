// Package connectivity answers one question for the state engine: when a
// monitor is about to be declared down on a network error, can this host reach
// anything at all?
//
// # Why
//
// SubGlance usually runs on one VPS or one box in an office. When that host's
// own uplink, router or resolver fails, every check fails at once, every
// monitor meets its threshold, and twenty incidents are confirmed for an outage
// that belonged to none of them. Grouping turns the twenty alerts into one
// message, but the message is still false, and the uptime history of every
// monitor carries the dent afterwards.
//
// The canary tells the two cases apart by dialling a few reference addresses
// that belong to nobody being monitored. If every one of them fails too, the
// failure is the host's, not the monitor's.
//
// # The resolver round
//
// The addresses are dialled by address, so a broken local resolver cannot
// fail that round, and a working one is not needed for it to pass. That makes
// it blind to the other way a host loses the internet: the uplink is fine and
// the resolver is not (a Pi-hole restarting, an upstream that stopped
// answering). Then every monitor on a host name fails on its lookup while the
// address round passes.
//
// So a failure whose lookup got no answer gets a second round: a few
// reference names are looked up through this host's own resolver. If the
// address round passed and none of the names gets an answer either, the
// failure is the resolver's, not the monitor's. A name that does not exist is
// an answer; only silence, a refusal or a server failure counts against the
// resolver. Its episode is kept apart from the address round's, with its own
// start and its own notice when it ends, because the two say different things
// to whoever reads them.
//
// # When it runs
//
// Only when an incident is about to be confirmed on a network error. A host
// with nothing failing sends no traffic to the canary targets at all, which is
// the difference between a check an operator can accept and a process that
// phones out on a timer. One round is shared by every monitor that asks within
// CacheFor, so twenty monitors failing together cost one round, not twenty.
//
// While the host is offline, or its resolver is not answering, Run re-probes
// on a timer until a round passes. That is still traffic during a suspected
// outage only, and it is the only way to say when the outage ended: nothing
// else runs the canary once checks start passing again.
//
// # Which way it errs
//
// A canary that fails when the host is fine would hide a real outage, and a
// missed outage is worse than a false one. So the host is only called offline
// when every target fails, its resolver only when every name fails, and the
// caller only consults the canary for failures that a dead uplink or resolver
// could explain: DNS, connection and timeout. A status code or a missing
// keyword proves the target answered, and is never suppressed.
//
// # Changing it while it runs
//
// The targets and the on/off switch can be changed at run time (Configure),
// from the settings API. A change forgets what the previous targets said: a
// round against addresses that are no longer configured says nothing about
// the ones that are, and a round still in flight when the change lands is
// discarded rather than recorded. Discarding errs the safe way: a discarded
// round reports the host online, so the alert goes out.
package connectivity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/frankgraave/subglance/internal/checker"
)

// DefaultTargets are two independent public resolvers, dialled over TCP by
// address so that a broken local resolver cannot fail the canary by itself
// and a working one cannot be required for it to pass.
var DefaultTargets = []string{"1.1.1.1:53", "9.9.9.9:53"}

// DefaultNames are the reference names of the resolver round: two names run
// by independent operators, so that one of them being withdrawn or blocked
// cannot make a working resolver look broken.
var DefaultNames = []string{"one.one.one.one", "dns.quad9.net"}

const (
	// DefaultCacheFor is how long one round's answer is reused. Monitors on
	// a 60-second interval that fail together fail across a few seconds, and
	// this covers them with one round.
	DefaultCacheFor = 30 * time.Second

	// DefaultTimeout bounds one dial. A TCP handshake to a public anycast
	// resolver takes tens of milliseconds; three seconds is generous
	// without holding a check worker for long. It bounds one lookup of the
	// resolver round too.
	DefaultTimeout = 3 * time.Second

	// MaxTargets bounds the list. Every round dials all of them at once,
	// and the list can be set over the API; a handful of independent
	// addresses is all the check needs. It bounds the reference names too.
	MaxTargets = 16
)

// DialFunc opens a connection. A net.Dialer's DialContext satisfies it; tests
// substitute one that never touches the network.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// LookupFunc resolves a host name through this host's own resolver and
// reports what went wrong, if anything. Tests substitute one that never
// touches the network.
type LookupFunc func(ctx context.Context, name string) error

// Options configures New.
type Options struct {
	// Targets are host:port addresses dialled over TCP. At least one is
	// required.
	Targets []string

	// Names are the host names the resolver round looks up. Empty means
	// there is no resolver round: a lookup that got no answer counts
	// against its monitor unless the address round says the host is
	// offline.
	Names []string

	// Disabled builds a canary that never dials and never reports the host
	// offline until Configure turns it on. The zero value is on.
	Disabled bool

	// CacheFor, Timeout: zero means the defaults above.
	CacheFor time.Duration
	Timeout  time.Duration

	// OnRestored is called once per offline episode, when a round passes
	// after one failed. from is when the first failing round ran and to is
	// when the first passing one did. Optional.
	OnRestored func(from, to time.Time)

	// OnResolverRestored is the same for the resolver round: called once
	// per episode in which the resolver answered none of the names, when
	// it answers one again. Optional.
	OnResolverRestored func(from, to time.Time)

	// Dial, Lookup and Now are for tests. Zero means a plain net.Dialer,
	// the host's resolver and time.Now.
	Dial   DialFunc
	Lookup LookupFunc
	Now    func() time.Time
}

// Settings are the parts of a Canary that can change while it runs.
type Settings struct {
	// Enabled false means the canary never dials and never reports the
	// host offline, so every failure counts against its monitor.
	Enabled bool
	// Targets are the host:port addresses a round dials.
	Targets []string
}

// episode is what one kind of round last found: when it ran, whether
// everything it tried failed, and since when that has been so.
type episode struct {
	checkedAt time.Time
	down      bool
	since     time.Time
}

// Canary probes the configured targets and remembers the answer briefly.
// It is safe for concurrent use.
type Canary struct {
	cacheFor           time.Duration
	timeout            time.Duration
	onRestored         func(from, to time.Time)
	onResolverRestored func(from, to time.Time)
	dial               DialFunc
	lookup             LookupFunc
	now                func() time.Time
	// names is fixed for the canary's life: it is set by a flag or a
	// variable, never through the API.
	names []string

	// probeMu serialises rounds, so concurrent callers wait for the round
	// in flight and read its answer instead of starting their own.
	probeMu sync.Mutex

	mu      sync.Mutex
	targets []string
	enabled bool
	// generation changes on every Configure. A round records the one it
	// started under and is discarded if it no longer matches.
	generation uint64
	address    episode
	resolver   episode
}

// New builds a Canary. It returns an error for an empty target list, an
// address that is not host:port or a name that cannot be looked up, so a typo
// is a startup error rather than a canary that always fails and silences
// every outage.
func New(opts Options) (*Canary, error) {
	if err := ValidateSavedTargets(opts.Targets); err != nil {
		return nil, err
	}
	if err := ValidateNames(opts.Names); err != nil {
		return nil, err
	}
	c := &Canary{
		targets:            append([]string(nil), opts.Targets...),
		names:              append([]string(nil), opts.Names...),
		enabled:            !opts.Disabled,
		cacheFor:           opts.CacheFor,
		timeout:            opts.Timeout,
		onRestored:         opts.OnRestored,
		onResolverRestored: opts.OnResolverRestored,
		dial:               opts.Dial,
		lookup:             opts.Lookup,
		now:                opts.Now,
	}
	if c.cacheFor <= 0 {
		c.cacheFor = DefaultCacheFor
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.dial == nil {
		c.dial = (&net.Dialer{}).DialContext
	}
	if c.lookup == nil {
		c.lookup = systemLookup
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// systemLookup asks this host's resolver for a name's addresses. The name is
// made absolute first, so a search domain in resolv.conf cannot turn one
// lookup into several, or into a lookup of some other name.
func systemLookup(ctx context.Context, name string) error {
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	_, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
	return err
}

// ParseTargets splits a comma-separated list, dropping blanks. It splits the
// reference names too.
func ParseTargets(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ValidateTargets reports whether every target is a dialable host:port whose
// host the resolver would at least ask about. It judges a list being set, by
// a flag, a variable or the settings API.
//
// The host rule is checker.ResolvableHost, the one a monitor's target is held
// to. A name that breaks it (two dots in a row, a leading hyphen, 999.1.1.1)
// fails its dial before a packet is sent, every time. A canary whose every
// target fails like that calls the host offline in every round and so holds
// back every network-error incident: the one way this package must never err.
func ValidateTargets(targets []string) error {
	return validateTargets(targets, true)
}

// ValidateSavedTargets is ValidateTargets without the host rule. A list saved
// through the settings API before the rule existed is read back with it, so
// an upgrade cannot stop the server from starting; such a list still runs,
// and the startup log says which target cannot be looked up.
func ValidateSavedTargets(targets []string) error {
	return validateTargets(targets, false)
}

func validateTargets(targets []string, resolvable bool) error {
	if len(targets) == 0 {
		return errors.New("no connectivity targets are set")
	}
	if len(targets) > MaxTargets {
		return fmt.Errorf("%d connectivity targets are set; at most %d are allowed", len(targets), MaxTargets)
	}
	for _, t := range targets {
		host, port, err := net.SplitHostPort(t)
		if err != nil {
			return fmt.Errorf("connectivity target %q: want host:port", t)
		}
		if host == "" {
			return fmt.Errorf("connectivity target %q: the host is empty", t)
		}
		// A comma would split the target in two when the list is stored
		// and read back, and no host name or address contains one or a
		// space.
		if strings.ContainsFunc(host, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			return fmt.Errorf("connectivity target %q: the host contains a comma or a space", t)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("connectivity target %q: port must be a number from 1 to 65535", t)
		}
		if resolvable {
			if err := checker.ResolvableHost(host); err != nil {
				return fmt.Errorf("connectivity target %q: %w", t, err)
			}
		}
	}
	return nil
}

// ValidateNames reports whether every reference name is a host name the
// resolver can be asked for. An empty list is valid: it turns the resolver
// round off.
//
// An address is refused rather than accepted: looking one up never asks the
// resolver anything, so it would always answer and the round could never
// find the resolver down. A name the resolver refuses without asking is
// refused for the same reason, by checker.ResolvableHost: 999.1.1.1 fails as
// "no such host", which reads as an answer, so the round could never find the
// resolver down; x.onion fails as no answer at all, so the round would find
// it down in every round and hold back every DNS failure.
func ValidateNames(names []string) error {
	if len(names) > MaxTargets {
		return fmt.Errorf("%d connectivity names are set; at most %d are allowed", len(names), MaxTargets)
	}
	for _, n := range names {
		if _, err := netip.ParseAddr(n); err == nil {
			return fmt.Errorf("connectivity name %q is an address; the resolver check needs a host name to look up", n)
		}
		if err := checker.ResolvableHost(n); err != nil {
			return fmt.Errorf("connectivity name %q is not a host name the resolver can look up: %w", n, err)
		}
	}
	return nil
}

// Targets returns the configured addresses.
func (c *Canary) Targets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.targets...)
}

// Names returns the reference names of the resolver round.
func (c *Canary) Names() []string {
	return append([]string(nil), c.names...)
}

// Enabled reports whether the canary is switched on.
func (c *Canary) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// Settings returns what Configure last set, or what New was given.
func (c *Canary) Settings() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Settings{Enabled: c.enabled, Targets: append([]string(nil), c.targets...)}
}

// Configure replaces the targets and the switch. The targets are validated
// even when the canary is being turned off, so it can never be turned back on
// with a list it would refuse.
//
// Any change forgets the current answers, of both rounds, and ends an
// episode without the restored callback: nothing was reached, so there is
// nothing to report, and a round against the old targets must not stand for
// the new ones. A round in flight is discarded when it finishes. Configuring
// the same settings again changes nothing, so an unrelated save cannot cut an
// episode short.
func (c *Canary) Configure(s Settings) error {
	if err := ValidateSavedTargets(s.Targets); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.Enabled == c.enabled && slices.Equal(s.Targets, c.targets) {
		return nil
	}
	c.enabled = s.Enabled
	c.targets = append([]string(nil), s.Targets...)
	c.generation++
	c.address = episode{}
	c.resolver = episode{}
	return nil
}

// Offline reports whether every target failed in the most recent round,
// running a round first if the last one is older than CacheFor. A disabled
// canary answers false without dialling.
func (c *Canary) Offline(ctx context.Context) bool {
	return c.ask(ctx, &c.address, c.round)
}

// ResolverDown reports whether this host's resolver answered none of the
// reference names in the most recent resolver round, running one first if
// the last is older than CacheFor. A disabled canary, or one with no names,
// answers false without looking anything up.
//
// It says nothing about the uplink. A host with no connection at all fails
// this round too, so the caller asks Offline first and only asks this when
// the host is online: an unplugged cable is reported as that, not as a
// resolver fault.
func (c *Canary) ResolverDown(ctx context.Context) bool {
	if len(c.names) == 0 {
		return false
	}
	return c.ask(ctx, &c.resolver, c.resolverRound)
}

// ask answers from e when its last round is fresh, and runs one otherwise.
func (c *Canary) ask(ctx context.Context, e *episode, round func(context.Context) bool) bool {
	if !c.Enabled() {
		return false
	}
	if down, fresh := c.cached(e); fresh {
		return down
	}

	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	// A round may have finished while this caller waited for the lock.
	if down, fresh := c.cached(e); fresh {
		return down
	}
	return round(ctx)
}

// OfflineSince reports when the current offline episode began, or false when
// the host is not known to be offline.
func (c *Canary) OfflineSince() (time.Time, bool) {
	return c.downSince(&c.address)
}

// ResolverDownSince reports when the resolver stopped answering, or false
// when it is not known to have.
func (c *Canary) ResolverDownSince() (time.Time, bool) {
	return c.downSince(&c.resolver)
}

func (c *Canary) downSince(e *episode) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return e.since, e.down
}

// Run re-probes every CacheFor while the host is offline or its resolver is
// not answering, so the end of an episode is noticed and reported even when
// no check asks. It does nothing while both are fine. It returns when ctx is
// cancelled.
func (c *Canary) Run(ctx context.Context) {
	t := time.NewTicker(c.cacheFor)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sweep(ctx)
		}
	}
}

// Sweep runs one round of each kind that is currently believed down. It is
// what Run calls on each tick, exported so tests need not wait on a ticker.
func (c *Canary) Sweep(ctx context.Context) {
	_, offline := c.OfflineSince()
	_, resolverDown := c.ResolverDownSince()
	if !offline && !resolverDown {
		return
	}
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	if offline {
		c.round(ctx)
	}
	if resolverDown {
		c.resolverRound(ctx)
	}
}

func (c *Canary) cached(e *episode) (down, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.checkedAt.IsZero() || c.now().Sub(e.checkedAt) >= c.cacheFor {
		return false, false
	}
	return e.down, true
}

// round dials every target concurrently and records the answer. The caller
// holds probeMu.
func (c *Canary) round(ctx context.Context) bool {
	c.mu.Lock()
	targets, generation, enabled := c.targets, c.generation, c.enabled
	c.mu.Unlock()
	if !enabled {
		return false
	}
	reached := c.anyReachable(ctx, targets)
	return c.settle(ctx, generation, &c.address, reached, c.onRestored)
}

// resolverRound looks up every reference name concurrently and records the
// answer. The caller holds probeMu.
func (c *Canary) resolverRound(ctx context.Context) bool {
	c.mu.Lock()
	generation, enabled := c.generation, c.enabled
	c.mu.Unlock()
	if !enabled || len(c.names) == 0 {
		return false
	}
	answered := c.anyAnswered(ctx, c.names)
	return c.settle(ctx, generation, &c.resolver, answered, c.onResolverRestored)
}

// settle records what a round found in its episode, and reports whether
// everything it tried failed.
func (c *Canary) settle(ctx context.Context, generation uint64, e *episode, reached bool, onRestored func(from, to time.Time)) bool {
	// A round cut short by shutdown says nothing about the network, and
	// must not start or end an episode.
	if ctx.Err() != nil {
		return false
	}

	at := c.now()
	c.mu.Lock()
	if c.generation != generation {
		// Configure ran while this round dialled: its answer is about
		// settings that are no longer the ones configured.
		c.mu.Unlock()
		return false
	}
	wasDown, since := e.down, e.since
	e.checkedAt = at
	e.down = !reached
	switch {
	case !reached && !wasDown:
		e.since = at
	case reached:
		e.since = time.Time{}
	}
	c.mu.Unlock()

	if reached && wasDown && onRestored != nil {
		onRestored(since, at)
	}
	return !reached
}

func (c *Canary) anyReachable(ctx context.Context, targets []string) bool {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	results := make(chan bool, len(targets))
	for _, target := range targets {
		go func() {
			conn, err := c.dial(ctx, "tcp", target)
			if err == nil {
				_ = conn.Close()
			}
			results <- err == nil
		}()
	}
	reached := false
	for range targets {
		if <-results {
			reached = true
		}
	}
	return reached
}

func (c *Canary) anyAnswered(ctx context.Context, names []string) bool {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	results := make(chan bool, len(names))
	for _, name := range names {
		go func() {
			results <- Answered(c.lookup(ctx, name))
		}()
	}
	answered := false
	for range names {
		if <-results {
			answered = true
		}
	}
	return answered
}

// Answered reports whether a lookup got an answer from the resolver.
//
// A name that does not exist is an answer: the resolver is working. That
// matters twice. A filtering resolver that blocks a reference name must not
// make every DNS failure on this host look like the resolver's, and a
// monitor whose own domain has expired gets NXDOMAIN, which is a real outage
// that the resolver round must never be asked to explain.
func Answered(err error) bool {
	if err == nil {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}
