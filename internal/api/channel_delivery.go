package api

import (
	"context"
	"regexp"
	"strings"
	"time"

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
}

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

func toChannelDelivery(h store.ChannelHealth, ch store.Channel) *channelDelivery {
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
	return d
}

// channelDeliveries reads every channel's delivery record, or nil when the
// outbox cannot be read.
//
// A failed read is logged and leaves the records null rather than failing
// the request. The list is also where channels are edited, and an outbox
// that cannot be read is no reason to stop someone fixing a channel; null
// reads as "not known", which is what it is, and never as healthy.
func (s *Server) channelDeliveries(ctx context.Context) map[int64]store.ChannelHealth {
	health, err := s.db.ChannelHealthSince(ctx, time.Now().Add(-store.DeliveryLogRetention))
	if err != nil {
		s.log.Error("read channel delivery health", "error", err)
		return nil
	}
	return health
}

// withDelivery fills a response's delivery record from health. A nil map is
// a read that failed, and leaves the record null.
func withDelivery(resp *channelResponse, ch store.Channel, health map[int64]store.ChannelHealth) {
	if health == nil {
		return
	}
	resp.Delivery = toChannelDelivery(health[ch.ID], ch)
}

// urlInText finds the URLs in a delivery error.
var urlInText = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// minRedactLen is the shortest credential replaced in an error message.
// Shorter values would match ordinary words and shred the message, and a
// credential that short is not one an error message can meaningfully leak.
const minRedactLen = 4

// redactDeliveryError takes a channel's credentials out of a delivery error.
//
// The notifier stores errors as the transport wrote them, and Go's HTTP
// client writes the whole request URL into every one: Post
// "https://hooks.slack.com/services/…": dial tcp …. For Slack, Discord
// and a plain webhook that URL is the credential, which the config mask
// exists to keep from anybody reading this list, viewers included.
//
// Two passes, because either alone has a gap. Every masked config value is
// replaced by its own mask wherever it appears, which catches a token
// embedded in a longer string and hides a stored URL as a whole, exactly as
// the config mask does. Then every URL that is left keeps its scheme and host
// and loses its path and query, which catches a credential a sender put into
// a URL it built, such as Telegram's bot token in the API path. The host is
// what diagnoses "no such host" or "connection refused"; the path is what
// grants access.
func redactDeliveryError(msg string, ch store.Channel) string {
	if msg == "" {
		return ""
	}
	for k, v := range ch.Config {
		if publicKeys[k] || len(strings.TrimSpace(v)) < minRedactLen {
			continue
		}
		msg = strings.ReplaceAll(msg, v, maskValue(v))
		if t := strings.TrimSpace(v); t != v {
			msg = strings.ReplaceAll(msg, t, maskValue(t))
		}
	}
	return urlInText.ReplaceAllStringFunc(msg, trimURL)
}

// trimURL keeps a URL's scheme and host, and marks that more was there.
func trimURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	host, tail := rest, ""
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		host, tail = rest[:end], rest[end:]
	}
	// Userinfo is a credential too: "https://user:pass@host/".
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if tail == "" || tail == "/" {
		return scheme + "://" + host + tail
	}
	return scheme + "://" + host + "/…"
}
