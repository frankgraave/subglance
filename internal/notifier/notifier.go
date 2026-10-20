package notifier

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/mutelink"
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
//
// The randomness comes from crypto/rand. Nothing here is a secret, but a
// retry schedule is not worth a lint exception, and a failure to read random
// bytes falls back to the undithered delay rather than to a predictable
// sequence.
func backoff(attempt int) time.Duration {
	const (
		base    = 10 * time.Second
		ceiling = 10 * time.Minute
	)

	d := time.Duration(float64(base) * math.Pow(3, float64(attempt-1)))
	if d > ceiling || d <= 0 {
		d = ceiling
	}

	return d + jitter(d/4)
}

// jitter returns a random duration in [0, span).
func jitter(span time.Duration) time.Duration {
	if span <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		// Out of entropy is not a reason to stop retrying; it only
		// means this delay is undithered.
		return 0
	}
	return time.Duration(n.Int64())
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

	// grouper decides which alerts belong together, and batches holds the
	// ones still inside their window.
	//
	// Guarded by mu: Enqueue runs on the check path, once per monitor, and
	// the sweep loop flushes on its own goroutine. Those genuinely race —
	// a shared outage is many monitors calling Enqueue at once, which is
	// exactly the case grouping exists for.
	mu      sync.Mutex
	grouper *Grouper
	batches map[string]*pending

	// stopped is set by the final flush, under mu, in the same critical
	// section that empties batches. From then on Enqueue writes straight to
	// the outbox. Nothing is left to close a window once Run has returned,
	// and the callers outlive it: the scheduler finishes its in-flight
	// checks, the HTTP server drains push reports and a reminder sweep
	// completes after the signal. An alert they raise is a real event whose
	// incident is already marked confirmed, reminded or resolved, so the
	// next start does not raise it again; held in memory it would be lost.
	stopped bool

	// counts holds the delivery counters /metrics serves, one set per
	// channel type. Built once in New and never written after, so reading
	// the map needs no lock; the counters themselves are atomic.
	counts map[string]*deliveryCounts

	// nextFailureCheck is when reportFailures next reads the failing
	// channels, in Unix nanoseconds. A delivery that gives up sets it to
	// zero, so the notice about it goes out on the next pass of the loop.
	nextFailureCheck atomic.Int64

	// unreported remembers, per channel, the spell the log has already
	// said there is no other channel for. Touched only by reportFailures,
	// on the loop's own goroutine.
	unreported map[int64]time.Time

	// rewritten holds the rows a recovery merged into or closed during the
	// current sweep. The sweep loaded them before that write, so it skips
	// them; the next sweep reads them as they are now. Touched only on the
	// loop's own goroutine, and emptied at the start of every sweep.
	rewritten map[int64]bool

	// baseURL is the address the instance is reached at, from ParseBaseURL,
	// or "" when alerts carry no links.
	baseURL string

	// muteLinks signs the link that mutes an incident from its alert, or
	// is nil when alerts carry none.
	muteLinks *mutelink.Signer
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

	// Guard restricts which addresses a delivery may connect to. It is
	// optional: nil means no restriction, which is what tests and a
	// deployment without the checker pipeline need. In the server it is
	// always set, from the same --allow-private-targets setting the
	// monitor checkers use.
	Guard *checker.Guard

	// Now is the clock, swappable in tests.
	Now func() time.Time

	// GroupWindow is how long an alert waits for others before it is sent.
	// Zero means DefaultGroupWindow.
	//
	// Negative (see GroupingDisabled) disables grouping: Enqueue writes to
	// the outbox straight away, as it did before grouping existed. That is
	// a real preference — someone with three monitors has nothing to group
	// and may want the alert the instant it happens — and it is also what
	// the delivery tests use, since they are about retries and failures
	// rather than about batching.
	GroupWindow time.Duration

	// BaseURL is the address SubGlance is reached at, as ParseBaseURL
	// returns it. When set, every alert about a monitor links to its
	// incident and to the monitor; empty sends every message without links,
	// exactly as before links existed.
	BaseURL string

	// MuteLinks signs the link in an alert that mutes its incident's
	// repeat alerts without signing in. Used only with BaseURL; nil sends
	// no such link.
	MuteLinks *mutelink.Signer
}

