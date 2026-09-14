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
	// An IPv4 address arriving over a dual-stack listener is written
	// ::ffff:10.0.0.1, which matches no IPv4 prefix until it is unmapped.
	addr = addr.Unmap()
	for _, p := range s.nets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
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
