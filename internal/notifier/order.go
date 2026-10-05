package notifier

import (
	"context"

	"github.com/frankgraave/subglance/internal/state"
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
// The rule is per incident and per channel. When a message that reports an
// incident back up is about to go out while an older message to the same
// channel, one that reports that incident down, is still waiting, the two
// become one message (see mergeEarlier). Messages about other incidents, and
// other channels, do not wait for each other: a phone that got the alert gets
// the recovery at once, however long a broken webhook takes over its copy.
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

// mergeEarlier folds into a recovery the older messages to its channel that
// still have to report the same incident down. It reports true when it did,
// and then d is not sent on this pass: the merged recovery is tried when the
// alert it replaced would have been.
//
// The alternative was to make the recovery wait behind the alert. That kept
// the order, but it still delivered "api is down" for an outage that was
// over by the time the message arrived, and then held the good news back for
// as long as the alert's retries ran. Merged, the channel gets one message
// that says both ("api was down for 3 minutes, now back up"), and gets it no
// later than it would have got the alert.
//
// The merged message takes the replaced alert's attempts, last error and next
// attempt: it replaces that row rather than joining the queue beside it, so
// it gives up when the alert would have, and a channel that was failing still
// shows as failing. The alert's row is closed with a note of where its news
// went; a grouped alert keeps its other monitors and loses only this one.
//
// A reminder still queued for the incident is merged the same way, since
// "still down" after the monitor came back is no truer than "down". It does
// not change the wording: the reader heard the first alert.
//
// An alert that gave up for good is not pending, so it is not merged, and the
// recovery goes out on its own. Hearing "back up" without the "down" before it
// is odd; hearing nothing at all about an outage is worse, and holding the
// recovery forever would turn one broken delivery into two.
func (n *Notifier) mergeEarlier(ctx context.Context, d store.Delivery, a Alert) (bool, error) {
	up := recoveredIncidents(a)
	if len(up) == 0 {
		return false, nil
	}

	earlier, err := n.db.PendingBefore(ctx, d.ChannelID, d.ID)
	if err != nil {
		return false, err
	}

	var (
		replaced []store.Replaced
		from     store.Delivery
		alerted  = map[int64]bool{}
	)
	for _, e := range earlier {
		ea, err := DecodeAlert(e.Payload)
		if err != nil {
			// Its own attempt dead-letters it, and then it holds
			// nothing back; it says nothing readable meanwhile.
			continue
		}
		var kept []Alert
		gone := false
		for _, l := range alertLeaves(ea) {
			if !up[l.IncidentID] || !l.Down() {
				kept = append(kept, l)
				continue
			}
			gone = true
			if state.Event(l.Event) != state.EventIncidentReminder {
				alerted[l.IncidentID] = true
			}
		}
		if !gone {
			continue
		}
		if len(replaced) == 0 {
			// Rows are listed oldest first: the first one replaced is
			// the alert whose retries the merged message follows.
			from = e
		}
		r := store.Replaced{ID: e.ID}
		if len(kept) > 0 {
			rest := Summarise(kept)
			if ea.Digest {
				rest = BuildDigest(kept, ea.DigestZone, ea.At)
			}
			if r.Payload, err = rest.Encode(); err != nil {
				return false, err
			}
		}
		replaced = append(replaced, r)
	}
	if len(replaced) == 0 {
		return false, nil
	}

	markReplaced(&a, alerted)
	payload, err := a.Encode()
	if err != nil {
		return false, err
	}
	// An alert that is already due is attempted in this sweep or the next,
	// and so is the message that replaces it.
	if now := n.now(); from.NextAttemptAt.Before(now) {
		from.NextAttemptAt = now
	}
	if from.QuietHeld {
		// Its quiet hours are over, or the recovery would have been held
		// too; the note saying it was held does not carry over.
		from.LastError = ""
	}
	n.log.Info("recovery replaces the alert still queued before it",
		"delivery", d.ID, "channel_id", d.ChannelID, "replaced", len(replaced), "next_attempt", from.NextAttemptAt)
	if err := n.db.ReplaceWithRecovery(ctx, d.ID, payload, from, replaced); err != nil {
		return false, err
	}
	// The rows just rewritten may sit further down this sweep's batch,
	// loaded before the rewrite: they must not go out as they were.
	for _, r := range replaced {
		n.rewritten[r.ID] = true
	}
	return true, nil
}

// markReplaced flags the recoveries in a whose own alert never reached the
// channel, and reports whether every recovery in it is one. A grouped message
// carries the flag at the top only when all its members do, so its title
// claims no more than is true of each; Summarise keeps the same rule.
func markReplaced(a *Alert, alerted map[int64]bool) bool {
	if len(a.Members) == 0 {
		// Only ever set: a recovery merged once keeps saying so when a
		// later pass finds no alert left to take, only a reminder.
		a.ReplacesAlert = a.ReplacesAlert || (!a.Down() && alerted[a.IncidentID])
		return a.ReplacesAlert
	}
	all := true
	for i := range a.Members {
		if !markReplaced(&a.Members[i], alerted) {
			all = false
		}
	}
	// A digest words every outage as down-and-back-up already.
	a.ReplacesAlert = all && !a.Digest
	return a.ReplacesAlert
}

// replacesAnAlert reports whether a carries a recovery that stands in for an
// alert the channel never received.
func replacesAnAlert(a Alert) bool {
	for _, l := range alertLeaves(a) {
		if l.ReplacesAlert {
			return true
		}
	}
	return false
}
