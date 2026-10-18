package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// A check reports the address it connected to whether it then passed or
// not: a 503 or an untrusted certificate still proves this host reached the
// server, which is what the connectivity check needs to know. A check that
// never connected reports none.
func TestChecksReportTheAddressTheyConnectedTo(t *testing.T) {
	loopback := netip.MustParseAddr("127.0.0.1")

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	res := testChecker().Check(context.Background(), monitor(down.URL))
	if res.OK || res.Kind != FailStatus {
		t.Fatalf("http: %s %q, want a status failure", res.Kind, res.Error)
	}
	if res.Peer != loopback {
		t.Fatalf("http: Peer = %v, want %v", res.Peer, loopback)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	open := ln.Addr().String()
	res = NewTCPChecker(NewGuard(true)).Check(context.Background(), Monitor{Type: TypeTCP, Target: open, Timeout: 5 * time.Second})
	if !res.OK || res.Peer != loopback {
		t.Fatalf("tcp: ok=%v Peer=%v, want a pass from %v", res.OK, res.Peer, loopback)
	}

	// A self-signed certificate this checker does not trust: the TLS check
	// fails after the connection was made.
	now := time.Now()
	cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	addr := startTLSServer(t, cert)
	res = NewSSLChecker(NewGuard(true)).Check(context.Background(), Monitor{Type: TypeSSL, Target: addr, Timeout: 5 * time.Second})
	if res.OK || res.Kind != FailTLS {
		t.Fatalf("ssl: %s %q, want a TLS failure", res.Kind, res.Error)
	}
	if res.Peer != loopback {
		t.Fatalf("ssl: Peer = %v, want %v", res.Peer, loopback)
	}

	// Nothing listens here any more: no connection, no address.
	_ = ln.Close()
	res = NewTCPChecker(NewGuard(true)).Check(context.Background(), Monitor{Type: TypeTCP, Target: open, Timeout: 5 * time.Second})
	if res.OK || res.Peer.IsValid() {
		t.Fatalf("tcp to a closed port: ok=%v Peer=%v, want a failure with no address", res.OK, res.Peer)
	}
	down.Close()
	res = testChecker().Check(context.Background(), monitor(down.URL))
	if res.OK || res.Peer.IsValid() {
		t.Fatalf("http to a closed port: ok=%v Peer=%v, want a failure with no address", res.OK, res.Peer)
	}
}

func TestRemoteAddrUnmapsAndRefusesNonsense(t *testing.T) {
	for in, want := range map[string]netip.Addr{
		"93.184.215.14:443":          netip.MustParseAddr("93.184.215.14"),
		"[::ffff:93.184.215.14]:443": netip.MustParseAddr("93.184.215.14"),
		"[2606:2800:21f::1]:443":     netip.MustParseAddr("2606:2800:21f::1"),
		"example.com:443":            {},
		"":                           {},
	} {
		if got := remoteAddr(in); got != want {
			t.Errorf("remoteAddr(%q) = %v, want %v", in, got, want)
		}
	}
}
