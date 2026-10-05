package notifier

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// EventChannelFailing is the event of the notice sent when a channel stops
// delivering: an alert through it gave up, either refused outright or out of
// retries. It goes through another channel, once per spell of failures, and
// names the failing channel and its last error with the credentials taken out.
//
// Without it, a revoked webhook or an expired mail password is visible only
// to whoever opens the notifications screen, and the next outage is confirmed
// and told to nobody.
const EventChannelFailing = "channel_failing"

// failureRecheck is how often the notifier looks again at a failing channel
// whose notice has not gone out, for example because every other channel is
// in its quiet hours. A delivery that gives up does not wait for it: the
// notice about it is attempted on the same pass.
const failureRecheck = time.Minute

// Delivery outcomes, as /metrics labels them.
const (
	outcomeDelivered = "delivered"
	outcomeFailed    = "failed"
	outcomeRetried   = "retried"
)

// deliveryOutcomes is every outcome, in the order /metrics writes them.
var deliveryOutcomes = []string{outcomeDelivered, outcomeFailed, outcomeRetried}

type deliveryCounts struct {
	delivered, failed, retried atomic.Uint64
}

// DeliveryCount is one series of subglance_notification_deliveries_total.
type DeliveryCount struct {
	ChannelType string
	Outcome     string
	Count       uint64
}

// count adds one outcome for a channel type.
func (n *Notifier) count(typ, outcome string) {
	c := n.counts[typ]
	if c == nil {
		return
	}
	switch outcome {
	case outcomeDelivered:
		c.delivered.Add(1)
	case outcomeFailed:
		c.failed.Add(1)
	case outcomeRetried:
		c.retried.Add(1)
	}
}

// DeliveryCounts reports how alert deliveries have gone since the process
// started, one entry per channel type and outcome, zeros included.
//
// The zeros are there on purpose: a series that appears only after the first
// failure makes rate() blind to that first failure, which is the one an alert
// rule exists to catch. Test sends and notices about SubGlance itself are not
// alert deliveries and are not counted.
func (n *Notifier) DeliveryCounts() []DeliveryCount {
	types := make([]string, 0, len(n.counts))
	for typ := range n.counts {
		types = append(types, typ)
	}
	slices.Sort(types)

	out := make([]DeliveryCount, 0, len(types)*len(deliveryOutcomes))
	for _, typ := range types {
		c := n.counts[typ]
		out = append(out,
			DeliveryCount{typ, outcomeDelivered, c.delivered.Load()},
			DeliveryCount{typ, outcomeFailed, c.failed.Load()},
			DeliveryCount{typ, outcomeRetried, c.retried.Load()},
		)
	}
	return out
}

// ChannelFailingNotice builds the notice about a channel whose alerts stopped
// arriving at f.FailedAt. The error is redacted with the failing channel's own
// config, because the notice goes to a different channel and possibly to
// different people.
func ChannelFailingNotice(ch store.Channel, f store.ChannelFailure, at time.Time) Alert {
	return Alert{
		MonitorName: "SubGlance",
		Target:      fmt.Sprintf("%s (%s)", ch.Name, ch.Type),
		Event:       EventChannelFailing,
		StartedAt:   f.FailedAt,
		At:          at,
		LastError:   RedactError(f.LastError, ch.Config),
	}
}

// reportFailures sends the notice about every failing channel that has not
// had one yet.
//
// It runs on the notifier's own loop, after each sweep, and reads the
// database at most once a minute unless a delivery has just given up. It
// follows SendNotice in what it honours: it sends straight away rather than
// through the outbox, and never into a channel inside its quiet hours. A
// notice that found nowhere to go is tried again on the next check, so a
// failure at 03:00 is reported when the first channel's quiet hours end.
func (n *Notifier) reportFailures(ctx context.Context) {
	now := n.now()
	if next := n.nextFailureCheck.Load(); next != 0 && now.UnixNano() < next {
		return
	}
	n.nextFailureCheck.Store(now.Add(failureRecheck).UnixNano())

	failing, err := n.db.ChannelFailures(ctx)
	if err != nil {
		n.log.Error("could not read failing channels", "error", err)
		return
	}
	if len(failing) == 0 {
		return
	}
	channels, err := n.db.ListChannels(ctx)
	if err != nil {
		n.log.Error("could not list channels for a failure notice", "error", err)
		return
	}

	ids := make([]int64, 0, len(failing))
	for id := range failing {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		f := failing[id]
		idx := slices.IndexFunc(channels, func(c store.Channel) bool { return c.ID == id })
		// A disabled channel delivers nothing because someone chose that,
		// which is not news to them.
		if !f.NoticedAt.IsZero() || idx < 0 || !channels[idx].Enabled {
			continue
		}
		n.reportFailure(ctx, channels[idx], f, store.NoticeCandidates(channels, failing, id), now)
	}
}

// reportFailure sends one notice through the first candidate that will take
// it, and records which one did.
func (n *Notifier) reportFailure(ctx context.Context, ch store.Channel, f store.ChannelFailure, candidates []store.Channel, now time.Time) {
	if len(candidates) == 0 {
		// The notifications screen and the dashboard say so; the log says
		// it once per spell rather than once a minute.
		if n.unreported[ch.ID] != f.FailedAt {
			n.unreported[ch.ID] = f.FailedAt
			n.log.Warn("a channel is failing and there is no other channel to report it through",
				"channel", ch.Name, "since", f.FailedAt)
		}
		return
	}
	delete(n.unreported, ch.ID)

	notice := ChannelFailingNotice(ch, f, now)
	for _, via := range candidates {
		q, quiet, err := n.db.GetQuietHours(ctx, via.ID)
		if err != nil {
			n.log.Error("could not read quiet hours for a failure notice", "channel", via.Name, "error", err)
			continue
		}
		if quiet && q.Active(now) {
			continue
		}
		sender, ok := n.senders[via.Type]
		if !ok {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
		err = sender.Send(sendCtx, via.Config, notice)
		cancel()
		if err != nil {
			n.log.Warn("could not send a failure notice, trying the next channel",
				"failing", ch.Name, "via", via.Name, "error", err)
			continue
		}
		if err := n.db.MarkChannelFailureNoticed(ctx, f, via.ID, now); err != nil {
			n.log.Error("failure notice sent but not recorded", "channel", ch.Name, "error", err)
		}
		n.log.Info("failure notice sent", "failing", ch.Name, "via", via.Name)
		return
	}
}
