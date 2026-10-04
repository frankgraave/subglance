package api

import (
	"context"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

// Delivery states, as a channel's delivery record names them.
const (
	// deliveryFailed: the newest delivery to finish gave up after its
	// retries, and nothing has arrived since.
	deliveryFailed = "failed"
	// deliveryRetrying: no delivery has given up since the last one that
	// arrived, but at least one is still waiting after a failed attempt.
	deliveryRetrying = "retrying"
	// deliveryDelivered: the newest delivery to finish arrived, and none is
	// being retried.
	deliveryDelivered = "delivered"
	// deliveryNone: no alert went through the channel inside the window. It
	// is not a verdict: a channel nobody needed has proved nothing either way.
	deliveryNone = "none"
)

// channelDelivery is how a channel's alerts have gone recently.
//
// The window is the outbox's own retention: delivered rows are deleted after
// store.DeliveryLogRetention, so a longer window would compare a failure it
// can see with successes that are already gone, and paint a channel red that
// in fact recovered.
type channelDelivery struct {
	State      string `json:"state"`
	WindowDays int    `json:"window_days"`

	LastDeliveredAt *time.Time `json:"last_delivered_at"`
	LastFailedAt    *time.Time `json:"last_failed_at"`

	Failed   int `json:"failed"`
	Pending  int `json:"pending"`
	Retrying int `json:"retrying"`

	// LastError is the newest failure's message with the channel's
	// credentials taken out; see redactDeliveryError.
	LastError string `json:"last_error"`

	// FailingSince is set while the channel is in a spell of failures: an
	// alert through it gave up at this moment and none has arrived since.
	// Unlike State it has no window, so a channel that failed forty days
	// ago and was never needed again still says so. It ends when an alert
	// or a test arrives.
	FailingSince *time.Time `json:"failing_since"`
	// NoticeSentAt and NoticeChannelID say when, and through which other
	// channel, the channel_failing notice about this spell went out. Both
	// are null while it has not; the channel id is also null when the
	// channel that carried it has since been deleted.
	NoticeSentAt    *time.Time `json:"notice_sent_at"`
	NoticeChannelID *int64     `json:"notice_channel_id"`
	// Notice is where that notice stands, null while the channel is not
	// failing: "sent"; "waiting", when another channel could carry it but
	// none has yet (quiet hours, or the next check is under a minute
	// away); or "no_other_channel", when every other channel is disabled
	// or failing too, and only this record says it.
	Notice *string `json:"notice"`
}

// Where a failing channel's notice stands; see channelDelivery.Notice.
const (
	noticeSent           = "sent"
	noticeWaiting        = "waiting"
	noticeNoOtherChannel = "no_other_channel"
)

// deliveryState decides which of the four states a channel is in.
//
// A failure outranks everything after it except a delivery that arrived
// later: the dangerous mistake on this screen is a green badge on a channel
// whose alerts are not arriving, so a tie between the two goes to the
// failure, and a retry in flight outranks an older success.
func deliveryState(h store.ChannelHealth) string {
	switch {
	case !h.LastFailedAt.IsZero() && !h.LastFailedAt.Before(h.LastDeliveredAt):
		return deliveryFailed
	case h.Retrying > 0:
		return deliveryRetrying
	case !h.LastDeliveredAt.IsZero():
		return deliveryDelivered
	default:
		return deliveryNone
	}
}

func toChannelDelivery(h store.ChannelHealth, f *store.ChannelFailure, hasOther bool, ch store.Channel) *channelDelivery {
	d := &channelDelivery{
		State:      deliveryState(h),
		WindowDays: int(store.DeliveryLogRetention / (24 * time.Hour)),
		Failed:     h.Failed,
		Pending:    h.Pending,
		Retrying:   h.Retrying,
		LastError:  redactDeliveryError(h.LastError, ch),
	}
	if !h.LastDeliveredAt.IsZero() {
		t := h.LastDeliveredAt
		d.LastDeliveredAt = &t
	}
	if !h.LastFailedAt.IsZero() {
		t := h.LastFailedAt
		d.LastFailedAt = &t
	}
	if f != nil {
		since := f.FailedAt
		d.FailingSince = &since
		if !f.NoticedAt.IsZero() {
			sent := f.NoticedAt
			d.NoticeSentAt = &sent
		}
		if f.NoticeChannelID != 0 {
			via := f.NoticeChannelID
			d.NoticeChannelID = &via
		}
		notice := noticeWaiting
		switch {
		case !f.NoticedAt.IsZero():
			notice = noticeSent
		case !hasOther:
			notice = noticeNoOtherChannel
		}
		d.Notice = &notice
	}
	return d
}

// deliveryRecords is what every channel's delivery record is built from: the
// outbox's outcomes and the spells of failure the notifier keeps.
type deliveryRecords struct {
	health   map[int64]store.ChannelHealth
	failures map[int64]store.ChannelFailure
	// hasOther holds, per failing channel, whether another channel could
	// carry its notice: the notifier's own rule, store.NoticeCandidates.
	hasOther map[int64]bool
}

// channelDeliveries reads every channel's delivery record, or nil when the
// outbox cannot be read.
//
// A failed read is logged and leaves the records null rather than failing
// the request. The list is also where channels are edited, and an outbox
// that cannot be read is no reason to stop someone fixing a channel; null
// reads as "not known", which is what it is, and never as healthy.
//
// Both reads or neither: a record built from the outbox alone would leave
// failing_since null on a channel that is failing, which reads as healthy.
func (s *Server) channelDeliveries(ctx context.Context) *deliveryRecords {
	health, err := s.db.ChannelHealthSince(ctx, time.Now().Add(-store.DeliveryLogRetention))
	if err != nil {
		s.log.Error("read channel delivery health", "error", err)
		return nil
	}
	failures, err := s.db.ChannelFailures(ctx)
	if err != nil {
		s.log.Error("read failing channels", "error", err)
		return nil
	}
	recs := &deliveryRecords{health: health, failures: failures, hasOther: map[int64]bool{}}
	if len(failures) == 0 {
		return recs
	}
	channels, err := s.db.ListChannels(ctx)
	if err != nil {
		s.log.Error("list channels for delivery records", "error", err)
		return nil
	}
	for id := range failures {
		recs.hasOther[id] = len(store.NoticeCandidates(channels, failures, id)) > 0
	}
	return recs
}

// withDelivery fills a response's delivery record. Nil records are a read
// that failed, and leave the record null.
func withDelivery(resp *channelResponse, ch store.Channel, recs *deliveryRecords) {
	if recs == nil {
		return
	}
	var failure *store.ChannelFailure
	if f, ok := recs.failures[ch.ID]; ok {
		failure = &f
	}
	resp.Delivery = toChannelDelivery(recs.health[ch.ID], failure, recs.hasOther[ch.ID], ch)
}

// redactDeliveryError takes a channel's credentials out of a delivery error;
// see notifier.RedactError, which holds the rules so that the notice about a
// failing channel is masked exactly as this list is.
func redactDeliveryError(msg string, ch store.Channel) string {
	return notifier.RedactError(msg, ch.Config)
}
