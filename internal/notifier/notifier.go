package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// maxAttempts is how often one delivery is tried before it is dead-lettered.
//
// Six attempts with the schedule below spans roughly half an hour. That covers
// the ordinary case — a webhook receiver restarting, a brief network fault —
// without holding an alert so long that it arrives after the outage it
// describes is over. Past that the honest thing is to record the failure where
// the operator can see it, rather than retrying into the void.
const maxAttempts = 6

// backoff returns the delay before attempt n (1-based).
//
// Exponential from 10 seconds with a cap, plus jitter. The jitter matters more
// than it looks: an outage produces alerts for every affected monitor at the
// same instant, so a fixed schedule would have them all retry in lockstep and
// hammer a recovering endpoint in waves.
func backoff(attempt int, rnd *rand.Rand) time.Duration {
	const (
		base = 10 * time.Second
		max  = 10 * time.Minute
	)

	d := time.Duration(float64(base) * math.Pow(3, float64(attempt-1)))
	if d > max || d <= 0 {
		d = max
	}

	jitter := time.Duration(rnd.Int63n(int64(d / 4)))
	return d + jitter
}

// Notifier drains the outbox.
type Notifier struct {
	db  *store.DB
	log *slog.Logger

	senders map[string]Sender

	// interval is how often the queue is swept when it was empty. A due
	// row found on a sweep makes the next one immediate, so a quiet system
	// polls slowly and a busy one does not wait.
	interval time.Duration

	// batch caps how many deliveries are loaded per sweep.
	batch int

	now func() time.Time

	mu  sync.Mutex
	rnd *rand.Rand
}

// Options configures New.
type Options struct {
	DB  *store.DB
	Log *slog.Logger

	// Interval is the idle sweep interval. Zero means five seconds.
	Interval time.Duration

	// Batch caps deliveries per sweep. Zero means 50.
	Batch int

	// Senders overrides the channel implementations. Nil means the real
	// ones; tests pass fakes.
	Senders map[string]Sender

	// Now is the clock, swappable in tests.
	Now func() time.Time
}

