package checker

import (
	"context"
	"errors"
	"net"
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
			_, err := r.LookupNetIP(ctx, "ip", tc.host)

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
