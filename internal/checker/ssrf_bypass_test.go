package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The guard exists to stop a user-supplied URL from reaching the internal
// network. Blocking a literal 127.0.0.1 is the easy half; these tests cover the
// ways an attacker gets there without ever typing a private address.
//
// Every test here is hermetic: the "remote" hosts are local listeners and the
// rebinding resolver is injected, so nothing depends on a real DNS zone.

// A redirect is a connection to an address the user never supplied. Validating
// only the saved URL would miss it, which is why the guard runs in the dialer
// rather than at save time.
//
// Both servers here are on loopback, so the test composes the control hook the
// way NewHTTPChecker does but treats the first server's port as if it were
// public. That keeps the production guard untouched while still exercising the
// real question: is the hook consulted again on the second hop?
func TestSSRFRedirectToPrivateIsBlocked(t *testing.T) {
	t.Parallel()

	var internalReached atomic.Bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalReached.Store(true)
		_, _ = w.Write([]byte("internal secrets"))
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	g := NewGuard(false)
	entryPort := portOf(t, redirector.Listener.Addr().String())

	dialer := &net.Dialer{Control: controlTreatingPortAsPublic(g, entryPort)}
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirector.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req) //nolint:bodyclose // closed below when non-nil
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the redirect into a private address was followed; the guard " +
			"must run on every hop, not only on the URL the user saved")
	}
	if internalReached.Load() {
		t.Error("the internal service was contacted")
	}
	if !errors.Is(err, ErrPrivateTarget) {
		t.Errorf("error = %v, want ErrPrivateTarget", err)
	}
}

// DNS rebinding: the name resolves to a harmless address while the target is
// validated and to a private one when the connection is actually made.
// Hostname validation cannot catch this; checking the resolved IP at dial time
// can. This test asserts both halves of that claim.
func TestSSRFDNSRebindingIsBlocked(t *testing.T) {
	t.Parallel()

	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal secrets"))
	}))
	defer internal.Close()

	// First answer is public, every later one is loopback — the classic rebind.
	var lookups atomic.Int32
	resolve := func(context.Context) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}

	g := NewGuard(false)

	// Pre-flight validation passes: at this moment the name looks public.
	// This is the window an attacker relies on.
	if err := checkResolved(t.Context(), g, resolve); err != nil {
		t.Fatalf("pre-flight should pass on the first answer, got: %v", err)
	}

	// The dial resolves again and gets loopback. Control sees the address the
	// kernel is about to reach, which is the whole point of hooking there.
	rebound, err := resolve(t.Context())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := g.CheckAddr(rebound[0]); err == nil {
		t.Fatal("the rebound address passed the guard")
	} else if !errors.Is(err, ErrPrivateTarget) {
		t.Errorf("error = %v, want ErrPrivateTarget", err)
	}

	// And end to end through a real dialer, to prove the hook is wired.
	port := portOf(t, internal.Listener.Addr().String())
	dialer := &net.Dialer{Control: g.ControlFunc()}
	conn, err := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
	if err == nil {
		_ = conn.Close()
		t.Fatal("dial to the rebound address succeeded; the guard must run at " +
			"connect time, not only when the monitor is saved")
	}
	if !errors.Is(err, ErrPrivateTarget) {
		t.Errorf("dial error = %v, want ErrPrivateTarget", err)
	}
}

// An IPv4 address written in IPv6-mapped form is the same address. Judging it
// on its textual form would let ::ffff:127.0.0.1 through.
func TestSSRFMappedAddressAtDialTime(t *testing.T) {
	t.Parallel()

	control := NewGuard(false).ControlFunc()

	for _, addr := range []string{
		"[::ffff:127.0.0.1]:80",
		"[::ffff:10.0.0.1]:80",
		"[::ffff:169.254.169.254]:80",
		"[::ffff:192.168.1.1]:443",
	} {
		if err := control("tcp6", addr, nil); err == nil {
			t.Errorf("control(%q) allowed the dial; the IPv4-mapped form must be "+
				"judged on its IPv4 value", addr)
		}
	}
}

