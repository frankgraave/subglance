package trustedproxy

import "testing"

func TestParseRejectsNonsense(t *testing.T) {
	for _, spec := range []string{"not-an-address", "10.0.0.0/33", "10.0.0.0/", "1.2.3.4.5"} {
		if err := Validate(spec); err == nil {
			t.Errorf("Validate(%q) = nil, want an error — a typo must not read as an empty set", spec)
		}
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		spec string
		ip   string
		want bool
	}{
		// The zero value is the security property: no configuration means no
		// peer may speak for another.
		{"", "10.0.0.1", false},
		{"", "127.0.0.1", false},

		{"10.0.0.0/8", "10.4.5.6", true},
		{"10.0.0.0/8", "11.0.0.1", false},

		// A bare address is one host, not its whole subnet.
		{"192.0.2.9", "192.0.2.9", true},
		{"192.0.2.9", "192.0.2.10", false},

		// A prefix given with host bits set still covers its range.
		{"10.1.2.3/8", "10.9.9.9", true},

		{"127.0.0.1, 10.0.0.0/8", "127.0.0.1", true},
		{"127.0.0.1, 10.0.0.0/8", "10.0.0.7", true},
		{"127.0.0.1, 10.0.0.0/8", "203.0.113.1", false},

		// An IPv4 peer on a dual-stack listener arrives mapped into v6.
		{"10.0.0.0/8", "::ffff:10.0.0.1", true},

		{"::1", "::1", true},
		{"fd00::/8", "fd12::1", true},

		// Not an address at all: a Unix socket peer, say. Trusting on a parse
		// failure would be the one direction that costs something.
		{"10.0.0.0/8", "@", false},
		{"10.0.0.0/8", "", false},
	}
	for _, tt := range tests {
		s, err := Parse(tt.spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.spec, err)
		}
		if got := s.Contains(tt.ip); got != tt.want {
			t.Errorf("Parse(%q).Contains(%q) = %v, want %v", tt.spec, tt.ip, got, tt.want)
		}
	}
}

func TestStringIsReadableInALog(t *testing.T) {
	s, err := Parse("")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := s.String(); got != "none" {
		t.Errorf("empty set renders as %q, want %q", got, "none")
	}

	s, err = Parse("10.0.0.0/8, 192.0.2.9")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := s.String(); got != "10.0.0.0/8,192.0.2.9/32" {
		t.Errorf("String() = %q", got)
	}
}
