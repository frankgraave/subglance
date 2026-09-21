package checker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"
)

// Preserve the production transport policy; replace only its socket destination.
func localOCSPClient(t *testing.T, c *SSLChecker, server *httptest.Server) {
	t.Helper()
	transport := c.ocspClient.Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := *c.ocspClient
	client.Transport = transport
	c.ocspClient = &client
	t.Cleanup(transport.CloseIdleConnections)
}

func TestOCSPPrivateRespondersBlocked(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write([]byte("private service")) }))
	defer server.Close()
	for _, name := range []string{"literal", "resolved-hostname"} {
		t.Run(name, func(t *testing.T) {
			url := server.URL
			if name == "resolved-hostname" {
				url = "http://ocsp.test/status"
			}
			f := newOCSPFixture(t, url)
			c := sslCheckerTrusting(f.roots, f.now) // monitor explicitly allows loopback
			if name == "resolved-hostname" {
				// The URL is public-looking; the resolved socket address is private.
				transport := c.ocspClient.Transport.(*http.Transport)
				guardedDial := transport.DialContext
				transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					return guardedDial(ctx, network, server.Listener.Addr().String())
				}
			}
			result := c.Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
			if !result.OK {
				t.Fatalf("blocked responder must soft-fail: %+v", result)
			}
			if calls.Load() != 0 {
				t.Fatal("certificate-controlled URL reached a private service")
			}
		})
	}
}

func TestOCSPURLAndRedirectPolicy(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "http://internal.test/secret", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := NewSSLChecker(nil)
	localOCSPClient(t, c, server)
	for _, url := range []string{"file:///etc/passwd", "ftp://ocsp.test/x", "http://user:secret@ocsp.test/", "http://ocsp.test/#secret", "http:///missing-host", "http://ocsp.test/" + strings.Repeat("x", 2048)} {
		t.Run(url[:min(len(url), 60)], func(t *testing.T) {
			if got := c.fetchOCSP(t.Context(), url, []byte("request")); got != nil {
				t.Error("invalid URL returned response")
			}
			if calls.Load() != 0 {
				t.Fatal("invalid responder URL reached network")
			}
		})
	}
	c.fetchOCSP(t.Context(), "http://ocsp.test/", []byte("request"))
	if calls.Load() != 1 {
		t.Fatalf("redirect followed: %d requests, want 1", calls.Load())
	}
}

func TestOCSPDoesNotUseEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:8181")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:8181")
	t.Setenv("NO_PROXY", "")
	for _, scheme := range []string{"http", "https"} {
		c := NewSSLChecker(nil)
		transport := c.ocspClient.Transport.(*http.Transport)
		var destination string
		transport.DialContext = func(_ context.Context, _, addr string) (net.Conn, error) {
			destination = addr
			return nil, fmt.Errorf("test stops before connect")
		}
		c.fetchOCSP(t.Context(), scheme+"://ocsp.test/", []byte("request"))
		port := "80"
		if scheme == "https" {
			port = "443"
		}
		if destination != "ocsp.test:"+port {
			t.Fatalf("certificate request used proxy: dialed %q", destination)
		}
	}
}

func TestOCSPHTTPSVerifiesResponder(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/ocsp-response")
		_, _ = w.Write([]byte("response"))
	}))
	defer server.Close()
	c := NewSSLChecker(nil)
	localOCSPClient(t, c, server)
	if c.fetchOCSP(t.Context(), "https://ocsp.test/", []byte("request")) != nil {
		t.Fatal("untrusted HTTPS responder was accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("OCSP request sent over unverified TLS")
	}
}

type ocspRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ocspRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ocspCountingBody struct {
	io.ReadCloser
	read   int
	closed bool
}

func (b *ocspCountingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += n
	return n, err
}
func (b *ocspCountingBody) Close() error { b.closed = true; return b.ReadCloser.Close() }

