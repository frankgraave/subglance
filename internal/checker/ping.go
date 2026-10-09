package checker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// PingChecker sends an ICMP echo request and waits for the reply.
//
// # Two sockets, one fallback
//
// Classic ICMP needs a raw socket, which needs root or CAP_NET_RAW. A
// monitoring container should need neither. Linux offers unprivileged
// datagram-based ICMP sockets ("ping sockets"), gated by the
// net.ipv4.ping_group_range sysctl, and macOS offers the same socket type
// without a gate.
//
// So the checker tries the unprivileged socket first and falls back to the raw
// one. When neither works the failure says which knob to turn, because
// "operation not permitted" on its own has cost many people an evening.
type PingChecker struct {
	// guard vets the resolved address before any packet leaves the host.
	guard *Guard

	// mode is resolved once, on the first check, and reused. Probing the
	// kernel on every check would be a syscall pair per monitor per interval
	// for an answer that cannot change while the process runs.
	once sync.Once
	mode pingMode
	err  error

	// id distinguishes our echo requests from other processes' on a shared
	// raw socket.
	id int

	// every is how long a check waits for an answer before it sends the
	// next echo request; zero means pingProbeInterval. Tests shorten it.
	every time.Duration
}

// pingProbeInterval is the gap between echo requests within one check: the
// default of ping(8), and what Uptime Kuma's checks wait as well, since they
// run ping with a deadline.
const pingProbeInterval = time.Second

// pingConn is the part of *icmp.PacketConn a check uses, so the exchange can
// be tested against a socket that loses packets on purpose.
type pingConn interface {
	WriteTo(b []byte, dst net.Addr) (int, error)
	ReadFrom(b []byte) (int, net.Addr, error)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

type pingMode int

const (
	pingModeUnknown pingMode = iota
	pingModeUnprivileged
	pingModeRaw
)

// NewPingChecker returns a ping checker.
//
// The SSRF guard is applied to the resolved address before any packet is sent.
// A ping socket has no Control hook the way a Dialer does, so the check is
// explicit rather than automatic — losing it would let anyone sweep the
// internal network for live hosts by watching which monitors go green.
//
// A nil guard fails closed, matching the other constructors. Storing nil and
// skipping the check would turn a caller's oversight into an unguarded ICMP
// prober.
func NewPingChecker(guard *Guard) *PingChecker {
	if guard == nil {
		guard = NewGuard(false)
	}
	return &PingChecker{
		guard: guard,
		// The lower 16 bits are what fits in an ICMP echo ID.
		id: os.Getpid() & 0xffff,
	}
}

// Check sends echo requests, one per probe interval, until one is answered
// or the timeout runs out.
//
// Routers drop and rate-limit ICMP as a matter of course, so one lost packet
// is not an outage. A check that sent a single request failed on it, and with
// retries at 0 that was an alert; ping(8) with a deadline, which is how
// Uptime Kuma checks, sends again every second instead.
func (c *PingChecker) Check(ctx context.Context, m Monitor) Result {
	start := time.Now()

	host := hostFromURL(m.Target)
	if host == "" {
		return fail(start, FailInternal, "no host in target %q", m.Target)
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr, err := c.resolve(ctx, host)
	if err != nil {
		return classifyRequestError(start, ctx, err)
	}

	c.once.Do(c.detectMode)
	if c.err != nil {
		return fail(start, FailInternal, "%v", c.err)
	}

	conn, err := c.listen(addr.Is4())
	if err != nil {
		return fail(start, FailInternal, "open ICMP socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	return c.exchange(ctx, conn, addr, start)
}

// exchange sends a numbered echo request every probe interval and returns as
// soon as any of them is answered.
//
// A reply to an earlier request still counts: a slow answer is an answer. The
// latency is the round trip of the request that was answered, plus what the
// check spent before its first request, so a first-try answer reports what a
// single ping always did and a lost packet does not add a second to the
// latency chart.
//
// The read deadline is the next send or the check's deadline, whichever is
// sooner, so a cancelled check stops within one interval rather than at its
// timeout.
func (c *PingChecker) exchange(ctx context.Context, conn pingConn, addr netip.Addr, start time.Time) Result {
	every := c.every
	if every <= 0 {
		every = pingProbeInterval
	}
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		_ = conn.SetWriteDeadline(deadline)
	}

	// sentAt[i] is when the request with sequence number first+i left. The
	// first stays below 1<<15 and a check sends at most one request a second
	// for at most two minutes, so the numbers never wrap.
	first := int(rand.N[uint32](1 << 15))
	var sentAt []time.Time
	buf := make([]byte, 1500)

	for {
		if err := ctx.Err(); err != nil {
			return classifyRequestError(start, ctx, err)
		}
		at := time.Now()
		if err := c.send(conn, addr, first+len(sentAt)); err != nil {
			return classifyRequestError(start, ctx, err)
		}
		sentAt = append(sentAt, at)

		wait := at.Add(every)
		if hasDeadline && deadline.Before(wait) {
			wait = deadline
		}
		_ = conn.SetReadDeadline(wait)

		i, err := c.awaitReply(ctx, conn, addr, first, len(sentAt), buf)
		if err == nil {
			res := ok(start, 0)
			res.Latency = sentAt[0].Sub(start) + time.Since(sentAt[i])
			return res
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return classifyRequestError(start, ctx, err)
		}
		if hasDeadline && !time.Now().Before(deadline) {
			// The check's own deadline, not the end of an interval. Said
			// as such, so the message does not depend on whether the
			// context's timer or the socket's noticed first.
			return classifyRequestError(start, ctx, context.DeadlineExceeded)
		}
	}
}

// resolve turns a hostname into an address the guard has approved.
func (c *PingChecker) resolve(ctx context.Context, host string) (netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("no addresses for %s", host)
	}

	addr := addrs[0].Unmap()
	if err := c.guard.CheckAddr(addr); err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}

// detectMode picks the socket type once per process.
func (c *PingChecker) detectMode() {
	// Unprivileged first: it is what a container should be using.
	if conn, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		_ = conn.Close()
		c.mode = pingModeUnprivileged
		return
	}
	if conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		_ = conn.Close()
		c.mode = pingModeRaw
		return
	}

	// Both refused. Say what to do about it rather than surfacing EPERM.
	c.err = errPingUnavailable()
}

