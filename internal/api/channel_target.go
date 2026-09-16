package api

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// TargetGuard decides whether a channel's destination host may be connected to.
//
// An interface rather than a direct dependency on *checker.Guard, for the same
// reason as Prober and ChannelTester: the API has to stay constructible in a
// test and in any build that runs without a checker pipeline behind it. It
// also lets a test supply a resolver that never touches the network — see
// checkChannelTarget on why that matters.
type TargetGuard interface {
	CheckHost(ctx context.Context, host string) error
}

// WithTargetGuard attaches the guard that channel destinations are validated
// against when a channel is saved.
//
// Nil (the default) means no save-time check at all, which is the honest
// behaviour for an API assembled without the delivery pipeline: refusing
// targets we have no policy for would be worse than letting them through, and
// the delivery side refuses them anyway.
func (s *Server) WithTargetGuard(g TargetGuard) *Server {
	s.targetGuard = g
	return s
}

// channelTargetLookupTimeout bounds the guard's hostname lookup.
//
// checkChannelTarget runs on the request path of a save, so an unbounded
// lookup would hold an HTTP handler open for as long as a slow or hostile
// resolver cared to stall — a hostname in a config field is enough to park a
// handler, which is a small denial-of-service surface built out of a
// convenience feature. Three seconds is far longer than a working resolver
// needs and far shorter than a person will wait before hitting save again.
const channelTargetLookupTimeout = 3 * time.Second

// channelTarget names the config field holding a channel's destination host,
// and the host itself.
//
// Which field that is, per type, and why:
//
//   - webhook, discord, slack — `config.url`. SubGlance issues an outbound
//     request to this URL from the server, which is functionally identical to
//     a monitor target: the same process, the same network position, the same
//     operator-supplied string. A monitor may not point at the cloud metadata
//     endpoint, so neither may a webhook.
//
//   - email — `config.host`, the SMTP relay. Same exposure by a different
//     protocol: a relay of 127.0.0.1:2375 makes SubGlance open a connection to
//     whatever listens there and speak at it. The port is not checked because
//     the guard judges addresses, not ports, and a blocked host is blocked on
//     every port.
//
//   - telegram — nothing. Telegram is reached at api.telegram.org, a constant
//     in the sender; the user supplies a bot token and a chat id, neither of
//     which names an address. There is nothing here for a guard to refuse, and
//     inventing a check for the constant would only suggest to a later reader
//     that the constant is configurable.
//
// An empty field means "this type has no user-supplied destination"; the
// caller skips the check rather than failing closed, because failing closed on
// a type with no target would make telegram channels unsavable.
func channelTarget(req channelRequest) (field, host string) {
	switch req.Type {
	case store.ChannelWebhook, store.ChannelDiscord, store.ChannelSlack:
		u, err := url.Parse(strings.TrimSpace(req.Config["url"]))
		if err != nil {
			// validateChannel has already rejected this; returning no
			// host here just avoids reporting the same fault twice.
			return "", ""
		}
		return "config.url", u.Hostname()

	case store.ChannelEmail:
		host := strings.TrimSpace(req.Config["host"])
		// `port` is its own config field, but the host field takes any
		// string and an operator may well type "relay.example:587" into
		// it. That string is neither an address literal nor a resolvable
		// name, so it would reach the guard intact, come back as a
		// lookup failure, and be waved through by the deliberate rule
		// that an unresolvable name is somebody else's valid setup —
		// turning the port into a way to smuggle a blocked relay past a
		// check that catches the same trick in a URL.
		//
		// SplitHostPort fails on a bare name and on a bracketless IPv6
		// literal, and failing is the signal that there was no port to
		// strip; keeping the original is then correct. It also unwraps
		// the bracketed IPv6 form, which is the only way a host field
		// can carry an IPv6 address and a port at once.
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		return "config.host", host

	default:
		return "", ""
	}
}

// checkChannelTarget refuses a channel whose destination the guard blocks,
// returning a validation message naming the offending field.
//
// # Why this exists on top of the delivery-time guard
//
// The dialer's Control hook (checker.Guard.ControlFunc, wired into the shared
// notifier transport and the SMTP dial) is the security boundary, and it stays
// the security boundary. It runs after DNS resolution and immediately before
// connect(2), so it judges the address the kernel is about to reach — which is
// what closes DNS rebinding and redirect hops.
//
// This function is a usability layer in front of it, nothing more. Without it
// a channel aimed at 169.254.169.254 saves cleanly and reports itself as
// configured; the operator finds out it never worked during the first outage,
// which is the worst possible moment to learn that the alerting is broken.
// Rejecting at save time moves that discovery to the moment the mistake is
// made, while the person who made it is still looking at the form.
//
// # The asymmetry is deliberate
//
// This check looks at the hostname as typed. The delivery check looks at the
// resolved address at dial time. A name that points somewhere public today and
// at 127.0.0.1 tomorrow therefore passes here and is still blocked there —
// that is the intended division of labour, not a gap to be closed. Anyone
// reading this later: do NOT conclude that the delivery-time guard is now
// redundant and remove it. Delete the Control hook and the only thing standing
// between a rebinding DNS record and the metadata service is a check that ran
// days earlier against a different answer.
//
// # Failures that are not refusals
//
// A lookup that errors or times out is NOT treated as a blocked target. A name
// that does not resolve right now is a perfectly ordinary thing to configure —
// an internal relay whose DNS is not reachable from the API process, a host
// that has not been provisioned yet — and refusing the save would make this
// convenience layer a new way for a flaky resolver to block configuration.
// Anything genuinely unreachable fails at delivery, visibly, in the outbox.
func (s *Server) checkChannelTarget(ctx context.Context, req channelRequest) string {
	if s.targetGuard == nil {
		return ""
	}

	field, host := channelTarget(req)
	if field == "" || host == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, channelTargetLookupTimeout)
	defer cancel()

	err := s.targetGuard.CheckHost(ctx, host)
	if err == nil {
		return ""
	}
	if !errors.Is(err, checker.ErrPrivateTarget) {
		// Resolution trouble, not a refusal. See above.
		return ""
	}

	// The field name leads, matching every other validation message in this
	// package: an error that says only "blocked address" leaves a form with
	// four inputs and no indication of which one to fix.
	return field + " is not an allowed destination: " + err.Error() +
		" (start SubGlance with --allow-private-targets to send to internal addresses)"
}
