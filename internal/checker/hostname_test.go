package checker

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hostCases are the shapes a target's hostname takes in practice, and the
// ones a slip of the keyboard produces. want is the substring a refusal must
// contain; empty means the host is accepted.
var hostCases = []struct {
	host string
	want string
}{
	// Kept: everything a monitor is actually pointed at.
	{"example.com", ""},
	{"EXAMPLE.com", ""},
	{"example.com.", ""},
	{"db", ""},
	{"localhost", ""},
	{"my_service.internal", ""},
	{"_dmarc.example.com", ""},
	{"xn--bcher-kva.de", ""},
	{"a-b.example.com", ""},
	{"1password.com", ""},
	{"123.example", ""},
	{"192.0.2.10", ""},
	{"2001:db8::1", ""},
	{"fe80::1%eth0", ""},
	{"::ffff:192.0.2.1", ""},
	{strings.Repeat("a", 63) + ".example", ""},
	{strings.Repeat("a.", 126) + "b", ""}, // 253 characters
	{strings.Repeat("a.", 127), ""},       // 254 with the trailing dot

	// Refused: the resolver would answer "no such host" without asking.
	{"", "no host"},
	{"exa..mple.com", "empty label"},
	{".example.com", "empty label"},
	{"example.com..", "empty label"},
	{"exa!mple.com", `contains "!"`},
	{"exa*mple.com", `contains "*"`},
	{"exa mple.com", `contains " "`},
	{"exa,mple.com", `contains ","`},
	{"-example.com", "starts with a hyphen"},
	{"www.-example.com", "starts with a hyphen"},
	{"example-.com", "ends with a hyphen"},
	{"example.com-", "ends with a hyphen"},
	{strings.Repeat("a", 64) + ".example", "label of 64 characters"},
	{strings.Repeat("a.", 126) + "bc", "the limit is 253"},
	{"b\u00fccher.de", "xn--"},
	{"ex\u0430mple.com", "xn--"}, // a Cyrillic a, which looks like the Latin one
	{"999.1.1.1", "not a valid IP address"},
	{"10.0.1", "not a valid IP address"},
	{"12345", "not a valid IP address"},
	{"duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion", "onion"},
}

func TestResolvableHost(t *testing.T) {
	for _, tc := range hostCases {
		t.Run(tc.host, func(t *testing.T) {
			err := ResolvableHost(tc.host)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("ResolvableHost(%q) = %v, want accepted", tc.host, err)
			case tc.want != "" && err == nil:
				t.Errorf("ResolvableHost(%q) accepted, want a refusal mentioning %q", tc.host, tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("ResolvableHost(%q) = %q, want it to mention %q", tc.host, err, tc.want)
			}
		})
	}
}

// urlCases are URLs that parse, and whether a request to them would leave
// the process. want is the substring a refusal must contain; empty means the
// URL is accepted.
var urlCases = []struct {
	raw  string
	want string
}{
	{"https://example.com/health", ""},
	{"http://example.com:8080/", ""},
	{"http://example.com:1/", ""},
	{"http://example.com:65535/", ""},
	{"http://example.com:/", ""}, // an empty port is the scheme's default
	{"http://192.168.1.9:8080/api", ""},
	{"http://[2001:db8::1]:8080/", ""},
	{"http://db/", ""},
	{"https://b\u00fccher.de/", ""},
	{"https://user:pass@example.com/", ""},

	{"http://example.com:0/", "port 0 is out of range"},
	{"http://example.com:65536/", "port 65536 is out of range"},
	{"http://example.com:99999/", "port 99999 is out of range"},
	{"http://[::1]:70000/", "port 70000 is out of range"},
	{"http://:8080/", "no host"},
	{"https://exa!mple.com/x", `contains "!"`},
	{"https://-bad.example.com/x", "starts with a hyphen"},
	{"http://10.0.1/", "not a valid IP address"},
}

