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
// # When it runs
//
// Only when an incident is about to be confirmed on a network error. A host
// with nothing failing sends no traffic to the canary targets at all, which is
// the difference between a check an operator can accept and a process that
// phones out on a timer. One round is shared by every monitor that asks within
// CacheFor, so twenty monitors failing together cost one round, not twenty.
//
// While the host is offline, Run re-probes on a timer until a round passes.
// That is still traffic during a suspected outage only, and it is the only way
// to say when the outage ended: nothing else runs the canary once checks start
// passing again.
//
// # Which way it errs
//
// A canary that fails when the host is fine would hide a real outage, and a
// missed outage is worse than a false one. So the host is only called offline
// when every target fails, and the caller only consults the canary for
// failures that a dead uplink could explain: DNS, connection and timeout.
// A status code or a missing keyword proves the target answered, and is never
// suppressed.
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DefaultTargets are two independent public resolvers, dialled over TCP by
// address so that a broken local resolver cannot fail the canary by itself
// and a working one cannot be required for it to pass.
var DefaultTargets = []string{"1.1.1.1:53", "9.9.9.9:53"}

const (
	// DefaultCacheFor is how long one round's answer is reused. Monitors on
	// a 60-second interval that fail together fail across a few seconds, and
	// this covers them with one round.
	DefaultCacheFor = 30 * time.Second

	// DefaultTimeout bounds one dial. A TCP handshake to a public anycast
	// resolver takes tens of milliseconds; three seconds is generous
	// without holding a check worker for long.
	DefaultTimeout = 3 * time.Second

	// MaxTargets bounds the list. Every round dials all of them at once,
	// and the list can be set over the API; a handful of independent
	// addresses is all the check needs.
	MaxTargets = 16
)

// DialFunc opens a connection. A net.Dialer's DialContext satisfies it; tests
// substitute one that never touches the network.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Options configures New.
type Options struct {
	// Targets are host:port addresses dialled over TCP. At least one is
	// required.
	Targets []string

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

	// Dial and Now are for tests. Zero means a plain net.Dialer and
	// time.Now.
	Dial DialFunc
	Now  func() time.Time
}

// Settings are the parts of a Canary that can change while it runs.
type Settings struct {
	// Enabled false means the canary never dials and never reports the
	// host offline, so every failure counts against its monitor.
	Enabled bool
	// Targets are the host:port addresses a round dials.
	Targets []string
}

// Canary probes the configured targets and remembers the answer briefly.
// It is safe for concurrent use.
type Canary struct {
	cacheFor   time.Duration
	timeout    time.Duration
	onRestored func(from, to time.Time)
	dial       DialFunc
	now        func() time.Time

	// probeMu serialises rounds, so concurrent callers wait for the round
	// in flight and read its answer instead of starting their own.
	probeMu sync.Mutex

	mu      sync.Mutex
	targets []string
	enabled bool
	// generation changes on every Configure. A round records the one it
	// started under and is discarded if it no longer matches.
	generation   uint64
	checkedAt    time.Time
	offline      bool
	offlineSince time.Time
}

// New builds a Canary. It returns an error for an empty target list or an
// address that is not host:port, so a typo is a startup error rather than a
// canary that always fails and silences every outage.
func New(opts Options) (*Canary, error) {
	if err := ValidateTargets(opts.Targets); err != nil {
		return nil, err
	}
	c := &Canary{
		targets:    append([]string(nil), opts.Targets...),
		enabled:    !opts.Disabled,
		cacheFor:   opts.CacheFor,
		timeout:    opts.Timeout,
		onRestored: opts.OnRestored,
		dial:       opts.Dial,
		now:        opts.Now,
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
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// ParseTargets splits a comma-separated list, dropping blanks.
func ParseTargets(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ValidateTargets reports whether every target is a dialable host:port.
func ValidateTargets(targets []string) error {
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
	}
	return nil
}

// Targets returns the configured addresses.
func (c *Canary) Targets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.targets...)
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
// Any change forgets the current answer and ends an offline episode without
// the restored callback: nothing was reached, so there is nothing to report,
// and a round against the old targets must not stand for the new ones. A
// round in flight is discarded when it finishes. Configuring the same
// settings again changes nothing, so an unrelated save cannot cut an episode
// short.
func (c *Canary) Configure(s Settings) error {
	if err := ValidateTargets(s.Targets); err != nil {
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
	c.checkedAt = time.Time{}
	c.offline = false
	c.offlineSince = time.Time{}
	return nil
}

// Offline reports whether every target failed in the most recent round,
// running a round first if the last one is older than CacheFor. A disabled
// canary answers false without dialling.
func (c *Canary) Offline(ctx context.Context) bool {
	if !c.Enabled() {
		return false
	}
	if offline, fresh := c.cached(); fresh {
		return offline
	}

	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	// A round may have finished while this caller waited for the lock.
	if offline, fresh := c.cached(); fresh {
		return offline
	}
	return c.round(ctx)
}

// OfflineSince reports when the current offline episode began, or false when
// the host is not known to be offline.
func (c *Canary) OfflineSince() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offlineSince, c.offline
}

// Run re-probes every CacheFor while the host is offline, so the end of an
// episode is noticed and reported even when no check asks. It does nothing
// while the host is online. It returns when ctx is cancelled.
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

// Sweep runs one round if the host is currently believed offline. It is what
// Run calls on each tick, exported so tests need not wait on a ticker.
func (c *Canary) Sweep(ctx context.Context) {
	if _, offline := c.OfflineSince(); !offline {
		return
	}
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	c.round(ctx)
}

func (c *Canary) cached() (offline, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkedAt.IsZero() || c.now().Sub(c.checkedAt) >= c.cacheFor {
		return false, false
	}
	return c.offline, true
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
	// A round cut short by shutdown says nothing about the network, and
	// must not start or end an episode.
	if ctx.Err() != nil {
		return false
	}

	at := c.now()
	c.mu.Lock()
	if c.generation != generation {
		// Configure ran while this round dialled: its answer is about
		// targets that are no longer the ones configured.
		c.mu.Unlock()
		return false
	}
	wasOffline, since := c.offline, c.offlineSince
	c.checkedAt = at
	c.offline = !reached
	switch {
	case !reached && !wasOffline:
		c.offlineSince = at
	case reached:
		c.offlineSince = time.Time{}
	}
	c.mu.Unlock()

	if reached && wasOffline && c.onRestored != nil {
		c.onRestored(since, at)
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