// New builds a Notifier with the standard set of channels.
func New(opts Options) *Notifier {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	senders := opts.Senders
	if senders == nil {
		senders = defaultSenders(opts.Guard)
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

	counts := make(map[string]*deliveryCounts, len(senders))
	for typ := range senders {
		counts[typ] = &deliveryCounts{}
	}

	return &Notifier{
		db:       opts.DB,
		log:      log,
		senders:  senders,
		interval: interval,
		batch:    batch,
		now:      now,
		grouper:  newGrouper(opts.GroupWindow, now),
		batches:  make(map[string]*pending),
		counts:   counts,

		unreported: make(map[int64]time.Time),
		rewritten:  make(map[int64]bool),
		baseURL:    opts.BaseURL,
		muteLinks:  opts.MuteLinks,
	}
}

// Enqueue collects one alert for delivery to every assigned channel.
//
// Ordinary alerts go into a batch keyed on
// channel and direction, and a batch is flushed once its window closes — so a
// shared outage becomes one message per channel rather than one per monitor.
// The window is short (see DefaultGroupWindow) and it never delays a batch that
// has already been flushed, so a lone failure still arrives promptly.
//
// Deferred maintenance initials go directly to the durable outbox in the same
// transaction that consumes their intent. No network call happens here. A
// broken channel still cannot slow a check down.
//
// An outage confirmed with its alert owed (store.Incident.AlertOwed) has the
// mark cleared in the same write that keeps the alert on disk, or here when
// there turns out to be nothing to send. Until then a process that ends
// leaves the mark, and the next start announces the outage (AnnounceOwed).
// Every error returned leaves it set.
func (n *Notifier) Enqueue(ctx context.Context, m store.Monitor, inc store.Incident, event state.Event, at time.Time) error {
	owed := owedBy(inc, event)

	muted, err := n.db.InMaintenance(ctx, m.ID, at)
	if err != nil {
		return err
	}
	if muted {
		if event == state.EventIncidentConfirmed && inc.ID != 0 {
			pending, err := n.db.HasPendingMaintenanceChannels(ctx, inc.ID)
			if err != nil {
				return err
			}
			if pending {
				// Maintenance already holds this incident's alert for
				// some channels and announces it after the window.
				return n.clearOwed(ctx, owed)
			}
			return n.db.DeferOwedToMaintenance(ctx, inc.ID)
		}
		return nil
	}
	// A monitor's own channels, or the instance default when it has none.
	// Without the fallback a monitor nobody remembered to route is a monitor
	// whose outage nobody hears about.
	channels, _, err := n.db.AlertChannels(ctx, m.ID)
	if err != nil {
		return fmt.Errorf("list channels for monitor %d: %w", m.ID, err)
	}
	if len(channels) == 0 {
		// Not an error. With no default set, plenty of monitors are
		// watched on the dashboard and nowhere else, and saying so at every
		// transition would fill the log with a non-event.
		return n.clearOwed(ctx, owed)
	}

	alert := AlertFromStore(m, inc, event, at)
	if event == state.EventIncidentConfirmed && inc.ID != 0 {
		payload, err := alert.Encode()
		if err != nil {
			return err
		}
		var deliveries []store.Delivery
		for _, ch := range channels {
			if ch.Enabled {
				deliveries = append(deliveries, store.Delivery{ChannelID: ch.ID, MonitorID: m.ID, IncidentID: inc.ID, Event: string(event), Payload: payload})
			}
		}
		handled, err := n.db.EnqueueMaintenanceDeliveries(ctx, inc.ID, deliveries)
		if err != nil || handled {
			return err
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// One write for the alert on every channel, so its owed mark is
	// cleared together with the last copy rather than with the first.
	var direct, holding []*pending
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}

		if alert.Notice {
			// A certificate notice is not batched. Grouping exists for an
			// outage seen by twenty monitors at once, and a notice is one
			// certificate's date: it would only make "3 monitors are down"
			// out of one outage and two certificates.
			direct = append(direct, &pending{
				key: groupKey(ch.ID, kindOf(alert)), channel: ch.ID, monitorID: m.ID,
				incidentID: alert.IncidentID, kind: kindOf(alert), alerts: []Alert{alert},
			})
			continue
		}

		b := n.grouper.add(n.batches, ch.ID, m.ID, alert)
		if !n.grouper.enabled() || n.stopped {
			// Grouping off, or the notifier has stopped and nothing would
			// ever close this window: write it now and keep nothing. The
			// outbox delivers it on the next start.
			delete(n.batches, b.key)
			direct = append(direct, b)
			continue
		}
		holding = append(holding, b)
	}

	if len(direct) == 0 && len(holding) == 0 {
		// Every channel it would go to is switched off.
		return n.clearOwed(ctx, owed)
	}
	if len(holding) > 0 {
		// The alert as it is held on disk while it waits, encoded once
		// for every channel. With grouping off, or for a notice, nothing
		// is held. The batches carry the owed mark until a write clears it.
		payload, err := alert.Encode()
		if err != nil {
			for _, b := range holding {
				b.owed = appendOwed(b.owed, owed)
			}
			return err
		}
		n.hold(ctx, holding, m.ID, payload, owed)
		owed = 0
	}
	if len(direct) > 0 {
		n.write(ctx, direct, owedList(owed))
	}
	return nil
}

// hold writes an alert that has joined a batch on each of its channels to
// notif_held, so it outlives a process that ends without its final flush, and
// clears the owed mark of the incident it announces in the same write. The
// caller holds mu.
//
// A failed write is logged, not returned: the alert is in its batches either
// way and goes out when their windows close. Its incident stays owed until
// the last of those batches is written (see releasableOwed), so a process
// that ends before that still announces it on the next start.
func (n *Notifier) hold(ctx context.Context, batches []*pending, monitorID int64, payload string, owed int64) {
	held := make([]store.HeldAlert, len(batches))
	for i, b := range batches {
		held[i] = store.HeldAlert{ChannelID: b.channel, MonitorID: monitorID, Payload: payload, Due: b.deadline}
	}
	ids, err := n.db.HoldAlerts(ctx, held, owed)
	if err != nil {
		// An outage's first alert is still owed on disk, so the next start
		// sends it; a reminder or a recovery is not.
		after := "but not if the process ends before that"
		if owed != 0 {
			after = "or on the next start if the process ends before that"
		}
		n.log.Warn("could not keep a grouped alert on disk; it is sent when its window closes, "+after,
			"monitor_id", monitorID, "channels", len(batches), "error", err)
		for _, b := range batches {
			b.owed = appendOwed(b.owed, owed)
		}
		return
	}
	for i, b := range batches {
		b.held = append(b.held, ids[i])
	}
}

// owedBy returns the incident whose owed alert this is, or zero.
func owedBy(inc store.Incident, event state.Event) int64 {
	if event != state.EventIncidentConfirmed || !inc.AlertOwed {
		return 0
	}
	return inc.ID
}

func owedList(id int64) []int64 {
	if id == 0 {
		return nil
	}
	return []int64{id}
}

func appendOwed(owed []int64, id int64) []int64 {
	if id == 0 {
		return owed
	}
	return append(owed, id)
}

// clearOwed records that an owed alert has nothing to send.
func (n *Notifier) clearOwed(ctx context.Context, owed int64) error {
	if owed == 0 {
		return nil
	}
	return n.db.ClearAlertOwed(ctx, owed)
}

// AnnounceOwed sends the alert of every open outage that the last process
// confirmed and ended before keeping its alert on disk (see
// store.Incident.AlertOwed). It goes through Enqueue like any alert, so the
// routing, maintenance and grouping of the moment decide where it goes, and
// its mark is cleared in the same write as for a fresh one.
//
// Call it once on start, before anything else can confirm an outage: one
// confirmed after the read and not yet held would be announced twice.
func (n *Notifier) AnnounceOwed(ctx context.Context) error {
	owed, err := n.db.OwedIncidents(ctx)
	if err != nil {
		return err
	}
	// One that cannot be announced now stays owed and is tried on the next
	// start; it does not hold up the rest.
	var errs []error
	for _, inc := range owed {
		m, err := n.db.GetMonitor(ctx, inc.MonitorID)
		if err == nil {
			err = n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, inc.ConfirmedAt)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("announce incident %d: %w", inc.ID, err))
		}
	}
	if len(owed) > len(errs) {
		n.log.Info("announced outages the last run confirmed without writing their alert",
			"incidents", len(owed)-len(errs))
	}
	return errors.Join(errs...)
}