// New builds a Notifier with the standard set of channels.
func New(opts Options) *Notifier {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	senders := opts.Senders
	if senders == nil {
		senders = map[string]Sender{
			store.ChannelWebhook:  NewWebhookSender(),
			store.ChannelDiscord:  NewDiscordSender(),
			store.ChannelSlack:    NewSlackSender(),
			store.ChannelTelegram: NewTelegramSender(),
			store.ChannelEmail:    NewEmailSender(),
		}
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	batch := opts.Batch
	if batch <= 0 {
		batch = 50
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Notifier{
		db:       opts.DB,
		log:      log,
		senders:  senders,
		interval: interval,
		batch:    batch,
		now:      now,
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Enqueue turns one alert into one outbox row per assigned channel.
//
// This is what the runner calls, and it is deliberately the only part of
// notification that happens on the check path. It writes rows and returns; no
// network call happens here, so a broken channel cannot slow a check down.
func (n *Notifier) Enqueue(ctx context.Context, m store.Monitor, inc store.Incident, event state.Event, at time.Time) error {
	channels, err := n.db.ListMonitorChannels(ctx, m.ID)
	if err != nil {
		return fmt.Errorf("list channels for monitor %d: %w", m.ID, err)
	}
	if len(channels) == 0 {
		// Not an error. Plenty of monitors are watched on the dashboard
		// and nowhere else, and saying so at every transition would fill
		// the log with a non-event.
		return nil
	}

	alert := AlertFromStore(m, inc, event, at)
	payload, err := alert.Encode()
	if err != nil {
		return err
	}

	var firstErr error
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		if _, err := n.db.EnqueueDelivery(ctx, store.Delivery{
			ChannelID:  ch.ID,
			MonitorID:  m.ID,
			IncidentID: inc.ID,
			Event:      string(event),
			Payload:    payload,
			// Due now, by this notifier's clock rather than the
			// store's. The worker and the queue have to agree on
			// what "now" means, or a delivery is written into a
			// future the sweeper never reaches.
			NextAttemptAt: n.now(),
		}); err != nil {
			// Keep going: one channel failing to enqueue must not
			// stop the others from being told.
			n.log.Error("could not queue notification",
				"monitor", m.Name, "channel", ch.Name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Run drains the outbox until the context is cancelled.
func (n *Notifier) Run(ctx context.Context) {
	n.log.Info("notifier started", "interval", n.interval)

	for {
		sent, err := n.sweep(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			n.log.Error("notification sweep failed", "error", err)
		}

		// A sweep that did work looks again straight away: a backlog
		// should drain at the speed of the endpoints, not the speed of
		// the poll interval.
		delay := n.interval
		if sent > 0 {
			delay = 0
		}

		select {
		case <-ctx.Done():
			n.log.Info("notifier stopped")
			return
		case <-time.After(delay):
		}
	}
}

// sweep processes one batch of due deliveries and returns how many it handled.
func (n *Notifier) sweep(ctx context.Context) (int, error) {
	due, err := n.db.DueDeliveries(ctx, n.now(), n.batch)
	if err != nil {
		return 0, err
	}

	for _, d := range due {
		if ctx.Err() != nil {
			return len(due), ctx.Err()
		}
		n.attempt(ctx, d)
	}
	return len(due), nil
}

// attempt performs one delivery and records the outcome.
func (n *Notifier) attempt(ctx context.Context, d store.Delivery) {
	ch, err := n.db.GetChannel(ctx, d.ChannelID)
	if err != nil {
		// The channel was deleted while this was queued. There is
		// nowhere to send it and never will be.
		n.fail(ctx, d, fmt.Sprintf("channel %d no longer exists", d.ChannelID))
		return
	}

	if !ch.Enabled {
		n.fail(ctx, d, "channel is disabled")
		return
	}

	sender, ok := n.senders[ch.Type]
	if !ok {
		n.fail(ctx, d, fmt.Sprintf("no implementation for channel type %q", ch.Type))
		return
	}

	alert, err := DecodeAlert(d.Payload)
	if err != nil {
		n.fail(ctx, d, "stored alert could not be read: "+err.Error())
		return
	}

	sendCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	err = sender.Send(sendCtx, ch.Config, alert)
	if err == nil {
		if err := n.db.MarkDelivered(ctx, d.ID); err != nil {
			n.log.Error("delivery sent but not recorded", "delivery", d.ID, "error", err)
		}
		n.log.Info("alert delivered",
			"monitor", alert.MonitorName, "channel", ch.Name, "event", alert.Event)
		return
	}

	var r *Retryable
	if !errors.As(err, &r) {
		// Permanent: a bad URL, a revoked token, a rejected body.
		n.log.Error("alert rejected",
			"monitor", alert.MonitorName, "channel", ch.Name, "error", err)
		n.fail(ctx, d, err.Error())
		return
	}

	next := d.Attempts + 1
	if next >= maxAttempts {
		n.log.Error("alert gave up after retries",
			"monitor", alert.MonitorName, "channel", ch.Name,
			"attempts", next, "error", err)
		n.fail(ctx, d, fmt.Sprintf("gave up after %d attempts: %s", next, err))
		return
	}

	n.mu.Lock()
	delay := backoff(next, n.rnd)
	n.mu.Unlock()

	n.log.Warn("alert delivery failed, will retry",
		"monitor", alert.MonitorName, "channel", ch.Name,
		"attempt", next, "retry_in", delay, "error", err)

	if err := n.db.MarkRetry(ctx, d.ID, err.Error(), n.now().Add(delay)); err != nil {
		n.log.Error("could not schedule retry", "delivery", d.ID, "error", err)
	}
}

// fail dead-letters a delivery.
func (n *Notifier) fail(ctx context.Context, d store.Delivery, cause string) {
	if err := n.db.MarkFailed(ctx, d.ID, cause); err != nil {
		n.log.Error("could not record delivery failure", "delivery", d.ID, "error", err)
	}
}

// Validate checks a channel config without sending anything.
//
// Exposed for the API so a channel is checked when it is saved, while the
// person who typed it is still looking at the form.
func (n *Notifier) Validate(channelType string, cfg map[string]string) error {
	sender, ok := n.senders[channelType]
	if !ok {
		return fmt.Errorf("unknown channel type %q", channelType)
	}
	return sender.Validate(cfg)
}

// Test sends a real message through a channel, so an operator can prove the
// configuration works before an outage does it for them.
func (n *Notifier) Test(ctx context.Context, ch store.Channel) error {
	sender, ok := n.senders[ch.Type]
	if !ok {
		return fmt.Errorf("unknown channel type %q", ch.Type)
	}
	if err := sender.Validate(ch.Config); err != nil {
		return err
	}

	alert := Alert{
		MonitorName: "SubGlance test",
		MonitorType: "http",
		Target:      "This is a test alert. No monitor is down.",
		Event:       string(state.EventIncidentResolved),
		At:          n.now(),
	}

	sendCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	return sender.Send(sendCtx, ch.Config, alert)
}
