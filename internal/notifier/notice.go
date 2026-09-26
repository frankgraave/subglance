package notifier

import (
	"context"
	"errors"
	"fmt"
)

// EventBackupFailed is the event of the notice sent when a scheduled backup
// fails. It is not a state-engine event: no monitor is involved, and a webhook
// consumer that switches on "event" needs a value it has never seen before
// rather than one it will mistake for an outage.
const EventBackupFailed = "backup_failed"

// ErrNoticeNotSent means a notice had nowhere to go right now: no default
// channel, the default channel is disabled, or it is inside its quiet hours.
// The caller decides whether to try again later.
var ErrNoticeNotSent = errors.New("notice not sent")

// SendNotice delivers a message about SubGlance itself — not about a monitor —
// to the instance's default channel, straight away.
//
// It bypasses the outbox on purpose. Every outbox row belongs to a monitor,
// and the only notices today are about backups, whose caller already retries
// on a schedule of its own: a failed backup is tried again within the hour,
// and a notice that could not be sent is tried again with it. Holding a row
// for a monitor that does not exist would be the wrong model to save a retry
// loop that already exists.
//
// It goes to the default channel because that is the one channel an operator
// named as "where things go when nothing more specific applies", which is
// exactly what a notice about the instance is. With no default set, nothing is
// sent and the caller is told, so it can say so in its log.
//
// Quiet hours are honoured by not sending: a failed backup is worth knowing
// about in the morning, not at 03:00, and the next retry after the window will
// send it.
func (n *Notifier) SendNotice(ctx context.Context, a Alert) error {
	ch, ok, err := n.db.DefaultChannel(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no default channel is set", ErrNoticeNotSent)
	}
	if !ch.Enabled {
		return fmt.Errorf("%w: the default channel %q is disabled", ErrNoticeNotSent, ch.Name)
	}
	q, hasQuiet, err := n.db.GetQuietHours(ctx, ch.ID)
	if err != nil {
		return err
	}
	if hasQuiet && q.Active(n.now()) {
		return fmt.Errorf("%w: the default channel %q is in its quiet hours", ErrNoticeNotSent, ch.Name)
	}
	sender, ok := n.senders[ch.Type]
	if !ok {
		return fmt.Errorf("unknown channel type %q", ch.Type)
	}
	if a.At.IsZero() {
		a.At = n.now()
	}
	sendCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	return sender.Send(sendCtx, ch.Config, a)
}
