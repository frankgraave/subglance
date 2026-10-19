package trustedproxy

import "testing"

// TestClientAddr covers how a request's address is resolved from the peer and
// its forwarding headers. The appending-proxy cases are the reason the chain
// is read from the right: there the leftmost entry is whatever the client
// sent, and a client that may choose its address chooses its rate-limit
// bucket.
func TestClientAddr(t *testing.T) {
	tests := []struct {
		name string
		spec string
		peer string
		xff  []string
		real string
		want string
	}{
		{"no proxies configured, header ignored",
			"", "192.0.2.1", []string{"203.0.113.5"}, "", "192.0.2.1"},
		{"untrusted peer, X-Real-Ip ignored",
			"10.0.0.0/8", "192.0.2.1", nil, "198.51.100.7", "192.0.2.1"},

		{"replacing proxy: one entry, the client",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.5"}, "", "203.0.113.5"},
		{"appending proxy: a forged first entry is not the client",
			"127.0.0.1", "127.0.0.1", []string{"198.51.100.1, 203.0.113.9"}, "", "203.0.113.9"},
		{"appending proxy: many forged entries",
			"127.0.0.1", "127.0.0.1", []string{"1.1.1.1, 2.2.2.2,3.3.3.3 , 203.0.113.9"}, "", "203.0.113.9"},
		{"two proxies in the chain, both named",
			"127.0.0.1, 10.0.0.0/8", "127.0.0.1", []string{"198.51.100.1, 203.0.113.9, 10.0.0.4"}, "", "203.0.113.9"},
		{"a proxy in the chain that is not named is the client",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9, 10.0.0.4"}, "", "10.0.0.4"},
		{"every hop trusted: the leftmost is the host that started it",
			"10.0.0.0/8", "10.0.0.1", []string{"10.0.0.7, 10.0.0.3"}, "", "10.0.0.7"},

		// HAProxy's forwardfor and some load balancers add a header line of
		// their own rather than extend the client's; the last line is the
		// nearest proxy's.
		{"a second header line is read after the first",
			"127.0.0.1", "127.0.0.1", []string{"198.51.100.1", "203.0.113.9"}, "", "203.0.113.9"},
		{"empty entries are skipped",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9, ,", ""}, "", "203.0.113.9"},

		{"a port after a hop is dropped",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9:51234"}, "", "203.0.113.9"},
		{"an IPv6 hop with a port",
			"127.0.0.1", "127.0.0.1", []string{"[2001:db8::7]:443"}, "", "2001:db8::7"},
		{"an IPv4-mapped hop is written as IPv4",
			"127.0.0.1", "127.0.0.1", []string{"::ffff:203.0.113.9"}, "", "203.0.113.9"},
		{"an IPv4-mapped peer is written as IPv4",
			"", "::ffff:192.0.2.1", nil, "", "192.0.2.1"},

		// Text that is not an address never becomes the client.
		{"a non-address as the nearest hop falls back to the peer",
			"127.0.0.1", "127.0.0.1", []string{"garbage\nline"}, "", "127.0.0.1"},
		{"a non-address beyond the client is never reached",
			"127.0.0.1", "127.0.0.1", []string{"unknown, 203.0.113.9"}, "", "203.0.113.9"},
		{"nothing left of a non-address is believed",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9, junk, 127.0.0.1"}, "", "127.0.0.1"},
		{"a non-address behind a trusted hop stops at that hop",
			"127.0.0.1, 10.0.0.0/8", "127.0.0.1", []string{"not-an-ip, 10.0.0.4"}, "", "10.0.0.4"},

		{"X-Real-Ip from a trusted proxy with no chain",
			"127.0.0.1", "127.0.0.1", nil, "198.51.100.7", "198.51.100.7"},

		// Both headers from a trusted proxy. A proxy that sets only one of
		// them passes the visitor's copy of the other through, so they are
		// believed only when they agree; otherwise the proxy's own address
		// is the bucket, which the visitor cannot choose.
		{"both headers naming the same client",
			"127.0.0.1", "127.0.0.1", []string{"198.51.100.1, 203.0.113.9"}, "203.0.113.9", "203.0.113.9"},
		{"both headers, X-Real-Ip IPv4-mapped",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9"}, "::ffff:203.0.113.9", "203.0.113.9"},
		{"X-Real-Ip naming a trusted hop of the chain: the chain carries further",
			"127.0.0.1, 10.0.0.0/8", "127.0.0.1", []string{"203.0.113.9, 10.0.0.4"}, "10.0.0.4", "203.0.113.9"},
		{"an X-Real-Ip-only proxy passing the visitor's X-Forwarded-For",
			"127.0.0.1", "127.0.0.1", []string{"198.51.100.1"}, "203.0.113.9", "127.0.0.1"},
		{"an X-Forwarded-For-only proxy passing the visitor's X-Real-Ip",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9"}, "198.51.100.7", "127.0.0.1"},
		{"X-Real-Ip naming a forged hop left of the client",
			"127.0.0.1", "127.0.0.1", []string{"198.51.100.1, 203.0.113.9"}, "198.51.100.1", "127.0.0.1"},
		{"X-Real-Ip naming the peer, which the chain never named",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9"}, "127.0.0.1", "127.0.0.1"},
		{"a non-address X-Real-Ip beside a chain is ignored",
			"127.0.0.1", "127.0.0.1", []string{"203.0.113.9"}, "unknown", "203.0.113.9"},
		{"X-Real-Ip from an untrusted peer does not override its chain either",
			"127.0.0.1", "192.0.2.1", []string{"203.0.113.9"}, "198.51.100.7", "192.0.2.1"},
		{"a non-address X-Real-Ip is ignored",
			"127.0.0.1", "127.0.0.1", nil, "anything at all", "127.0.0.1"},
		{"a trusted peer with no header is the client",
			"127.0.0.1", "127.0.0.1", nil, "", "127.0.0.1"},

		{"a peer that is not an address is returned as given",
			"127.0.0.1", "@", []string{"203.0.113.9"}, "", "@"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := Parse(tt.spec)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.spec, err)
			}
			if got := set.ClientAddr(tt.peer, tt.xff, tt.real); got != tt.want {
				t.Errorf("ClientAddr(%q, %q, %q) = %q, want %q", tt.peer, tt.xff, tt.real, got, tt.want)
			}
		})
	}
}