// restoreHeld reads back the alerts a process left waiting in their windows
// when it ended without its final flush, and puts each into its batch with
// the deadline it had. One that is already due goes out on the first pass of
// Run; one whose window is still open waits out the rest of it, and an alert
// raised in that time still joins it.
//
// It merges into any batch an Enqueue opened before Run started, putting the
// restored alerts first, since they happened first, and keeping the earlier
// deadline. That batch's own alerts are on disk too, and are left to it.
func (n *Notifier) restoreHeld(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Read under the lock, so an alert held after this read cannot be
	// mistaken for one the last run left behind.
	held, err := n.db.ListHeldAlerts(ctx)
	if err != nil {
		n.log.Error("could not read alerts left waiting by the last run; they are sent on the next start",
			"error", err)
		return
	}
	live := make(map[int64]bool)
	for _, b := range n.batches {
		for _, id := range b.held {
			live[id] = true
		}
	}

	restored := make(map[string]*pending)
	var order []string
	count := 0
	for _, h := range held {
		if live[h.ID] {
			continue
		}
		count++
		a, err := DecodeAlert(h.Payload)
		if err != nil {
			// Written by this code from an Alert, so this is a damaged
			// row rather than a message. Kept, it would be read and
			// refused again on every start.
			n.log.Error("could not read an alert left waiting by the last run; discarding it",
				"channel_id", h.ChannelID, "monitor_id", h.MonitorID, "error", err)
			if err := n.db.ReleaseHeldAlerts(ctx, []int64{h.ID}); err != nil {
				n.log.Error("could not discard an unreadable waiting alert", "id", h.ID, "error", err)
			}
			continue
		}
		kind := kindOf(a)
		key := groupKey(h.ChannelID, kind)
		b, ok := restored[key]
		if !ok {
			b = &pending{key: key, channel: h.ChannelID, deadline: h.Due, monitorID: h.MonitorID, kind: kind}
			restored[key] = b
			order = append(order, key)
		}
		if h.Due.Before(b.deadline) {
			b.deadline = h.Due
		}
		b.alerts = append(b.alerts, a)
		b.held = append(b.held, h.ID)
		if b.incidentID == 0 {
			b.incidentID = a.IncidentID
		}
	}

	for _, key := range order {
		b := restored[key]
		if live, ok := n.batches[key]; ok {
			b.alerts = append(b.alerts, live.alerts...)
			b.held = append(b.held, live.held...)
			b.owed = append(b.owed, live.owed...)
			if live.deadline.Before(b.deadline) {
				b.deadline = live.deadline
			}
			if b.incidentID == 0 {
				b.incidentID = live.incidentID
			}
		}
		n.batches[key] = b
	}
	if count > 0 {
		n.log.Info("restored alerts left waiting by the last run", "alerts", count, "batches", len(order))
	}
}