func TestOCSPResponseBodyBound(t *testing.T) {
	for _, declared := range []int64{-1, 1 << 20} {
		t.Run(fmt.Sprint(declared), func(t *testing.T) {
			body := &ocspCountingBody{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20)))}
			c := NewSSLChecker(nil)
			c.ocspClient.Transport = ocspRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, ContentLength: declared, Header: http.Header{"Content-Type": []string{"application/ocsp-response"}}, Body: body}, nil
			})
			if got := c.fetchOCSP(t.Context(), "http://ocsp.test/", nil); got != nil {
				t.Error("oversized body accepted")
			}
			maxRead := maxOCSPResponseBytes + 1
			if declared > 0 {
				maxRead = 0
			}
			if body.read > maxRead {
				t.Errorf("read %d bytes, limit %d", body.read, maxRead)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

func TestOCSPResponseHeadersBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Padding", strings.Repeat("x", 20<<10))
		w.Header().Set("Content-Type", "application/ocsp-response")
		_, _ = w.Write([]byte("response"))
	}))
	defer server.Close()
	c := NewSSLChecker(nil)
	localOCSPClient(t, c, server)
	if c.fetchOCSP(t.Context(), "http://ocsp.test/", nil) != nil {
		t.Fatal("oversized response headers accepted")
	}
}

func TestOCSPTimeoutAndCancellationSoftFail(t *testing.T) {
	for _, name := range []string{"monitor-timeout", "parent-timeout", "parent-cancel", "lookup-budget", "slow-body"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 5 * time.Second
			if name == "monitor-timeout" || name == "slow-body" {
				timeout = 150 * time.Millisecond
			}
			if name == "parent-timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 150*time.Millisecond)
				defer stop()
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if name == "slow-body" {
					w.Header().Set("Content-Type", "application/ocsp-response")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				if name == "parent-cancel" {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			f := newOCSPFixture(t, "http://ocsp.test/one", "http://ocsp.test/two", "http://ocsp.test/three")
			c := sslCheckerTrusting(f.roots, f.now)
			localOCSPClient(t, c, server)
			target := startTLSServer(t, f.cert)
			start := time.Now()
			result := c.Check(ctx, Monitor{Type: TypeSSL, Target: target, Timeout: timeout})
			elapsed := time.Since(start)
			if !result.OK {
				t.Fatalf("responder unreachability caused a false outage: %+v", result)
			}
			bound := time.Second
			if name == "lookup-budget" {
				bound = 2500 * time.Millisecond
			}
			if elapsed > bound {
				t.Fatalf("lookup outlived budget: %v, bound %v", elapsed, bound)
			}
			if calls.Load() == 0 {
				t.Fatal("timeout test never contacted responder")
			}
			if name == "lookup-budget" && calls.Load() < 2 {
				t.Fatal("one slow responder exhausted whole lookup; later responder never attempted")
			}
			if result.Latency < elapsed-50*time.Millisecond {
				t.Errorf("latency omitted OCSP wait: %v vs %v", result.Latency, elapsed)
			}
		})
	}
}

func TestOCSPResponderAttemptLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	f := newOCSPFixture(t, "http://ocsp.test/1", "http://ocsp.test/2", "http://ocsp.test/3", "http://ocsp.test/4")
	c := sslCheckerTrusting(f.roots, f.now)
	localOCSPClient(t, c, server)
	result := c.Check(t.Context(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
	if !result.OK || calls.Load() != 3 {
		t.Fatalf("lookup must soft-fail after 3 attempts: result=%+v calls=%d", result, calls.Load())
	}
}

func TestOCSPFallbackTriesNextResponder(t *testing.T) {
	f := newOCSPFixture(t, "http://ocsp.test/unavailable", "http://ocsp.test/revoked")
	der := f.response(t, ocsp.Revoked)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/unavailable" {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/ocsp-response")
		_, _ = w.Write(der)
	}))
	defer server.Close()
	c := sslCheckerTrusting(f.roots, f.now)
	localOCSPClient(t, c, server)
	result := c.Check(t.Context(), Monitor{Type: TypeSSL, Target: startTLSServer(t, f.cert), Timeout: time.Second})
	if result.OK || result.Kind != FailTLS || calls.Load() != 2 {
		t.Fatalf("fallback did not detect revocation: result=%+v calls=%d", result, calls.Load())
	}
}
