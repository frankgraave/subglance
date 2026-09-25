package notifier

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// EventQuietDigest is the event of the one message a channel receives when
// its quiet hours end, covering everything that was held.
const EventQuietDigest = "quiet_hours_digest"

// applyQuietHours decides whether a delivery goes out now. It reports true
// when it has dealt with the delivery itself — held it, dropped it, or folded
// it into a digest that the next sweep sends.
//
// Quiet hours are evaluated when a delivery is attempted rather than when it
// is queued. That is what makes them safe to edit: an alert queued at 22:59
// and retried at 23:01 is held like any other, and one held under a window
// that has since been removed is released on the next pass.
func (n *Notifier) applyQuietHours(ctx context.Context, d store.Delivery) (bool, error) {
	q, ok, err := n.db.GetQuietHours(ctx, d.ChannelID)
	if err != nil {
		return false, err
	}

	now := n.now()
	if ok && q.Active(now) {
		if q.During == store.QuietDrop {
			n.log.Info("alert dropped during quiet hours", "delivery", d.ID, "channel_id", d.ChannelID)
			return true, n.db.DropDelivery(ctx, d.ID)
		}
		return true, n.db.HoldDelivery(ctx, d.ID, q.EndAfter(now))
	}
	if !d.QuietHeld {
		return false, nil
	}

	zone := "UTC"
	if ok {
		zone = q.Timezone
	}
	return true, n.foldHeld(ctx, d, zone)
}

// foldHeld turns everything a channel held overnight into one delivery.
//
// The first held row to come due carries the digest; the others are closed in
// the same transaction. A single held alert is released as it was, since a
// digest of one is just the alert with a longer title.
func (n *Notifier) foldHeld(ctx context.Context, d store.Delivery, zone string) error {
	held, err := n.db.HeldDeliveries(ctx, d.ChannelID)
	if err != nil {
		return err
	}

	var (
		alerts []Alert
		folded []int64
		// self is d as persisted. Maintenance may have trimmed the payload
		// before this call, so d.Payload can still name members it removed.
		self = d
	)
	for _, h := range held {
		if h.ID == d.ID {
			self = h
		}
		a, err := DecodeAlert(h.Payload)
		if err != nil {
			// Left held: its own attempt dead-letters it with the
			// decode error, which is where an operator will look.
			continue
		}
		if h.ID != d.ID {
			folded = append(folded, h.ID)
		}
		if len(a.Members) > 0 {
			alerts = append(alerts, a.Members...)
		} else {
			alerts = append(alerts, a)
		}
	}

	if len(folded) == 0 && len(alerts) <= 1 {
		return n.db.FoldDigest(ctx, self.ID, self.Event, self.Payload, nil, n.now())
	}

	digest := BuildDigest(alerts, zone, n.now())
	payload, err := digest.Encode()
	if err != nil {
		return err
	}
	n.log.Info("quiet hours ended, digest queued",
		"channel_id", d.ChannelID, "alerts", len(alerts), "deliveries", len(folded)+1)
	return n.db.FoldDigest(ctx, d.ID, EventQuietDigest, payload, folded, n.now())
}

// BuildDigest renders the message a channel receives when its quiet hours
// end.
//
// It is organised by monitor, not by event. Overnight, one monitor can produce
// a confirmation, four reminders and a recovery; listed as events that is six
// lines about one thing. By monitor it is one line that says what matters at
// 07:00 — whether it is still broken.
func BuildDigest(alerts []Alert, zone string, at time.Time) Alert {
	sorted := make([]Alert, len(alerts))
	copy(sorted, alerts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	out := Alert{
		Members:    sorted,
		Event:      EventQuietDigest,
		At:         at,
		Digest:     true,
		DigestZone: zone,
	}
	if len(sorted) > 0 {
		out.MonitorID = sorted[0].MonitorID
		out.MonitorName = sorted[0].MonitorName
		out.MonitorType = sorted[0].MonitorType
		out.Target = sorted[0].Target
	}
	return out
}

// digestEntry is what the digest says about one monitor.
type digestEntry struct {
	name      string
	down      bool
	startedAt time.Time
	endedAt   time.Time
	cause     string
}

// digestEntries folds a digest's members into one entry per monitor, in the
// order each monitor first appeared.
func digestEntries(a Alert) []*digestEntry {
	var order []*digestEntry
	byKey := map[string]*digestEntry{}
	for _, m := range a.Members {
		key := m.MonitorName
		if m.MonitorID != 0 {
			key = fmt.Sprint(m.MonitorID)
		}
		e, ok := byKey[key]
		if !ok {
			e = &digestEntry{name: m.MonitorName}
			byKey[key] = e
			order = append(order, e)
		}

		started := m.StartedAt
		if started.IsZero() {
			started = m.At
		}
		if state.Event(m.Event) == state.EventIncidentResolved {
			if e.startedAt.IsZero() {
				e.startedAt = started
			}
			e.down = false
			e.endedAt = m.At
			continue
		}
		if !e.down {
			// A new outage, or the first news of this monitor: the
			// line describes the latest one.
			e.startedAt = started
			e.cause = ""
		}
		e.down = true
		e.endedAt = time.Time{}
		if m.Cause != "" {
			e.cause = m.Cause
		}
	}
	return order
}

// digestStillDown counts the monitors a digest reports as still down.
func digestStillDown(entries []*digestEntry) int {
	n := 0
	for _, e := range entries {
		if e.down {
			n++
		}
	}
	return n
}

// DigestTitle leads with the one fact that decides whether to get up.
func DigestTitle(a Alert) string {
	entries := digestEntries(a)
	down := digestStillDown(entries)
	switch {
	case len(entries) == 1 && down == 1:
		return fmt.Sprintf("%s is still down after quiet hours", entries[0].name)
	case len(entries) == 1:
		return fmt.Sprintf("%s went down and recovered during quiet hours", entries[0].name)
	case down > 0:
		return fmt.Sprintf("%d of %d monitors still down after quiet hours", down, len(entries))
	default:
		return fmt.Sprintf("%d monitors went down and recovered during quiet hours", len(entries))
	}
}

// DigestBody lists each monitor once, still-down first, in the channel's own
// timezone: a digest is read by the person whose night it covers.
func DigestBody(a Alert) string {
	const maxNamed = 10

	loc, err := time.LoadLocation(a.DigestZone)
	if err != nil || a.DigestZone == "" {
		loc = time.UTC
	}
	entries := digestEntries(a)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].down && !entries[j].down })

	var b strings.Builder
	shown := entries
	if len(shown) > maxNamed {
		shown = shown[:maxNamed]
	}
	for _, e := range shown {
		start := e.startedAt.In(loc)
		if e.down {
			fmt.Fprintf(&b, "• %s: down since %s", e.name, start.Format("15:04 MST"))
			if e.cause != "" {
				b.WriteString(" — " + e.cause)
			}
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(&b, "• %s: down %s–%s, back up after %s\n", e.name,
			start.Format("15:04"), e.endedAt.In(loc).Format("15:04 MST"),
			durationWords(e.endedAt.Sub(e.startedAt)))
	}
	if rest := len(entries) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "… and %d more\n", rest)
	}
	return strings.TrimRight(b.String(), "\n")
}
