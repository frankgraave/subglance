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
func NewPingChecker(guard *Guard) *PingChecker {
	return &PingChecker{
		guard: guard,
		// The lower 16 bits are what fits in an ICMP echo ID.
		id: os.Getpid() & 0xffff,
	}
}

// Check sends one echo request and waits for a matching reply.
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

	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		_ = conn.SetDeadline(deadline)
	}

	seq := int(rand.N[uint32](1 << 15))
	if err := c.send(conn, addr, seq); err != nil {
		return classifyRequestError(start, ctx, err)
	}

	if err := c.awaitReply(ctx, conn, addr, seq); err != nil {
		return classifyRequestError(start, ctx, err)
	}

	return ok(start, 0)
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
	if c.guard != nil {
		if err := c.guard.CheckAddr(addr); err != nil {
			return netip.Addr{}, err
		}
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

func (c *PingChecker) send(conn *icmp.PacketConn, addr netip.Addr, seq int) error {
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

// awaitReply reads until our echo reply arrives or the deadline passes.
//
// Replies from other pings on the host can land on this socket, so anything
// that is not ours is skipped rather than treated as success — otherwise a
// busy host would make every ping monitor pass regardless of the target.
func (c *PingChecker) awaitReply(ctx context.Context, conn *icmp.PacketConn, addr netip.Addr, seq int) error {
	proto := 1 // ICMPv4
	if !addr.Is4() {
		proto = 58 // ICMPv6
	}

	buf := make([]byte, 1500)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}

		msg, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue // not something we can read; keep waiting
		}

		switch body := msg.Body.(type) {
		case *icmp.Echo:
			// On an unprivileged socket the kernel rewrites the ID, so it
			// cannot be matched. The sequence number survives, and the peer
			// address confirms the rest.
			if body.Seq != seq {
				continue
			}
			if !peerMatches(peer, addr) {
				continue
			}
			return nil

		case *icmp.DstUnreach:
			return fmt.Errorf("destination unreachable")

		case *icmp.TimeExceeded:
			return fmt.Errorf("TTL exceeded in transit")
		}
	}
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