// errPingUnavailable is the message shown when neither socket type works.
//
// It names both fixes and the alternative, because the raw kernel error
// ("operation not permitted") gives the reader nothing to act on.
func errPingUnavailable() error {
	return errors.New(
		"ICMP is not permitted: grant CAP_NET_RAW to the container " +
			"(docker run --cap-add=NET_RAW), or allow unprivileged pings with " +
			"sysctl -w net.ipv4.ping_group_range=\"0 2147483647\". " +
			"A TCP check on a known port is a good alternative")
}

func (c *PingChecker) listen(ipv4Target bool) (*icmp.PacketConn, error) {
	switch {
	case c.mode == pingModeUnprivileged && ipv4Target:
		return icmp.ListenPacket("udp4", "0.0.0.0")
	case c.mode == pingModeUnprivileged:
		return icmp.ListenPacket("udp6", "::")
	case ipv4Target:
		return icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	default:
		return icmp.ListenPacket("ip6:ipv6-icmp", "::")
	}
}

func (c *PingChecker) send(conn pingConn, addr netip.Addr, seq int) error {
	msgType := icmp.Type(ipv4.ICMPTypeEcho)
	if !addr.Is4() {
		msgType = ipv6.ICMPTypeEchoRequest
	}

	msg := icmp.Message{
		Type: msgType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   c.id,
			Seq:  seq,
			Data: []byte("SubGlance"),
		},
	}

	payload, err := msg.Marshal(nil)
	if err != nil {
		return fmt.Errorf("marshal echo request: %w", err)
	}

	// An unprivileged socket is a UDP socket to the kernel, so the
	// destination must be a UDPAddr even though no UDP is involved.
	var dst net.Addr = &net.IPAddr{IP: addr.AsSlice()}
	if c.mode == pingModeUnprivileged {
		dst = &net.UDPAddr{IP: addr.AsSlice()}
	}

	if _, err := conn.WriteTo(payload, dst); err != nil {
		return err
	}
	return nil
}

