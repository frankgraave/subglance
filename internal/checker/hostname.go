package checker

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"
)

// ResolvableHost reports why the checker's resolver would refuse host without
// asking any server, or nil when it would at least ask.
//
// The binary is built without cgo, so every tcp, ssl and ping check, and
// every http check's dial, looks names up through Go's own resolver. That
// resolver refuses a name that is not a valid domain name before it sends a
// query, and reports the refusal as "no such host" — the same words it uses
// for a name that really does not exist. A monitor saved with such a name
// therefore fails every check forever, and the failure reads as an outage of
// a real host rather than as a typo in the form. The rule is fixed and known
// at save time, so the API applies it there and names what is wrong.
//
// The rules mirror the standard library's (net.isDomainName and the .onion
// refusal beside it); TestResolvableHostAgreesWithTheResolver asks a real
// resolver about every case, so the two cannot drift apart unnoticed. A name
// this function accepts can still fail to resolve, because it may not exist;
// that is a check result, not a form error.
//
// An IP address, including an IPv6 address with a zone, is always accepted.
// A non-ASCII name is refused with a pointer to its xn-- form: the tcp, ssl
// and ping checkers hand the name to the resolver as written, and the
// resolver does not convert it. net/http does, so an http monitor's hostname
// goes through ResolvableURLHost instead.
func ResolvableHost(host string) error {
	if host == "" {
		return fmt.Errorf("target has no host")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if !isASCII(host) {
		return fmt.Errorf("hostname %q contains characters outside ASCII; "+
			"write an internationalised name in its xn-- form, as the resolver needs it", host)
	}
	// The root name "." is valid to the resolver but has no address, and no
	// one types it to mean anything; it is refused as an empty label below.

	// 253 characters, or 254 with the trailing dot of a fully qualified name.
	if n := len(host); n > 254 || n == 254 && host[n-1] != '.' {
		return fmt.Errorf("hostname is %d characters long; the limit is 253", len(strings.TrimSuffix(host, ".")))
	}

	name := strings.TrimSuffix(host, ".")
	nonNumeric := false
	for _, label := range strings.Split(name, ".") {
		switch {
		case label == "":
			return fmt.Errorf("hostname %q has an empty label: two dots in a row, or a dot at the start", host)
		case len(label) > 63:
			return fmt.Errorf("hostname %q has a label of %d characters; the limit is 63", host, len(label))
		case label[0] == '-':
			return fmt.Errorf("hostname %q has a label that starts with a hyphen", host)
		case label[len(label)-1] == '-':
			return fmt.Errorf("hostname %q has a label that ends with a hyphen", host)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', c == '_', c == '-':
				nonNumeric = true
			case '0' <= c && c <= '9':
			default:
				return fmt.Errorf("hostname %q contains %q; a hostname takes letters, digits, hyphens, "+
					"underscores and dots", host, string(c))
			}
		}
	}
	// A name of digits and dots alone is read as an address, and this one
	// did not parse as one: 999.1.1.1, or 10.0.1 with an octet missing.
	if !nonNumeric {
		return fmt.Errorf("%q is not a valid IP address, and a hostname needs at least one letter", host)
	}
	// RFC 7686: a .onion name is reachable only through Tor, and the
	// resolver refuses to send it to DNS as written. On a machine with a
	// search domain it still asks for x.onion.<search domain>, which is not
	// the onion service either.
	if strings.HasSuffix(strings.ToLower(name), ".onion") {
		return fmt.Errorf("%q is a Tor onion address, which DNS does not resolve", host)
	}
	return nil
}

// ResolvableURLHost is ResolvableHost for the hostname of an http monitor's
// URL. net/http converts an internationalised hostname to its xn-- form before
// it dials, so a non-ASCII hostname is left to it; an ASCII one reaches the
// resolver as written and is held to the same rule as the other types.
func ResolvableURLHost(host string) error {
	if !isASCII(host) {
		return nil
	}
	return ResolvableHost(host)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
