// Package watchdog pings an external dead man's switch while SubGlance is
// demonstrably still checking things.
//
// # Why an outbound ping at all
//
// SubGlance is one binary on one host. If that process is killed, runs out of
// memory, fills its disk or wedges, the dashboard does not turn red — it stops
// existing. A monitor that reports nothing and a monitor that is dead look
// identical from the outside, and the second one is the worse failure: it is
// silence that feels like good news.
//
// Nothing inside the process can report its own death. So the only honest
// answer is to hand the judgement to something else: SubGlance pings a URL on
// a schedule, and whoever owns that URL raises the alarm when the pings stop.
// Healthchecks.io, Dead Man's Snitch and a second SubGlance's push endpoint
// all work that way.
//
// # Why the ping is tied to evidence, not to a timer
//
// A bare ticker would keep pinging from a process whose scheduler has
// deadlocked, whose database is unwritable, or whose worker pool is wedged —
// reporting health from a corpse, which is precisely the failure this is meant
// to catch. So a ping is only sent when the check pipeline has completed at
// least one check since the previous ping.
//
// The one exception is an instance with no monitors scheduled. There, zero
// completed checks is the correct outcome rather than a symptom, and staying
// silent would alarm someone about a perfectly healthy install.
//
// # Why there is no SSRF guard here
//
// The guard in package checker exists because monitor targets come from users
// of the web interface. This URL comes from whoever starts the process, and
// pointing it at a second instance on the same LAN is a reasonable thing to
// want. Someone who can set the command line can already reach the host
// network.
package watchdog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// DefaultInterval is how often a ping is sent when none is configured.
//
// Five minutes is a compromise: frequent enough that a dead instance is
// noticed within a coffee break, rare enough that a free tier at the receiving
// end is not a problem.
const DefaultInterval = 5 * time.Minute

// pingTimeout bounds one outbound request. The watchdog must never be the
// reason shutdown hangs, and a ping that takes longer than this is one the
// next tick can retry anyway.
const pingTimeout = 10 * time.Second

// Liveness is the evidence the watchdog demands before it speaks.
type Liveness struct {
	// ChecksCompleted counts checks recorded since process start. Only its
	// movement matters, never its absolute value.
	ChecksCompleted uint64

	// Scheduled is how many monitors are currently in the schedule.
	Scheduled int
}

// Options configures New.
type Options struct {
	// URL is the dead man's switch to ping. Empty disables the watchdog.
	URL string

	// Interval is the gap between pings. Zero means DefaultInterval.
	Interval time.Duration

	// Liveness reports progress of the check pipeline. Required.
	Liveness func() Liveness

	// Log receives one line per failed ping. Optional.
	Log *slog.Logger

	// Client sends the pings. Optional; a bounded default is used.
	Client *http.Client
}

// Watchdog pings an external URL on a schedule.
type Watchdog struct {
	url      string
	interval time.Duration
	liveness func() Liveness
	log      *slog.Logger
	client   *http.Client

	// last is the liveness reading at the previous ping decision.
	last Liveness
}

// New validates the options and builds a Watchdog.
//
// It returns a nil Watchdog and no error when no URL is configured: the
// feature is off by default, and the caller should not have to distinguish
// "disabled" from "broken".
func New(opts Options) (*Watchdog, error) {
	if opts.URL == "" {
		return nil, nil
	}
	if err := ValidateURL(opts.URL); err != nil {
		return nil, err
	}
	if opts.Liveness == nil {
		return nil, fmt.Errorf("watchdog: liveness function is required")
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: pingTimeout}
	}

	return &Watchdog{
		url:      opts.URL,
		interval: interval,
		liveness: opts.Liveness,
		log:      log,
		client:   client,
	}, nil
}

// ValidateURL reports whether a string is usable as a ping target.
//
// The check is deliberately shallow — absolute, http or https, with a host —
// because anything stricter would start rejecting the private addresses this
// feature is often pointed at on purpose.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("watchdog-url %q: %w", raw, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("watchdog-url %q: want an http or https URL", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("watchdog-url %q: missing host", raw)
	}
	return nil
}

// Run pings until ctx is cancelled, then sends one final "stopped" ping.
//
// The first ping waits out a full interval rather than firing at startup. A
// process that crashes seconds after boot would otherwise report itself
// healthy on the way down, and a crash loop would keep the switch happy
// forever.
func (w *Watchdog) Run(ctx context.Context) {
	if w == nil {
		return
	}
	w.last = w.liveness()
	w.log.Info("watchdog enabled", "url", w.url, "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.pingStopped()
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick sends one ping if the pipeline has moved since the last one.
func (w *Watchdog) tick(ctx context.Context) {
	now := w.liveness()
	progressed := now.ChecksCompleted > w.last.ChecksCompleted
	idle := now.Scheduled == 0
	w.last = now

	if !progressed && !idle {
		// Staying silent here is the whole point of the feature: the
		// process is running but the check pipeline is not, and the
		// receiving end should treat that exactly like a dead host.
		w.log.Warn("skipping watchdog ping: no checks completed since the last one",
			"scheduled", now.Scheduled)
		return
	}

	body := fmt.Sprintf("alive; %d monitors scheduled, %d checks completed",
		now.Scheduled, now.ChecksCompleted)
	w.send(ctx, "alive", body)
}

// pingStopped announces a deliberate shutdown.
//
// Without it, every planned restart looks the same as a crash to whatever is
// watching, and an operator who gets paged by their own deploy learns to
// ignore the pager. It uses a fresh context because the one that triggered
// shutdown is already cancelled.
func (w *Watchdog) pingStopped() {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	w.send(ctx, "stopped", "stopped; shutting down cleanly")
}

// send performs one ping. Failures are logged, never returned: a watchdog that
// takes the process down with it would be worse than no watchdog.
func (w *Watchdog) send(ctx context.Context, event, body string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewBufferString(body))
	if err != nil {
		w.log.Error("watchdog ping could not be built", "error", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "SubGlance-Watchdog/1")
	// The event is a header as well as a body line so a receiver that only
	// logs bodies and one that only inspects headers both see it.
	req.Header.Set("X-SubGlance-Event", event)

	resp, err := w.client.Do(req)
	if err != nil {
		w.log.Error("watchdog ping failed", "event", event, "error", err)
		return
	}
	defer func() {
		// Drain before closing so the connection can be reused; the
		// limit keeps a hostile or broken endpoint from feeding us a
		// stream we would dutifully read forever.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.log.Error("watchdog ping rejected", "event", event, "status", resp.StatusCode)
		return
	}
	w.log.Debug("watchdog ping sent", "event", event)
}
