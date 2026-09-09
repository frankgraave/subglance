package checker

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		defaultPort int
		wantHost    string
		wantPort    int
		wantErr     bool
	}{
		{name: "host and port", target: "db.example.com:5432", wantHost: "db.example.com", wantPort: 5432},
		{name: "host only", target: "example.com", wantHost: "example.com", wantPort: 0},
		{name: "host only with default", target: "example.com", defaultPort: 443, wantHost: "example.com", wantPort: 443},
		{name: "explicit port beats default", target: "example.com:8443", defaultPort: 443, wantHost: "example.com", wantPort: 8443},

		// A scheme should not be a reason to reject a target; people paste URLs.
		{name: "https scheme implies 443", target: "https://example.com", wantHost: "example.com", wantPort: 443},
		{name: "http scheme implies 80", target: "http://example.com", wantHost: "example.com", wantPort: 80},
		{name: "scheme with explicit port", target: "https://example.com:8443", wantHost: "example.com", wantPort: 8443},
		{name: "tcp scheme, no implied port", target: "tcp://example.com:5432", wantHost: "example.com", wantPort: 5432},

		// Paths, queries and credentials are meaningless here but must not error.
		{name: "path is ignored", target: "https://example.com/health", wantHost: "example.com", wantPort: 443},
		{name: "query is ignored", target: "https://example.com/x?y=1", wantHost: "example.com", wantPort: 443},
		{name: "credentials are stripped", target: "https://user:pass@example.com", wantHost: "example.com", wantPort: 443},

		{name: "ipv4", target: "192.0.2.10:5432", wantHost: "192.0.2.10", wantPort: 5432},
		{name: "bracketed ipv6 with port", target: "[2001:db8::1]:5432", wantHost: "2001:db8::1", wantPort: 5432},
		{name: "bracketed ipv6 without port", target: "[2001:db8::1]", defaultPort: 443, wantHost: "2001:db8::1", wantPort: 443},
		{name: "bare ipv6", target: "2001:db8::1", defaultPort: 443, wantHost: "2001:db8::1", wantPort: 443},

		{name: "empty", target: "", wantErr: true},
		{name: "port out of range", target: "example.com:70000", wantErr: true},
		{name: "port not a number", target: "example.com:http", wantErr: true},
		{name: "unbalanced bracket", target: "[2001:db8::1:5432", wantErr: true},
		{name: "scheme only", target: "https://", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port, err := ParseHostPort(tc.target, tc.defaultPort)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got host=%q port=%d", host, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if port != tc.wantPort {
				t.Errorf("port = %d, want %d", port, tc.wantPort)
			}
		})
	}
}

func TestTCPCheckSucceedsOnOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Accept and immediately close, like any real service would.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// Loopback needs the guard disabled, which is what a local test target is.
	c := NewTCPChecker(NewGuard(true))
	res := c.Check(context.Background(), Monitor{
		Type:    TypeTCP,
		Target:  ln.Addr().String(),
		Timeout: 5 * time.Second,
	})

	if !res.OK {
		t.Fatalf("expected success, got %s: %s", res.Kind, res.Error)
	}
	if res.Latency <= 0 {
		t.Error("expected a measured latency")
	}
}

func TestTCPCheckFailsOnClosedPort(t *testing.T) {
	// Bind then close, so the port is almost certainly free and refusing.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := NewTCPChecker(NewGuard(true))
	res := c.Check(context.Background(), Monitor{
		Type:    TypeTCP,
		Target:  addr,
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected failure against a closed port")
	}
	if res.Kind != FailConnection {
		t.Errorf("kind = %q, want %q", res.Kind, FailConnection)
	}
	// The message has to name the problem; "check failed" helps nobody.
	if !strings.Contains(res.Error, "refused") {
		t.Errorf("error = %q, want it to mention the connection being refused", res.Error)
	}
}

func TestTCPCheckRequiresPort(t *testing.T) {
	c := NewTCPChecker(NewGuard(true))
	res := c.Check(context.Background(), Monitor{
		Type:    TypeTCP,
		Target:  "example.com",
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected failure when no port is given")
	}
	if res.Kind != FailInternal {
		t.Errorf("kind = %q, want %q", res.Kind, FailInternal)
	}
	if !strings.Contains(res.Error, "port") {
		t.Errorf("error = %q, should explain that a port is required", res.Error)
	}
}

// The SSRF guard has to cover TCP too. Without it, a TCP monitor becomes a
// port scanner for the internal network.
func TestTCPCheckRespectsGuard(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	c := NewTCPChecker(NewGuard(false)) // guard on
	res := c.Check(context.Background(), Monitor{
		Type:    TypeTCP,
		Target:  ln.Addr().String(),
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("guard should have refused a loopback target")
	}
	if !strings.Contains(res.Error, "allow-private-targets") {
		t.Errorf("error = %q, should tell the operator which flag to set", res.Error)
	}
}

func TestTCPCheckTimesOut(t *testing.T) {
	// 198.51.100.0/24 is TEST-NET-2: routable-looking and guaranteed dead.
	c := NewTCPChecker(NewGuard(true))

	start := time.Now()
	res := c.Check(context.Background(), Monitor{
		Type:    TypeTCP,
		Target:  "198.51.100.1:9999",
		Timeout: 1 * time.Second,
	})
	elapsed := time.Since(start)

	if res.OK {
		t.Fatal("expected a timeout against an unreachable address")
	}
	if res.Kind != FailTimeout && res.Kind != FailConnection {
		t.Errorf("kind = %q, want timeout or connection", res.Kind)
	}
	// The monitor's timeout must actually bound the check, or one dead target
	// would hold a worker far longer than its interval.
	if elapsed > 3*time.Second {
		t.Errorf("check took %s, want it bounded near the 1s timeout", elapsed)
	}
}
