package checker

import (
	"context"
	"errors"
	"net"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func init() {
	// No test reads this host's resolv.conf to word a message.
	systemResolvers = func() ([]string, error) { return []string{"192.0.2.53:53"}, nil }
}

// A lookup that got no answer before the check ran out of time is a DNS
// failure that names the resolver, not "timed out after 10s": the error the
// request returns says the deadline passed as well, and the lookup must be
// read first.
func TestUnansweredLookupIsADNSFailureNamingTheResolver(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	// What net returns for a lookup cut off by the context: the deadline is
	// wrapped inside the DNSError, so errors.Is finds it.
	lookup := &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true, UnwrapErr: context.DeadlineExceeded}
	err := &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Op: "dial", Net: "tcp", Err: lookup}}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("the fixture does not carry the deadline, so it proves nothing about the order")
	}

	res := classifyRequestError(time.Now().Add(-10*time.Second), ctx, err)
	if res.Kind != FailDNS {
		t.Fatalf("kind = %q, want dns (error %q)", res.Kind, res.Error)
	}
	if !strings.Contains(res.Error, "DNS lookup timed out") || !strings.Contains(res.Error, "no answer from 192.0.2.53") {
		t.Fatalf("error = %q, want it to say the lookup timed out and name the resolver", res.Error)
	}
	if !res.ResolverFailed {
		t.Fatal("an unanswered lookup is not marked as the resolver's failure")
	}
}

func TestLookupErrorsAndTheResolverFlag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       *net.DNSError
		resolver  bool
		wantInErr string
	}{
		{"no such host", &net.DNSError{Err: "no such host", Name: "gone.example", IsNotFound: true}, false,
			"DNS lookup failed: host gone.example not found"},
		{"server failure", &net.DNSError{Err: "server misbehaving", Name: "example.com", Server: "10.0.0.53:53"}, true,
			"DNS lookup failed: server misbehaving (resolver 10.0.0.53)"},
		{"timeout from a named server", &net.DNSError{Err: "i/o timeout", Name: "example.com", Server: "[2001:db8::53]:53", IsTimeout: true}, true,
			"no answer from 2001:db8::53"},
		{"resolver on another port", &net.DNSError{Err: "connection refused", Name: "example.com", Server: "127.0.0.1:5353"}, true,
			"(resolver 127.0.0.1:5353)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := classifyRequestError(time.Now(), context.Background(), &url.Error{Op: "Get", URL: "x", Err: tc.err})
			if res.Kind != FailDNS || res.ResolverFailed != tc.resolver || !strings.Contains(res.Error, tc.wantInErr) {
				t.Fatalf("got kind=%q resolver=%v error=%q; want dns, resolver=%v, containing %q",
					res.Kind, res.ResolverFailed, res.Error, tc.resolver, tc.wantInErr)
			}
		})
	}
}

// A check cancelled during its lookup is still a cancelled check: shutdown is
// nobody's DNS failure.
func TestCancelledLookupIsNotADNSFailure(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "x", Err: &net.DNSError{Err: "operation was canceled", Name: "example.com", UnwrapErr: context.Canceled}}
	if res := classifyRequestError(time.Now(), context.Background(), err); res.Kind != FailInternal || res.ResolverFailed {
		t.Fatalf("got kind=%q resolver=%v, want internal and not the resolver's", res.Kind, res.ResolverFailed)
	}
}

// The HTTP transport can return the deadline before the dial that would
// report the lookup has: the trace is what knows the lookup was still out.
func TestLookupWatchKnowsALookupThatNeverReturned(t *testing.T) {
	w := &lookupWatch{}
	trace := w.trace()
	if w.failure() != nil {
		t.Fatal("a request that looked nothing up reports a lookup failure")
	}
	trace.DNSStart(httptrace.DNSStartInfo{Host: "example.com"})
	got := w.failure()
	if got == nil || !got.IsTimeout || got.Name != "example.com" {
		t.Fatalf("pending lookup = %+v, want a timeout for example.com", got)
	}
	trace.DNSDone(httptrace.DNSDoneInfo{})
	if w.failure() != nil {
		t.Fatal("an answered lookup is reported as failed")
	}
	trace.DNSStart(httptrace.DNSStartInfo{Host: "example.com"})
	trace.DNSDone(httptrace.DNSDoneInfo{Err: &net.DNSError{Err: "server misbehaving", Name: "example.com"}})
	if got := w.failure(); got == nil || got.Err != "server misbehaving" {
		t.Fatalf("failed lookup = %+v, want server misbehaving", got)
	}
}

// Asked of this host's resolvers, silence and a server failure may be the
// resolver's fault; NXDOMAIN is an answer. A monitor's own resolver is what
// it watches, so its silence is never put down to this host.
func TestDNSMonitorMarksOnlyTheSystemResolversFailures(t *testing.T) {
	f := newFakeDNS(t)
	f.mu.Lock()
	f.rcode["gone.test"] = dnsmessage.RCodeNameError
	f.rcode["broken.test"] = dnsmessage.RCodeServerFailure
	f.mu.Unlock()
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })

	system := openChecker()
	system.systemServers = func() ([]string, error) { return []string{f.addr}, nil }
	quiet := openChecker()
	quiet.retransmitAfter = 50 * time.Millisecond
	quiet.systemServers = func() ([]string, error) { return []string{silent.LocalAddr().String()}, nil }

	onSystem := func(name string) Monitor {
		m := f.monitor(name, DNSRecordA)
		m.DNSResolver = ""
		m.Timeout = 300 * time.Millisecond
		return m
	}
	ownSilent := Monitor{Type: TypeDNS, Target: "example.test", Timeout: 300 * time.Millisecond,
		DNSRecordType: DNSRecordA, DNSResolver: silent.LocalAddr().String()}
	for _, tc := range []struct {
		name     string
		c        *DNSChecker
		m        Monitor
		resolver bool
	}{
		{"SERVFAIL from the system resolver", system, onSystem("broken.test"), true},
		{"NXDOMAIN from the system resolver", system, onSystem("gone.test"), false},
		{"silence from the system resolver", quiet, onSystem("example.test"), true},
		{"SERVFAIL from the monitor's own resolver", system, f.monitor("broken.test", DNSRecordA), false},
		{"silence from the monitor's own resolver", quiet, ownSilent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.c.Check(context.Background(), tc.m)
			if res.OK || res.ResolverFailed != tc.resolver {
				t.Fatalf("OK=%v ResolverFailed=%v (kind %q, error %q), want ResolverFailed=%v",
					res.OK, res.ResolverFailed, res.Kind, res.Error, tc.resolver)
			}
		})
	}
}

// End to end over a silent resolver: the HTTP check runs out of time while
// its lookup is still waiting, and says that it was the lookup.
func TestHTTPCheckWhoseLookupGetsNoAnswer(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", silent.LocalAddr().String())
	}}
	c := NewHTTPChecker(HTTPOptions{Guard: NewGuard(true)})
	c.transport.DialContext = (&net.Dialer{Resolver: resolver}).DialContext

	res := c.Check(context.Background(), Monitor{Type: TypeHTTP, Target: "http://site.example/", Timeout: 300 * time.Millisecond})
	if res.OK || res.Kind != FailDNS || !res.ResolverFailed || !strings.Contains(res.Error, "DNS lookup timed out") {
		t.Fatalf("got OK=%v kind=%q resolver=%v error=%q; want an unanswered lookup",
			res.OK, res.Kind, res.ResolverFailed, res.Error)
	}
}
