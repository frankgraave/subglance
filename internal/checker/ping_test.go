package checker

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Ping needs either unprivileged ICMP sockets (net.ipv4.ping_group_range) or
// CAP_NET_RAW. Neither is guaranteed in CI, so the network-touching tests skip
// rather than fail when the environment does not allow ICMP — a red build that
// says nothing about the code is worse than an honest skip.
func requirePing(t *testing.T) *PingChecker {
	t.Helper()

	c := NewPingChecker(NewGuard(true))
	c.once.Do(c.detectMode)
	if c.err != nil {
		t.Skipf("ICMP not permitted in this environment: %v", c.err)
	}
	return c
}

func TestPingReachesLoopback(t *testing.T) {
	c := requirePing(t)

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "127.0.0.1",
		Timeout: 5 * time.Second,
	})

	if !res.OK {
		t.Fatalf("expected loopback to answer, got %s: %s", res.Kind, res.Error)
	}
	if res.Latency <= 0 {
		t.Error("expected a measured round-trip time")
	}
}

func TestPingResolvesHostname(t *testing.T) {
	requireNetwork(t)
	c := requirePing(t)

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "localhost",
		Timeout: 5 * time.Second,
	})

	if !res.OK {
		t.Fatalf("expected localhost to answer, got %s: %s", res.Kind, res.Error)
	}
}

// A URL is a reasonable thing to paste into a ping monitor by mistake, and
// pinging its host is what the user meant.
func TestPingAcceptsURLShapedTarget(t *testing.T) {
	requireNetwork(t)
	c := requirePing(t)

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "http://localhost/health",
		Timeout: 5 * time.Second,
	})

	if !res.OK {
		t.Fatalf("expected the host to be extracted and pinged, got %s: %s", res.Kind, res.Error)
	}
}

func TestPingTimesOutOnDeadAddress(t *testing.T) {
	requireNetwork(t)
	c := requirePing(t)

	// TEST-NET-2: routable-looking, guaranteed to answer nothing.
	start := time.Now()
	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "198.51.100.1",
		Timeout: 1 * time.Second,
	})
	elapsed := time.Since(start)

	if res.OK {
		t.Fatal("expected no reply from a dead address")
	}
	if elapsed > 3*time.Second {
		t.Errorf("check took %s, want it bounded near the 1s timeout", elapsed)
	}
}

func TestPingFailsOnUnknownHost(t *testing.T) {
	requireNetwork(t)
	c := requirePing(t)

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "no-such-host.invalid",
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected DNS failure")
	}
	if res.Kind != FailDNS {
		t.Errorf("kind = %q, want %q", res.Kind, FailDNS)
	}
}

// Without this the ping check would be an internal-network host scanner: add a
// monitor, watch whether it goes green, learn what is alive behind the
// firewall.
func TestPingRespectsGuard(t *testing.T) {
	c := NewPingChecker(NewGuard(false)) // guard on
	c.once.Do(c.detectMode)
	if c.err != nil {
		t.Skipf("ICMP not permitted: %v", c.err)
	}

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "127.0.0.1",
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("guard should have refused a loopback ping")
	}
	if !strings.Contains(res.Error, "allow-private-targets") {
		t.Errorf("error = %q, should tell the operator which flag to set", res.Error)
	}
}

func TestPingEmptyTarget(t *testing.T) {
	c := NewPingChecker(NewGuard(true))

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "",
		Timeout: time.Second,
	})

	if res.OK {
		t.Fatal("expected failure on an empty target")
	}
	if res.Kind != FailInternal {
		t.Errorf("kind = %q, want %q", res.Kind, FailInternal)
	}
}

// Every checker constructor must fail closed on a nil guard. Ping is the one
// that cannot rely on a Dialer Control hook, so a nil guard here would mean no
// SSRF protection at all rather than merely a missing convenience.
func TestPingNilGuardFailsClosed(t *testing.T) {
	c := NewPingChecker(nil)
	c.once.Do(c.detectMode)
	if c.err != nil {
		t.Skipf("ICMP not permitted: %v", c.err)
	}

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "127.0.0.1",
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("a nil guard must fail closed, not disable the SSRF check")
	}
	if !strings.Contains(res.Error, "loopback") && !strings.Contains(res.Error, "private") {
		t.Errorf("error = %q, want the guard's rejection", res.Error)
	}
}

// When ICMP is unavailable the message must name the fix. "operation not
// permitted" on its own is the kind of error that costs someone an evening.
func TestPingUnavailableExplainsHowToFixIt(t *testing.T) {
	c := NewPingChecker(NewGuard(true))
	c.once.Do(func() {}) // block real detection
	c.err = errPingUnavailable()

	res := c.Check(context.Background(), Monitor{
		Type:    TypePing,
		Target:  "127.0.0.1",
		Timeout: time.Second,
	})

	if res.OK {
		t.Fatal("expected failure when ICMP is unavailable")
	}
	for _, want := range []string{"NET_RAW", "ping_group_range", "TCP check"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("error should mention %q, got: %s", want, res.Error)
		}
	}
}