// flushDue writes out every batch whose window has closed.
//
// Called from the sweep loop rather than from a timer of its own: the sweeper
// already runs often enough, and one clock is easier to reason about — and to
// test — than two.
func (n *Notifier) flushDue(ctx context.Context) {
	n.mu.Lock()
	due := n.grouper.due(n.batches)
	owed := n.releasableOwed(due)
	n.mu.Unlock()

	n.write(ctx, due, owed)
}

// releasableOwed returns the owed incidents that the given batches, just
// taken out of batches, are the last to carry. An alert whose copies could not
// be held is owed until it is on disk for every channel. The caller holds mu.
func (n *Notifier) releasableOwed(taken []*pending) []int64 {
	var out []int64
	seen := make(map[int64]bool)
	for _, b := range taken {
		for _, id := range b.owed {
			if seen[id] {
				continue
			}
			seen[id] = true
			if !n.carriesOwed(id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// carriesOwed reports whether a batch still waiting carries an owed alert
// for the incident. The caller holds mu.
func (n *Notifier) carriesOwed(id int64) bool {
	for _, b := range n.batches {
		for _, o := range b.owed {
			if o == id {
				return true
			}
		}
	}
	return false
}

// flushAll writes out every pending batch regardless of its window.
//
// An alert that is sitting in a window when the process stops must not be
// lost: it is already a real event, and the outbox is what makes it survive a
// restart. Run calls stop for that; tests call this to close every window at
// once and keep grouping afterwards.
func (n *Notifier) flushAll(ctx context.Context) {
	n.mu.Lock()
	all := n.takeAll()
	owed := n.releasableOwed(all)
	n.mu.Unlock()

	n.write(ctx, all, owed)
}

// stop is the final flush: it writes out every pending batch and turns
// grouping off for whatever is enqueued after it (see stopped).
//
// The lock is held across the writes, not only across emptying the map. An
// Enqueue that waited on it then writes after the batches it might follow,
// so a recovery cannot reach the outbox ahead of the alert it closes. The
// writes are a handful of local inserts, and Enqueue already writes under
// this lock when grouping is off.
func (n *Notifier) stop(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.stopped = true
	all := n.takeAll()
	n.write(ctx, all, n.releasableOwed(all))
}

// takeAll empties batches and returns them in the order a channel hears them:
// alerts, then reminders, then recoveries, since the outbox keeps a channel's
// messages in the order they are written. The caller holds mu.
func (n *Notifier) takeAll() []*pending {
	var all []*pending
	for key, b := range n.batches {
		all = append(all, b)
		delete(n.batches, key)
	}
	sortBatches(all)
	return all
}

// write turns each batch into one outbox row, in one transaction that also
// removes the batches' held copies and clears the owed marks in owed.
//
// When that write fails the batches' alerts stay held and their incidents
// owed, so the next start sends them.
func (n *Notifier) write(ctx context.Context, batches []*pending, owed []int64) {
	var (
		writes  []store.BatchWrite
		kept    []*pending
		grouped []bool
	)
	for _, b := range batches {
		if len(b.alerts) == 0 {
			continue
		}
		summary := Summarise(b.alerts)
		payload, err := summary.Encode()
		if err != nil {
			n.log.Error("could not encode grouped alert", "channel_id", b.channel, "error", err)
			continue
		}
		writes = append(writes, store.BatchWrite{
			Delivery: b.delivery(payload, state.Event(summary.Event), n.now()),
			Held:     b.held,
		})
		kept = append(kept, b)
		grouped = append(grouped, summary.Grouped())
	}
	if len(writes) == 0 && len(owed) == 0 {
		return
	}

	dropped, err := n.db.EnqueueBatches(ctx, writes, owed)
	if err != nil {
		// The batches' alerts kept in notif_held stay there, and their
		// incidents owed, so the next start sends them. An alert whose
		// hold failed too (a reminder or recovery: only first alerts are
		// owed) has no copy left, which the hold's log line said.
		n.log.Error("could not queue notification",
			"batches", len(writes), "error", err)
		return
	}
	for i, b := range kept {
		switch {
		case errors.Is(dropped[i], store.ErrNotFound):
			// Every monitor in the batch was deleted while it waited for
			// company. Their messages go with them, as their queued and
			// sent ones do; that is not a failure to report.
			n.log.Info("notification dropped: its monitors were deleted",
				"channel_id", b.channel, "monitors", len(b.alerts))
		case errors.Is(dropped[i], store.ErrUnknownChannel):
			// The channel was deleted while the batch waited. Its queued
			// messages went with it, and so does this one.
			n.log.Info("notification dropped: its channel was deleted",
				"channel_id", b.channel, "monitors", len(b.alerts))
		case grouped[i]:
			n.log.Info("grouped alert queued",
				"channel_id", b.channel, "monitors", len(b.alerts), "event", writes[i].Delivery.Event)
		}
	}
}

// Run drains the outbox until the context is cancelled.
func (n *Notifier) Run(ctx context.Context) {
	n.log.Info("notifier started", "interval", n.interval)

	// Before the first flush, so an alert whose window closed while the
	// process was down goes out on the first pass. A local read, so it is
	// not cut short by a stop that arrives as Run starts: the stop's flush
	// then writes what it restored.
	n.restoreHeld(context.WithoutCancel(ctx))

	for {
		// Close any grouping window that has expired before looking for
		// due rows, so a batch that came of age during the last wait is
		// sent on this pass rather than the next one.
		n.flushDue(ctx)

		sent, err := n.sweep(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			n.log.Error("notification sweep failed", "error", err)
		}

		// After the sweep, so a delivery that gave up on this pass is
		// reported on this pass too.
		n.reportFailures(ctx)

		// A sweep that did work looks again straight away: a backlog
		// should drain at the speed of the endpoints, not the speed of
		// the poll interval.
		delay := n.interval
		if err == nil && sent > 0 {
			delay = 0
		}

		select {
		case <-ctx.Done():
			// Write out whatever is still inside its window, and
			// everything enqueued from here on as it arrives. These
			// are real events that have already happened; losing
			// them to a restart would be the one failure mode the
			// outbox exists to prevent. Use a fresh context, since
			// the one that just ended cannot carry a write.
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			n.stop(flushCtx)
			cancel()

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
	clear(n.rewritten)

	for _, d := range due {
		if ctx.Err() != nil {
			return len(due), ctx.Err()
		}
		if err := n.attempt(ctx, d); err != nil {
			return len(due), err
		}
	}
	return len(due), nil
}

// attempt performs one delivery and records the outcome.
func (n *Notifier) attempt(ctx context.Context, d store.Delivery) error {
	if n.rewritten[d.ID] {
		// A recovery earlier in this sweep took this row's news (see
		// mergeEarlier); what was loaded is no longer what it says.
		return nil
	}
	if d.QuietHeld {
		// Loaded in the same batch as a row that has since folded this
		// one into a digest: its content is already on its way.
		held, err := n.db.StillHeld(ctx, d.ID)
		if err != nil {
			return err
		}
		if !held {
			return nil
		}
	}

	ch, err := n.db.GetChannel(ctx, d.ChannelID)
	if err != nil {
		// The channel was deleted while this was queued. There is
		// nowhere to send it and never will be.
		n.fail(ctx, d, fmt.Sprintf("channel %d no longer exists", d.ChannelID))
		return nil
	}

	if !ch.Enabled {
		n.fail(ctx, d, "channel is disabled")
		return nil
	}

	sender, ok := n.senders[ch.Type]
	if !ok {
		n.fail(ctx, d, fmt.Sprintf("no implementation for channel type %q", ch.Type))
		return nil
	}

	alert, err := DecodeAlert(d.Payload)
	if err != nil {
		n.fail(ctx, d, "stored alert could not be read: "+err.Error())
		return nil
	}

	alert, keep, held, filterErr := n.filterMaintenance(ctx, alert, d.ChannelID)
	if filterErr != nil {
		n.log.Error("could not evaluate maintenance for delivery", "delivery", d.ID, "error", filterErr)
		return n.retryMaintenance(ctx, d, filterErr)
	}
	payload, err := alert.Encode()
	if err != nil {
		return nil
	}
	if err := n.db.FilterMaintenanceDelivery(ctx, d, payload, keep, held); err != nil {
		n.log.Error("could not persist maintenance delivery decision", "delivery", d.ID, "error", err)
		return n.retryMaintenance(ctx, d, err)
	}
	if !keep {
		return nil
	}

	// Quiet hours come after maintenance: an alert that maintenance
	// suppresses should not be held overnight only to be suppressed at 07:00.
	handled, err := n.applyQuietHours(ctx, d)
	if err != nil {
		n.log.Error("could not apply quiet hours to delivery", "delivery", d.ID, "error", err)
		return n.retryMaintenance(ctx, d, err)
	}
	if handled {
		return nil
	}

	// A recovery does not overtake its own alert: it replaces it. After
	// quiet hours, so a recovery that arrives in the night is held and
	// folded into the morning's digest with its alert rather than merged
	// here.
	merged, err := n.mergeEarlier(ctx, d, alert)
	if err != nil {
		n.log.Error("could not check delivery order", "delivery", d.ID, "error", err)
		return n.retryMaintenance(ctx, d, err)
	}
	if merged {
		return nil
	}

	// Last, a channel may decline the alert on grounds of its own, such as
	// an SMS channel's hourly limit. It comes after quiet hours so that an
	// alert held overnight is judged when it would actually go out.
	if w, ok := sender.(Withholder); ok {
		if reason := w.Withhold(ch.Config, alert, n.now()); reason != "" {
			if err := n.db.WithholdDelivery(ctx, d.ID, reason); err != nil {
				n.log.Error("could not record withheld delivery", "delivery", d.ID, "error", err)
				return n.retryMaintenance(ctx, d, err)
			}
			n.log.Info("alert withheld by channel",
				"monitor", alert.MonitorName, "channel", ch.Name, "event", alert.Event, "reason", reason)
			return nil
		}
	}

	// The row rides along so a sender can name this delivery the same way
	// on every attempt, whatever maintenance has since taken out of it.
	sendCtx, cancel := context.WithTimeout(withDeliveryKey(ctx, d), defaultTimeout)
	defer cancel()

	// Links are added here, at the last moment, rather than stored with
	// the alert: what is in the outbox stays the alert itself, and a
	// retry after the address changed links to where SubGlance is now.
	//
	// The mute link is signed for this channel and runs from this send,
	// so a retry carries a link that works for as long as the first would
	// have from the moment it arrives.
	linked := withMuteLink(withLinks(alert, n.baseURL), n.baseURL, n.muteLinks, ch.ID, n.now())
	err = n.send(sendCtx, sender, ch.Config, linked)
	if err == nil {
		n.count(ch.Type, outcomeDelivered)
		if err := n.db.MarkDelivered(ctx, d.ID); err != nil {
			n.log.Error("delivery sent but not recorded", "delivery", d.ID, "error", err)
		}
		// An alert arrived, so whatever spell of failures this channel
		// was in is over, and the next one deserves a notice of its own.
		if err := n.db.ClearChannelFailure(ctx, ch.ID); err != nil {
			n.log.Error("could not clear channel failure", "channel", ch.Name, "error", err)
		}
		n.log.Info("alert delivered",
			"monitor", alert.MonitorName, "channel", ch.Name, "event", alert.Event)
		return nil
	}

	var r *Retryable
	if !errors.As(err, &r) {
		// Permanent: a bad URL, a revoked token, a rejected body.
		n.log.Error("alert rejected",
			"monitor", alert.MonitorName, "channel", ch.Name, "error", err)
		n.giveUp(ctx, d, ch, err.Error())
		return nil
	}

	next := d.Attempts + 1
	if next >= maxAttempts {
		n.log.Error("alert gave up after retries",
			"monitor", alert.MonitorName, "channel", ch.Name,
			"attempts", next, "error", err)
		n.giveUp(ctx, d, ch, fmt.Sprintf("gave up after %d attempts: %s", next, err))
		return nil
	}

	n.count(ch.Type, outcomeRetried)
	delay := backoff(next)

	n.log.Warn("alert delivery failed, will retry",
		"monitor", alert.MonitorName, "channel", ch.Name,
		"attempt", next, "retry_in", delay, "error", err)

	if err := n.db.MarkRetry(ctx, d.ID, err.Error(), n.now().Add(delay)); err != nil {
		n.log.Error("could not schedule retry", "delivery", d.ID, "error", err)
	}
	return nil
}

// retryMaintenance keeps the delivery pending until its prerequisite (a
// maintenance or quiet-hours decision) can be
// evaluated and persisted, without charging an attempt before Send. Repeated
// prerequisite failures keep the current delivery backoff. A failed deferral
// write reaches Run so even a database
// that cannot write cannot turn the outbox into a busy loop.
func (n *Notifier) retryMaintenance(ctx context.Context, d store.Delivery, cause error) error {
	return n.db.DeferDelivery(ctx, d.ID, cause.Error(), n.now().Add(backoff(d.Attempts+1)))
}

// fail dead-letters a delivery.
func (n *Notifier) fail(ctx context.Context, d store.Delivery, cause string) {
	if err := n.db.MarkFailed(ctx, d.ID, cause); err != nil {
		n.log.Error("could not record delivery failure", "delivery", d.ID, "error", err)
	}
}

// giveUp dead-letters a delivery that its channel could not take, and records
// the channel as failing so that reportFailures tells someone through another
// one.
//
// Only a failure of the channel itself comes here: a permanent rejection, or
// retries run out. A delivery to a channel that was deleted or disabled goes
// to fail alone, because nothing is wrong with a channel the operator turned
// off.
func (n *Notifier) giveUp(ctx context.Context, d store.Delivery, ch store.Channel, cause string) {
	n.count(ch.Type, outcomeFailed)
	n.fail(ctx, d, cause)
	if err := n.db.RecordChannelFailure(ctx, ch.ID, n.now(), cause); err != nil {
		n.log.Error("could not record channel failure", "channel", ch.Name, "error", err)
		return
	}
	n.nextFailureCheck.Store(0)
}

// defaultSenders is one sender per channel type, the set a Notifier delivers
// through unless its options name others.
func defaultSenders(guard *checker.Guard) map[string]Sender {
	return map[string]Sender{
		store.ChannelWebhook:  NewWebhookSender(guard),
		store.ChannelDiscord:  NewDiscordSender(guard),
		store.ChannelSlack:    NewSlackSender(guard),
		store.ChannelTelegram: NewTelegramSender(guard),
		store.ChannelEmail:    NewEmailSender(guard),
		store.ChannelNtfy:     NewNtfySender(guard),
		store.ChannelGotify:   NewGotifySender(guard),
		store.ChannelSMS:      NewSMSSender(guard),
	}
}

// configRules is the senders whose Validate ValidateConfig asks. Validation
// reads only the settings and never dials, so these need no guard.
var configRules = defaultSenders(nil)

// ValidateConfig checks a channel's settings against the rules its sender
// holds them to, without a Notifier and without sending anything.
//
// The API calls it when a channel is saved. Delivery does not validate: an
// email channel saved without a host dials ":587", and one with port 99999
// fails its dial, and both are retried as if the server were down until the
// alert is given up — so a setting the sender refuses has to be refused on
// the form, or the first sign of it is an outage nobody was told about.
// Every refusal names the config key it is about (ConfigKey).
func ValidateConfig(channelType string, cfg map[string]string) error {
	sender, ok := configRules[channelType]
	if !ok {
		return fmt.Errorf("unknown channel type %q", channelType)
	}
	return sender.Validate(cfg)
}

// ValidateEditedConfig is ValidateConfig for an edit of a channel whose
// settings are stored, where a value saved under an earlier rule is kept
// through an edit that does not change it. A rename, switching a channel off
// or an import of its own export cannot be refused over a setting the edit
// does not touch, and switching off is what an operator does with a channel
// that fails. So an edit that leaves every setting as stored is accepted
// whatever rules came after the channel was saved: an email channel saved
// with port 99999, or a webhook to port 0, before those were refused. An
// edit that changes any setting is held to every rule, except that a
// webhook's header lines, when they are the ones stored, are held only to
// the Name: value form. stored is the channel as it is now; nil, or another
// type, means there is nothing to keep.
func ValidateEditedConfig(channelType string, cfg map[string]string, stored *store.Channel) error {
	if stored != nil && stored.Type == channelType && maps.Equal(cfg, stored.Config) {
		return nil
	}
	if channelType == store.ChannelWebhook && stored != nil && stored.Type == store.ChannelWebhook &&
		cfg["headers"] == stored.Config["headers"] {
		return validateWebhook(cfg, false)
	}
	return ValidateConfig(channelType, cfg)
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
		MonitorName: testAlertName,
		MonitorType: "http",
		Target:      "This is a test alert. No monitor is down.",
		Event:       string(state.EventIncidentResolved),
		At:          n.now(),
	}

	sendCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	if err := n.send(sendCtx, sender, ch.Config, alert); err != nil {
		return err
	}
	// A test that arrived proves the channel delivers again, which is what
	// an operator who has just fixed a revoked webhook presses it for. A
	// failed test changes nothing: it is reported to the person who
	// pressed the button, at the moment they pressed it.
	if err := n.db.ClearChannelFailure(ctx, ch.ID); err != nil {
		n.log.Error("could not clear channel failure", "channel", ch.Name, "error", err)
	}
	return nil
}
