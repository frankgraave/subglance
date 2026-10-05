package notifier

import (
	"context"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A channel hears about an outage in the order it happened: the alert first,
// the recovery after it.
//
// The outbox does not give that for free. Every row has its own retry
// schedule, so when an alert's first attempt failed and the monitor recovered
// while the retry was still pending, the recovery was due at once and went
// out first. The alert followed at its next retry, minutes later, reporting
// an outage that was already over to someone who would then go looking for
// it. A false alarm, just delivered out of order.
//
// The rule is per incident and per channel. A message that reports an
// incident back up waits while an older message to the same channel, one
// that reports that incident down, is still waiting to go out. Messages about
// other incidents, and other channels, do not wait for each other: a phone
// that got the alert gets the recovery at once, however long a broken webhook
// takes over its copy.
//
// The grouping window can invert the order before anything reaches the
// outbox; Grouper.due holds a batch of recoveries back for that case, so the
// rule here only has to compare rows.

// recoveredIncidents returns the incidents a message reports as back up. A
// grouped message or a quiet-hours digest can report several.
func recoveredIncidents(a Alert) map[int64]bool {
	out := map[int64]bool{}
	for _, l := range alertLeaves(a) {
		if l.IncidentID != 0 && !l.Down() {
			out[l.IncidentID] = true
		}
	}
	return out
}

// reportsDown reports whether a message says one of the incidents is down: an
// alert, a reminder, or a grouped message or digest with one of those in it.
func reportsDown(a Alert, incidents map[int64]bool) bool {
	for _, l := range alertLeaves(a) {
		if incidents[l.IncidentID] && l.Down() {
			return true
		}
	}
	return false
}

// alertLeaves flattens a message into the single alerts it carries.
func alertLeaves(a Alert) []Alert {
	if len(a.Members) == 0 {
		return []Alert{a}
	}
	var out []Alert
	for _, m := range a.Members {
		out = append(out, alertLeaves(m)...)
	}
	return out
}

// waitForEarlier holds a recovery back while its channel still has an older
// alert about the same incident to send. It reports true when it postponed d,
// which then waits until the earliest such alert is next tried; it is checked
// again then, after that alert, since the outbox sends due rows oldest first.
//
// Waiting costs the delivery nothing: no attempt is charged and no error is
// recorded, because nothing failed.
//
// An alert that gives up for good stops holding the recovery back, and the
// recovery goes out on its own. Hearing "back up" without the "down" before it
// is odd; hearing nothing at all about an outage is worse, and holding the
// recovery forever would turn one broken delivery into two.
func (n *Notifier) waitForEarlier(ctx context.Context, d store.Delivery, a Alert) (bool, error) {
	up := recoveredIncidents(a)
	if len(up) == 0 {
		return false, nil
	}

	earlier, err := n.db.PendingBefore(ctx, d.ChannelID, d.ID)
	if err != nil {
		return false, err
	}

	var until time.Time
	for _, e := range earlier {
		ea, err := DecodeAlert(e.Payload)
		if err != nil {
			// Its own attempt dead-letters it, and then it holds
			// nothing back; it says nothing readable meanwhile.
			continue
		}
		if !reportsDown(ea, up) {
			continue
		}
		if until.IsZero() || e.NextAttemptAt.Before(until) {
			until = e.NextAttemptAt
		}
	}
	if until.IsZero() {
		return false, nil
	}

	// An alert that is already due is attempted in this sweep or the next,
	// before this row: due rows go oldest first.
	if now := n.now(); until.Before(now) {
		until = now
	}
	n.log.Info("recovery waits for the alert before it",
		"delivery", d.ID, "channel_id", d.ChannelID, "until", until)
	return true, n.db.PostponeDelivery(ctx, d.ID, until)
}
