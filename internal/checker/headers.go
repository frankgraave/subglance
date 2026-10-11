package checker

import (
	"fmt"
	"slices"
	"strings"
)

// A monitor's request headers are held to the rules net/http applies when it
// writes them. A header it refuses is not dropped: the whole request fails in
// the transport before a connection is made, and that error reads like a
// network fault. A monitor saved with one reports an outage of a server it
// never contacted, on every check. So the API refuses such headers when they
// are saved, by these functions.
//
// The two rules are the ones in golang.org/x/net/http/httpguts, which
// net/http carries a copy of. They are restated here rather than imported:
// that package pulls in golang.org/x/text for its IDNA code, a module this
// build does not otherwise need. TestRequestHeaderProblemMatchesTheTransport
// holds them to what the transport actually does.

// ValidHeaderName reports whether net/http will send a header by this name:
// an HTTP token, which has no spaces, colons or control characters.
func ValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

// isTokenChar reports whether b may appear in an HTTP token (RFC 9110
// section 5.6.2): a letter, a digit or one of !#$%&'*+-.^_`|~.
func isTokenChar(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

// validHeaderValue reports whether net/http will send a header value: no
// control character other than a horizontal tab. Bytes above 0x7f pass, as
// they do in net/http.
func validHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if b := v[i]; (b < ' ' && b != '\t') || b == 0x7f {
			return false
		}
	}
	return true
}

// RequestHeaderProblem returns why these headers cannot be sent as they are,
// or "" when they can. Names are judged in sorted order, so the same map
// always gets the same answer.
//
// Two names that differ only in case are refused as well. They are one header
// on the wire, and which of the two values went out would depend on map order.
func RequestHeaderProblem(headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	slices.Sort(names)
	seen := make(map[string]string, len(names))
	for _, k := range names {
		if !ValidHeaderName(k) {
			return fmt.Sprintf("header name %q is not valid: a name cannot be empty or contain spaces, colons or other separators", k)
		}
		if !validHeaderValue(headers[k]) {
			return fmt.Sprintf("header %q has a line break or another control character in its value", k)
		}
		folded := strings.ToLower(k)
		if first, ok := seen[folded]; ok {
			return fmt.Sprintf("headers %q and %q are one header, because header names ignore case; keep one of them", first, k)
		}
		seen[folded] = k
	}
	return ""
}
