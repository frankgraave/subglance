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
// # Why there is no address guard here, but redirects are still refused
//
// The guard in package checker exists because monitor targets come from users
// of the web interface. This URL comes from whoever starts the process, and
// pointing it at a second instance on the same LAN is a reasonable thing to
// want. Someone who can set the command line can already reach the host
// network.
//
// Redirects are a different question. The configured host is trusted; the host
// it points at next is not, and ValidateURL never sees that second address. So
// the client refuses to follow redirects and a 3xx is recorded as a rejected
// ping.
//
// # What a ping discloses
//
// A ping carries the event name, the number of monitors scheduled and the
// number of checks completed. Monitor names, target URLs and check results
// never leave the instance. Those counts are still information about the
// install, and over http they travel in cleartext: on any network path that is
// not trusted end to end, use an https URL.
//
// # Why the log never shows the URL in full
//
// The URL is the switch's credential. Whoever knows a Healthchecks.io check
// URL, a Dead Man's Snitch URL or a push URL can reset that switch, and keep
// it quiet while this instance is down. Logs travel further than the
// process environment does, to aggregators and to whoever is helping read
// them, so every line and every error the watchdog writes shows the URL as
// its scheme and host only, the same shape a failed channel delivery gets:
// the host tells one receiver from another and diagnoses a refused
// connection; the path is what grants access.
package watchdog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
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
	mu        sync.RWMutex
	history   history
	createdAt time.Time
	url       string
	// shown is url as a log line or an error may print it; see checker.ShownURL.
	shown    string
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
	if client.CheckRedirect == nil {
		// ValidateURL only ever sees the configured URL. Following a
		// redirect would send this request to an address nobody
		// checked, which is a blind SSRF primitive from the SubGlance
		// host. Hand the 3xx back instead and let send reject it.
		client = cloneWithoutRedirects(client)
	}

	return &Watchdog{
		createdAt: time.Now(),
		url:       opts.URL,
		shown:     checker.ShownURL(opts.URL),
		interval:  interval,
		liveness:  opts.Liveness,
		log:       log,
		client:    client,
	}, nil
}

// cloneWithoutRedirects copies a client and makes it stop at the first
// response. The copy keeps the caller's client untouched: a client passed in
// through Options may well be shared with something that does want redirects.
func cloneWithoutRedirects(c *http.Client) *http.Client {
	clone := *c
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

// ValidateURL reports whether a string is usable as a ping target.
//
// The check is deliberately shallow — absolute, http or https, with a host —
// because anything stricter would start rejecting the private addresses this
// feature is often pointed at on purpose.
func ValidateURL(raw string) error {
	shown := checker.ShownURL(raw)
	quoted := strconv.Quote(shown)
	u, err := url.Parse(raw)
	if err != nil {
		// Go's own message quotes the whole input back, path and all,
		// and its reason can quote the authority: in
		// https://user:pass/x@host, or https://user:pass/x with the "@"
		// left out, the parser reads pass as a port and its reason
		// prints it. The reason is given only where checker.ShownURL kept the
		// authority whole.
		if strings.Contains(shown, "***") {
			// Go's reason without the text it quotes.
			if strings.HasPrefix(errors.Unwrap(err).Error(), "invalid port ") {
				return fmt.Errorf("watchdog-url %s is not a URL: invalid port after host", quoted)
			}
			return fmt.Errorf("watchdog-url %s is not a URL", quoted)
		}
		return fmt.Errorf("watchdog-url %s is not a URL: %w", quoted, errors.Unwrap(err))
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("watchdog-url %s: want an http or https URL", quoted)
	}
	if u.Host == "" {
		return fmt.Errorf("watchdog-url %s: missing host", quoted)
	}
	// Not a stricter reading of the address, which stays any host and any
	// network: a port out of range or a hostname the resolver refuses
	// unasked fails every ping before it leaves the process, and the switch
	// at the far end would raise the alarm for an instance that is fine.
	if err := checker.ResolvableURL(u); err != nil {
		return fmt.Errorf("watchdog-url %s: %w", quoted, err)
	}
	return nil
}

// loggable is err with every URL in it shown as checker.ShownURL shows it.
//
// The HTTP client wraps every transport failure in a *url.Error that
// quotes the request URL: Go masks a password there, but not the path or
// the query, which is where every common switch keeps its token. A
// transport passed in through Options can return a *url.Error of its own,
// which Do then wraps in another, so every *url.Error in the chain is
// copied with its URL shown, and an error that wraps one is given as its
// text with that URL cut. The copies keep Op and the cause below the last
// *url.Error, so "dial tcp …: connection refused" and a timeout read
// exactly as before, and errors.Is still finds the cause.
func loggable(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	clean := *ue
	clean.URL = checker.ShownURL(ue.URL)
	clean.Err = loggable(ue.Err)
	msg := strings.ReplaceAll(err.Error(), ue.Error(), clean.Error())
	if msg == clean.Error() {
		return &clean
	}
	// A wrapper above the *url.Error: its message is given with the
	// chain's text cleaned, around the cleaned chain. A wrapper that
	// printed the URL itself has that cut too.
	return &redacted{msg: strings.ReplaceAll(msg, ue.URL, clean.URL), err: &clean}
}

// redacted is an error whose message has had a URL cut out of it, and
// which unwraps to the cleaned *url.Error below it.
type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }

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
	w.log.Info("watchdog enabled", "url", w.shown, "interval", w.interval)

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
		w.mu.Lock()
		w.history.decision = time.Now().UTC()
		w.history.suppressed = true
		w.mu.Unlock()
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
//
// The X-SubGlance-Event header is the only thing that marks this ping as
// different, and a generic provider ignores it: Healthchecks.io and Dead Man's
// Snitch both read it as an ordinary check-in. That is the intended outcome —
// the switch is reset, so nobody is paged for a planned restart — but only a
// receiver that reads the header can tell a clean stop from a normal ping.
func (w *Watchdog) pingStopped() {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	w.send(ctx, "stopped", "stopped; shutting down cleanly")
}

// send performs one ping. Failures are logged, never returned: a watchdog that
// takes the process down with it would be worse than no watchdog.
func (w *Watchdog) send(ctx context.Context, event, body string) {
	w.beginAttempt(event)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewBufferString(body))
	if err != nil {
		w.log.Error("watchdog ping could not be built", "error", loggable(err))
		w.finishAttempt("request_error", 0)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "SubGlance-Watchdog/1")
	// The event is a header as well as a body line so a receiver that only
	// logs bodies and one that only inspects headers both see it.
	req.Header.Set("X-SubGlance-Event", event)

	resp, err := w.client.Do(req)
	if err != nil {
		w.log.Error("watchdog ping failed", "event", event, "error", loggable(err))
		result := "transport_error"
		var networkError interface{ Timeout() bool }
		if errors.Is(err, context.Canceled) {
			result = "canceled"
		} else if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			result = "timeout"
		}
		w.finishAttempt(result, 0)
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
		// A 3xx lands here rather than being followed; see the client
		// setup in New.
		w.log.Error("watchdog ping rejected", "event", event, "status", resp.StatusCode)
		w.finishAttempt("rejected", resp.StatusCode)
		return
	}
	w.log.Debug("watchdog ping sent", "event", event)
	w.finishAttempt("succeeded", resp.StatusCode)
}
