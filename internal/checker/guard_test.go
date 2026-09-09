package checker

import (
	"net/netip"
	"testing"
)

func TestGuardBlocksDangerousAddresses(t *testing.T) {
	g := NewGuard(false)

	blocked := []struct {
		addr string
		why  string
	}{
		{"127.0.0.1", "loopback"},
		{"127.1.2.3", "loopback range"},
		{"::1", "IPv6 loopback"},
		{"10.0.0.1", "private class A"},
		{"172.16.0.1", "private class B"},
		{"172.31.255.255", "private class B upper bound"},
		{"192.168.1.1", "private class C"},
		{"169.254.169.254", "cloud metadata service"},
		{"169.254.0.1", "link-local"},
		{"fe80::1", "IPv6 link-local"},
		{"0.0.0.0", "unspecified"},
		{"::", "IPv6 unspecified"},
		{"224.0.0.1", "multicast"},
		{"ff02::1", "IPv6 multicast"},
		{"fc00::1", "IPv6 unique local"},
		{"fd00::1", "IPv6 unique local"},
		{"100.64.0.1", "carrier-grade NAT"},
		{"192.0.2.1", "TEST-NET-1"},
		{"198.51.100.1", "TEST-NET-2"},
		{"203.0.113.1", "TEST-NET-3"},
		{"198.18.0.1", "benchmarking range"},
		{"240.0.0.1", "reserved"},
		{"255.255.255.255", "broadcast"},
		{"0.1.2.3", "this-network"},
		{"2001:db8::1", "documentation range"},
	}

	for _, tc := range blocked {
		t.Run(tc.addr, func(t *testing.T) {
			addr := netip.MustParseAddr(tc.addr)
			if err := g.CheckAddr(addr); err == nil {
				t.Errorf("%s (%s) was allowed; it must be blocked", tc.addr, tc.why)
			}
		})
	}
}

// An IPv4 address written in IPv6-mapped form must be judged on its IPv4 value.
// Missing this is the classic way an SSRF guard gets bypassed.
func TestGuardBlocksIPv4MappedBypass(t *testing.T) {
	g := NewGuard(false)

	mapped := []string{
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
		"::ffff:10.0.0.1",
		"::ffff:192.168.0.1",
	}
	for _, s := range mapped {
		t.Run(s, func(t *testing.T) {
			addr, err := netip.ParseAddr(s)
			if err != nil {
				t.Fatalf("parse %s: %v", s, err)
			}
			if err := g.CheckAddr(addr); err == nil {
				t.Errorf("%s was allowed; the IPv4-mapped form bypasses the guard", s)
			}
		})
	}
}

func TestGuardAllowsPublicAddresses(t *testing.T) {
	g := NewGuard(false)

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34", // example.com
		"172.32.0.1",    // just outside the private class B range
		"172.15.255.255",
		"2606:4700:4700::1111", // Cloudflare DNS
	}
	for _, s := range allowed {
		t.Run(s, func(t *testing.T) {
			if err := g.CheckAddr(netip.MustParseAddr(s)); err != nil {
				t.Errorf("public address %s was blocked: %v", s, err)
			}
		})
	}
}

func TestGuardAllowPrivateDisablesEverything(t *testing.T) {
	g := NewGuard(true)

	for _, s := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::1"} {
		if err := g.CheckAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("with AllowPrivate the address %s should pass, got: %v", s, err)
		}
	}
}

func TestGuardRejectsInvalidAddress(t *testing.T) {
	g := NewGuard(false)
	if err := g.CheckAddr(netip.Addr{}); err == nil {
		t.Error("the zero Addr was accepted")
	}
}

func TestCheckHostWithLiteralIP(t *testing.T) {
	g := NewGuard(false)

	if err := g.CheckHost("127.0.0.1"); err == nil {
		t.Error("CheckHost accepted a loopback literal")
	}
	if err := g.CheckHost("1.1.1.1"); err != nil {
		t.Errorf("CheckHost rejected a public literal: %v", err)
	}
}

func TestControlFuncEnforcesPolicy(t *testing.T) {
	g := NewGuard(false)
	control := g.ControlFunc()

	if err := control("tcp4", "127.0.0.1:8080", nil); err == nil {
		t.Error("ControlFunc allowed a loopback connection")
	}
	if err := control("tcp4", "1.1.1.1:443", nil); err != nil {
		t.Errorf("ControlFunc blocked a public connection: %v", err)
	}
	if err := control("tcp4", "garbage", nil); err == nil {
		t.Error("ControlFunc accepted a malformed address")
	}
}