func TestResolvableURL(t *testing.T) {
	for _, tc := range urlCases {
		t.Run(tc.raw, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.raw, err)
			}
			err = ResolvableURL(u)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("ResolvableURL(%q) = %v, want accepted", tc.raw, err)
			case tc.want != "" && err == nil:
				t.Errorf("ResolvableURL(%q) accepted, want a refusal mentioning %q", tc.raw, tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("ResolvableURL(%q) = %q, want it to mention %q", tc.raw, err, tc.want)
			}
		})
	}
}

// Every URL ResolvableURL refuses is one the client's dial refuses before a
// byte leaves the process, or, for port 0, one no server can answer. This
// asks the two parts of the dial that decide it, the port parser and a Go
// resolver whose every connection fails, so nothing leaves the machine: if a
// Go release changes either rule, this fails on the URL where they part ways.
func TestResolvableURLAgreesWithTheDial(t *testing.T) {
	for _, tc := range urlCases {
		u, _ := url.Parse(tc.raw)
		if !isASCII(u.Hostname()) || u.Hostname() == "" || u.Port() == "0" {
			// net/http converts the first before it dials, the second never
			// reaches a dial, and the dial accepts port 0 for the far end
			// to refuse.
			continue
		}
		t.Run(tc.raw, func(t *testing.T) {
			var dialled atomic.Bool
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(context.Context, string, string) (net.Conn, error) {
					dialled.Store(true)
					return nil, errors.New("no network in this test")
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			port := u.Port()
			if port == "" {
				port = "80"
			}
			_, lookupErr := r.LookupNetIP(ctx, "ip", rooted(u.Hostname()))
			_, portErr := net.LookupPort("tcp", port)
			usable := (dialled.Load() || lookupErr == nil) && portErr == nil
			ours := ResolvableURL(u) == nil
			if usable != ours {
				t.Errorf("client usable = %v (dialled %v, lookup %v, port %v), ResolvableURL accepted = %v",
					usable, dialled.Load(), lookupErr, portErr, ours)
			}
		})
	}
}

func TestResolvableURLHostLeavesInternationalisedNamesToNetHTTP(t *testing.T) {
	if err := ResolvableURLHost("b\u00fccher.de"); err != nil {
		t.Errorf("internationalised URL host refused: %v", err)
	}
	if err := ResolvableURLHost("exa..mple.com"); err == nil {
		t.Error("an ASCII URL host with an empty label was accepted")
	}
}

// The rule is a copy of the resolver's, so this asks the resolver itself. A
// Go resolver whose every connection fails can still say which names it
// would have sent a query for: it dials for those, and refuses the rest
// without dialling. Nothing leaves the machine — the dial never connects.
//
// If a Go release loosens or tightens its name rule, this fails on the name
// where ResolvableHost and the resolver part ways.
func TestResolvableHostAgreesWithTheResolver(t *testing.T) {
	for _, tc := range hostCases {
		if tc.host == "" {
			continue // an empty host never reaches the resolver; ParseHostPort refuses it
		}
		t.Run(tc.host, func(t *testing.T) {
			var dialled atomic.Bool
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(context.Context, string, string) (net.Conn, error) {
					dialled.Store(true)
					return nil, errors.New("no network in this test")
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := r.LookupNetIP(ctx, "ip", rooted(tc.host))

			// Asked a server, or answered at once (an address, or a name in
			// the hosts file): either way the name was usable.
			usable := dialled.Load() || err == nil
			ours := ResolvableHost(tc.host) == nil
			if usable != ours {
				t.Errorf("resolver usable = %v (dialled %v, err %v), ResolvableHost accepted = %v",
					usable, dialled.Load(), err, ours)
			}
		})
	}
}

// rooted returns host as a fully qualified name, so the resolver's answer in
// TestResolvableHostAgreesWithTheResolver does not depend on the machine.
// An unrooted name is also tried under each search domain in resolv.conf,
// and the resolver applies its .onion refusal to each name it would send:
// on a machine with a search domain it dials for x.onion.corp.example, and
// the onion case would read as usable. A rooted name skips the search list.
// An address is left alone, because a trailing dot would make it a name.
func rooted(host string) string {
	if _, err := netip.ParseAddr(host); err == nil || strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}
