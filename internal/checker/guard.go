package checker

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// ErrPrivateTarget is returned when a check target resolves to an address that
// the SSRF guard refuses.
var ErrPrivateTarget = errors.New("target resolves to a private or reserved address")

// Guard decides which network addresses a check may connect to.
//
// Users supply the URLs that SubGlance fetches. Without this guard, anyone with
// permission to add a monitor could point it at 169.254.169.254 and read the
// host's cloud credentials, or sweep the internal network by watching response
// times. The guard is therefore on by default and disabling it is an explicit
// operator decision (--allow-private-targets).
//
// The check runs at dial time on the resolved IP, not on the hostname. That is
// deliberate: validating the hostname before connecting leaves a DNS-rebinding
// window where the name resolves to something harmless during validation and to
// 127.0.0.1 a moment later when the connection is actually made. Redirects are
// covered for free, because each hop dials again.
type Guard struct {
	// AllowPrivate disables the guard entirely.
	AllowPrivate bool
}

// NewGuard returns a Guard. Pass true only when the operator has explicitly
// opted in to monitoring internal addresses.
func NewGuard(allowPrivate bool) *Guard {
	return &Guard{AllowPrivate: allowPrivate}
}

// CheckAddr reports whether a resolved address may be dialled.
func (g *Guard) CheckAddr(addr netip.Addr) error {
	if g.AllowPrivate {
		return nil
	}
	if !addr.IsValid() {
		return fmt.Errorf("%w: invalid address", ErrPrivateTarget)
	}

	// An IPv4 address wrapped in IPv6 form (::ffff:127.0.0.1) must be judged
	// on its IPv4 value, or every guard below could be bypassed by writing the
	// address in its mapped form.
	if addr.Is4In6() {
		addr = addr.Unmap()
	}

	switch {
	case addr.IsLoopback():
		return fmt.Errorf("%w: loopback address %s", ErrPrivateTarget, addr)
	case addr.IsPrivate():
		return fmt.Errorf("%w: private address %s", ErrPrivateTarget, addr)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		// 169.254.0.0/16 is where cloud metadata services live.
		return fmt.Errorf("%w: link-local address %s", ErrPrivateTarget, addr)
	case addr.IsUnspecified():
		return fmt.Errorf("%w: unspecified address %s", ErrPrivateTarget, addr)
	case addr.IsMulticast():
		return fmt.Errorf("%w: multicast address %s", ErrPrivateTarget, addr)
	case addr.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: interface-local address %s", ErrPrivateTarget, addr)
	}

	// Ranges that netip has no predicate for but that must not be reachable.
	for _, p := range reservedPrefixes {
		if p.Contains(addr) {
			return fmt.Errorf("%w: reserved address %s", ErrPrivateTarget, addr)
		}
	}
	return nil
}

// reservedPrefixes covers reserved ranges beyond the netip predicates.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),      // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),       // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),     // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),    // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),     // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
	netip.MustParsePrefix("::/128"),             // unspecified
	netip.MustParsePrefix("64:ff9b::/96"),       // NAT64
	netip.MustParsePrefix("100::/64"),           // discard-only
	netip.MustParsePrefix("2001:db8::/32"),      // documentation
	netip.MustParsePrefix("fc00::/7"),           // unique local
}

// ControlFunc returns a net.Dialer Control hook that enforces the guard.
//
// Control runs after DNS resolution and after the socket is created, but before
// connect(2). Checking here means we validate the address the kernel is about
// to reach, which closes the DNS-rebinding gap that hostname-based validation
// leaves open.
func (g *Guard) ControlFunc() func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("guard: parse address %q: %w", address, err)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("guard: parse ip %q: %w", host, err)
		}
		return g.CheckAddr(addr)
	}
}

// CheckHost resolves a hostname and verifies every address it maps to.
//
// This is for pre-flight validation when a user saves a monitor, so that an
// obviously bad target is rejected immediately with a clear message rather than
// failing silently on every check. It is not a substitute for ControlFunc:
// between this call and the actual connection, DNS can change.
func (g *Guard) CheckHost(host string) error {
	if g.AllowPrivate {
		return nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return g.CheckAddr(addr)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return fmt.Errorf("guard: unusable address for %s", host)
		}
		if err := g.CheckAddr(addr); err != nil {
			return err
		}
	}
	return nil
}
