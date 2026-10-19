// Package trustedproxy decides whose forwarding headers may be believed.
//
// X-Forwarded-For and X-Real-Ip are set by whoever sends them. Honouring one
// unconditionally lets a caller name its own address, and an address the
// caller chooses is not an identity — it is a rate-limit bucket the caller can
// swap out per request. This package exists so that the question "may this
// peer speak for someone else" has one answer, configured once.
//
// It lives apart from internal/api because the configuration layer has to
// validate the setting at startup, and importing the whole HTTP surface to do
// that would put the layers the wrong way round.
package trustedproxy

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Set is the group of peers whose forwarding headers are believed.
//
// The zero value believes nobody, which is the right default for a product
// that starts with no configuration at all: an instance reached directly from
// the internet must not take a client's word for where it came from. An
// operator who does run SubGlance behind nginx, Caddy or Traefik names that
// proxy once and gets real client addresses back.
type Set struct {
	nets []netip.Prefix
}

// Parse accepts a comma-separated list of addresses and CIDR blocks. A bare
// address is taken as a single host.
func Parse(spec string) (Set, error) {
	var s Set
	for _, field := range strings.Split(spec, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.Contains(field, "/") {
			p, err := netip.ParsePrefix(field)
			if err != nil {
				return Set{}, fmt.Errorf("trusted proxy %q: %w", field, err)
			}
			// Masking rejects nothing but keeps Contains honest: 10.0.0.5/8
			// is a prefix whose own address is not in it otherwise.
			s.nets = append(s.nets, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return Set{}, fmt.Errorf("trusted proxy %q: %w", field, err)
		}
		s.nets = append(s.nets, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return s, nil
}

// Validate reports whether a configuration string is usable, so a typo is a
// startup error rather than a limiter that silently trusts nobody.
func Validate(spec string) error {
	_, err := Parse(spec)
	return err
}

// Contains reports whether an address may speak for other addresses.
func (s Set) Contains(ip string) bool {
	if len(s.nets) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		// A host that is not an address cannot be matched against a CIDR, and
		// guessing would mean trusting on a parse failure.
		return false
	}
	return s.contains(addr)
}

// String renders the configured set, for startup logging.
func (s Set) String() string {
	if len(s.nets) == 0 {
		return "none"
	}
	out := make([]string, 0, len(s.nets))
	for _, p := range s.nets {
		out = append(out, p.String())
	}
	return strings.Join(out, ",")
}

// ClientAddr resolves the address a request came from.
//
// peer is the address that opened the connection. forwardedFor holds every
// X-Forwarded-For line of the request, in the order they arrived, and realIP
// the X-Real-Ip header; both are read only when peer is in the set.
//
// The chain is read from the right. Each proxy appends the address it was
// reached from, so the rightmost entries are written by proxies in the set and
// everything to the left of the first address outside it was written by the
// client — which can put any text there. Taking the leftmost entry instead,
// which is what a replacing proxy leaves behind, lets a client behind an
// appending one (nginx's $proxy_add_x_forwarded_for, HAProxy's forwardfor,
// most load balancers) pick its own rate-limit bucket per request. Walking
// from the right gives the same answer behind a replacing proxy and the right
// one behind an appending proxy.
//
// Every hop must be an address; a port after it is dropped. Text that is not
// an address never becomes the client: the walk stops there and keeps the
// last address it read, or the peer when there was none. When every hop is in
// the set, the request started inside it and the leftmost hop is that host.
//
// X-Real-Ip stands in for a chain when there is none, and only when it is an
// address. When a proxy sends both, they must agree: X-Real-Ip has to name a
// hop the walk read, the client or a proxy in the set to its right (an inner
// nginx writing the $remote_addr of an outer load balancer). Otherwise one of
// the two headers came from the visitor and nothing says which: a proxy that
// sets only X-Real-Ip passes a visitor's X-Forwarded-For through untouched,
// and Caddy, which sets only X-Forwarded-For, passes a visitor's X-Real-Ip.
// Either header winning outright lets the visitor pick their own rate-limit
// bucket behind one of those two, so neither does: the peer is used, which is
// the proxy's shared bucket, and a visitor who forges nothing is not affected.
//
// The result is canonical — an IPv4 address that arrived
// IPv6-mapped is written as IPv4, and a zone is dropped — so one host has one
// bucket however it connected.
func (s Set) ClientAddr(peer string, forwardedFor []string, realIP string) string {
	peerAddr, ok := parseHop(peer)
	if !ok {
		return peer
	}
	if !s.contains(peerAddr) {
		return canonical(peerAddr)
	}

	real, haveReal := parseHop(strings.TrimSpace(realIP))
	if client, read, present := s.fromChain(forwardedFor); present {
		if !client.IsValid() {
			return canonical(peerAddr)
		}
		if haveReal && !slices.ContainsFunc(read, func(hop netip.Addr) bool { return sameHost(hop, real) }) {
			return canonical(peerAddr)
		}
		return canonical(client)
	}

	if haveReal {
		return canonical(real)
	}
	return canonical(peerAddr)
}

// fromChain walks a forwarding chain from the right and returns the first hop
// outside the set, or the leftmost hop when all of them are inside it. read
// holds every address the walk parsed, the client last. present reports
// whether the chain named any hop at all.
//
// A hop that is not an address ends the walk. Every hop to its right was
// written by a proxy in the set, so the last address read is the nearest
// client known for certain; when the very first hop is not an address, no
// client is returned and the caller falls back to the peer.
func (s Set) fromChain(lines []string) (client netip.Addr, read []netip.Addr, present bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		rest := lines[i]
		for rest != "" {
			var entry string
			if j := strings.LastIndexByte(rest, ','); j >= 0 {
				entry, rest = rest[j+1:], rest[:j]
			} else {
				entry, rest = rest, ""
			}
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			present = true
			addr, ok := parseHop(entry)
			if !ok {
				return client, read, true
			}
			client = addr
			read = append(read, addr)
			if !s.contains(addr) {
				return client, read, true
			}
		}
	}
	return client, read, present
}

// contains reports whether an already parsed address is in the set.
//
// An IPv4 address arriving over a dual-stack listener is written
// ::ffff:10.0.0.1, which matches no IPv4 prefix until it is unmapped.
func (s Set) contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range s.nets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHop reads one hop of a forwarding chain: an address, or an address
// with a port ("192.0.2.1:443", "[2001:db8::1]:443"), which some proxies
// write.
func parseHop(hop string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(hop); err == nil {
		return addr, true
	}
	if ap, err := netip.ParseAddrPort(hop); err == nil {
		return ap.Addr(), true
	}
	return netip.Addr{}, false
}

// sameHost reports whether two addresses name one host however each was
// written: IPv4-mapped or not, with a zone or without.
func sameHost(a, b netip.Addr) bool {
	return a.Unmap().WithZone("") == b.Unmap().WithZone("")
}

// canonical writes an address in one form per host.
func canonical(addr netip.Addr) string {
	return addr.Unmap().WithZone("").String()
}