// awaitReply reads until a reply to one of the sent requests arrives or the
// read deadline passes, and returns which request was answered.
//
// The requests sent so far carry the sequence numbers first to
// first+sent-1. Replies from other pings on the host can land on this
// socket, so anything that is not ours is skipped rather than treated as
// success — otherwise a busy host would make every ping monitor pass
// regardless of the target.
func (c *PingChecker) awaitReply(ctx context.Context, conn pingConn, addr netip.Addr, first, sent int, buf []byte) (int, error) {
	proto := 1 // ICMPv4
	if !addr.Is4() {
		proto = 58 // ICMPv6
	}

	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, err
		}

		msg, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue // not something we can read; keep waiting
		}

		switch body := msg.Body.(type) {
		case *icmp.Echo:
			// On an unprivileged socket the kernel rewrites the ID, so it
			// cannot be matched. The sequence number survives, and the peer
			// address confirms the rest. On a raw socket the ID is ours and
			// worth checking: every ICMP message on the host arrives here,
			// so concurrent monitors would otherwise read each other's
			// replies.
			if c.mode == pingModeRaw && body.ID != c.id {
				continue
			}
			if body.Seq < first || body.Seq >= first+sent {
				continue
			}
			if !peerMatches(peer, addr) {
				continue
			}
			return body.Seq - first, nil

		case *icmp.DstUnreach:
			// An ICMP error carries the header of the datagram that caused
			// it. Without checking that, an unrelated flow's "destination
			// unreachable" — which a raw socket also receives — would fail a
			// healthy monitor. That is a false-alarm generator, so anything
			// we cannot positively attribute to our own probe is ignored.
			if !c.quotesOurProbe(body.Data, addr, first, sent) {
				continue
			}
			return 0, fmt.Errorf("destination unreachable")

		case *icmp.TimeExceeded:
			if !c.quotesOurProbe(body.Data, addr, first, sent) {
				continue
			}
			return 0, fmt.Errorf("TTL exceeded in transit")
		}
	}
}

// quotesOurProbe reports whether an ICMP error was caused by any of the
// requests this check has sent. Such an error is the network's answer about
// the target, not a lost packet, so it fails the check at once.
func (c *PingChecker) quotesOurProbe(quoted []byte, addr netip.Addr, first, sent int) bool {
	for seq := first; seq < first+sent; seq++ {
		if errorRefersToOurProbe(quoted, addr, c.id, seq, c.mode) {
			return true
		}
	}
	return false
}

// errorRefersToOurProbe reports whether an ICMP error message was caused by
// the echo request with this sequence number.
//
// ICMP errors quote the offending datagram: the IP header plus at least the
// first eight bytes of its payload, which for an echo request covers type,
// code, checksum, identifier and sequence number. Matching on that is what
// separates "the host we are checking is unreachable" from "some other
// connection on this machine got an error" — and on a raw socket, where every
// ICMP message on the host is delivered, the difference is a false alarm.
//
// Anything that cannot be parsed is treated as not ours. Ignoring a real error
// costs one check that times out instead; acting on someone else's costs a
// page in the middle of the night.
func errorRefersToOurProbe(quoted []byte, want netip.Addr, id, seq int, mode pingMode) bool {
	if want.Is4() {
		// IPv4 header is at least 20 bytes; the payload follows.
		if len(quoted) < 20 {
			return false
		}
		ihl := int(quoted[0]&0x0f) * 4
		if ihl < 20 || len(quoted) < ihl+8 {
			return false
		}

		// Destination address of the original datagram, bytes 16-19.
		dst, ok := netip.AddrFromSlice(quoted[16:20])
		if !ok || dst.Unmap() != want.Unmap() {
			return false
		}
		return echoHeaderMatches(quoted[ihl:], id, seq, mode)
	}

	// IPv6 has a fixed 40-byte header; the destination sits at bytes 24-39.
	if len(quoted) < 40+8 {
		return false
	}
	dst, ok := netip.AddrFromSlice(quoted[24:40])
	if !ok || dst.Unmap() != want.Unmap() {
		return false
	}
	return echoHeaderMatches(quoted[40:], id, seq, mode)
}

// echoHeaderMatches checks the quoted ICMP echo header's identifier and
// sequence number.
func echoHeaderMatches(hdr []byte, id, seq int, mode pingMode) bool {
	if len(hdr) < 8 {
		return false
	}

	gotID := int(hdr[4])<<8 | int(hdr[5])
	gotSeq := int(hdr[6])<<8 | int(hdr[7])

	if gotSeq != seq {
		return false
	}
	// The kernel rewrites the identifier on an unprivileged socket, so it is
	// only meaningful on the raw path.
	if mode == pingModeRaw && gotID != id {
		return false
	}
	return true
}

func peerMatches(peer net.Addr, want netip.Addr) bool {
	var got net.IP
	switch p := peer.(type) {
	case *net.IPAddr:
		got = p.IP
	case *net.UDPAddr:
		got = p.IP
	default:
		return false
	}

	gotAddr, ok := netip.AddrFromSlice(got)
	if !ok {
		return false
	}
	return gotAddr.Unmap() == want.Unmap()
}