// The metadata endpoint is the highest-value SSRF target on a cloud host: it
// hands credentials to anything that can make a plain GET. IP literals only, so
// no resolver is involved.
func TestSSRFCloudMetadataUnreachable(t *testing.T) {
	t.Parallel()

	c := NewHTTPChecker(HTTPOptions{Guard: NewGuard(false)})

	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[fd00:ec2::254]/latest/meta-data/",
		"http://100.100.100.200/latest/meta-data/", // Alibaba
	} {
		res := c.Check(t.Context(), Monitor{
			ID: 1, Type: "http", Target: target, Timeout: 3 * time.Second,
		})
		if res.OK {
			t.Errorf("%s was reachable", target)
		}
		if !strings.Contains(strings.ToLower(res.Error), "private") &&
			!strings.Contains(strings.ToLower(res.Error), "link-local") &&
			!strings.Contains(strings.ToLower(res.Error), "reserved") &&
			!strings.Contains(strings.ToLower(res.Error), "loopback") {
			t.Errorf("%s: error %q should say the address was refused as internal", target, res.Error)
		}
	}
}

// The guard must cover every check type, not just HTTP. A TCP or SSL monitor
// pointed at the metadata service is the same attack with a different label.
func TestSSRFAllCheckTypesGuarded(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	loopback := srv.Listener.Addr().String()

	g := NewGuard(false)

	tcpChecker := NewTCPChecker(g)
	if res := tcpChecker.Check(t.Context(), Monitor{
		ID: 1, Type: "tcp", Target: loopback, Timeout: 3 * time.Second,
	}); res.OK {
		t.Error("tcp check reached a loopback target")
	}

	sslChecker := NewSSLChecker(g)
	if res := sslChecker.Check(t.Context(), Monitor{
		ID: 2, Type: "ssl", Target: loopback, Timeout: 3 * time.Second,
	}); res.OK {
		t.Error("ssl check reached a loopback target")
	}

	pingChecker := NewPingChecker(g)
	if res := pingChecker.Check(t.Context(), Monitor{
		ID: 3, Type: "ping", Target: "127.0.0.1", Timeout: 3 * time.Second,
	}); res.OK {
		t.Error("ping check reached a loopback target")
	}
}

// Disabling the guard is an operator decision and must actually work — plenty
// of people monitor a LAN on purpose.
func TestSSRFAllowPrivateOptIn(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHTTPChecker(HTTPOptions{Guard: NewGuard(true)})
	res := c.Check(t.Context(), Monitor{
		ID: 1, Type: "http", Target: srv.URL, Timeout: 5 * time.Second,
	})
	if !res.OK {
		t.Errorf("--allow-private-targets did not permit a loopback target: %s", res.Error)
	}
}

// checkResolved runs pre-flight validation against an injected resolver, so the
// rebinding scenario needs no real DNS zone.
func checkResolved(
	ctx context.Context,
	g *Guard,
	resolve func(context.Context) ([]netip.Addr, error),
) error {
	addrs, err := resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	for _, a := range addrs {
		if err := g.CheckAddr(a); err != nil {
			return err
		}
	}
	return nil
}

// controlTreatingPortAsPublic delegates to the real guard except for one
// loopback port, which stands in for a legitimate public server.
//
// This lives in the test rather than in Guard on purpose: an allowlist field on
// the production type would be a bypass waiting to be misused, and the point of
// the test is the hop *after* the entry point.
func controlTreatingPortAsPublic(g *Guard, allowPort string) func(string, string, syscall.RawConn) error {
	real := g.ControlFunc()
	return func(network, address string, c syscall.RawConn) error {
		if _, port, err := net.SplitHostPort(address); err == nil && port == allowPort {
			return nil
		}
		return real(network, address, c)
	}
}

func portOf(t *testing.T, hostPort string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split %q: %v", hostPort, err)
	}
	return port
}
