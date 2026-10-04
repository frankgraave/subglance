package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin down that an HTTP check measures what a visitor meets: a
// fresh connection, with its own DNS lookup, TCP handshake and TLS handshake.
// A check that reused a warm connection stayed green after the server stopped
// accepting new ones, and kept reporting a certificate that had been renewed.

// connCounter counts the connections a test server accepted.
type connCounter struct{ n atomic.Int64 }

func (c *connCounter) track(_ net.Conn, s http.ConnState) {
	if s == http.StateNew {
		c.n.Add(1)
	}
}

// trustingChecker returns a checker that can see loopback and trusts pool.
func trustingChecker(pool *x509.CertPool) *HTTPChecker {
	c := testChecker()
	c.transport.TLSClientConfig = c.transport.TLSClientConfig.Clone()
	c.transport.TLSClientConfig.RootCAs = pool
	return c
}

func TestHTTPEachCheckOpensItsOwnConnection(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("all good"))
	})

	t.Run("http/1.1", func(t *testing.T) {
		var conns connCounter
		srv := httptest.NewUnstartedServer(ok)
		srv.Config.ConnState = conns.track
		srv.Start()
		defer srv.Close()

		c := testChecker()
		for i := range 2 {
			if res := c.Check(context.Background(), monitor(srv.URL)); !res.OK {
				t.Fatalf("check %d failed: %s", i+1, res.Error)
			}
		}
		if got := conns.n.Load(); got != 2 {
			t.Errorf("two checks opened %d connections, want 2", got)
		}
	})

	// HTTP/2 multiplexes onto one connection unless told otherwise, so it
	// is the protocol most likely to quietly bring reuse back.
	t.Run("h2", func(t *testing.T) {
		var conns connCounter
		var protos sync.Map
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			protos.Store(r.Proto, true)
			ok(w, r)
		}))
		srv.EnableHTTP2 = true
		srv.Config.ConnState = conns.track
		srv.StartTLS()
		defer srv.Close()

		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())
		c := trustingChecker(pool)
		for i := range 2 {
			if res := c.Check(context.Background(), monitor(srv.URL)); !res.OK {
				t.Fatalf("check %d failed: %s", i+1, res.Error)
			}
		}
		if _, h2 := protos.Load("HTTP/2.0"); !h2 {
			t.Fatal("the checks did not negotiate HTTP/2, so this case tests nothing")
		}
		if got := conns.n.Load(); got != 2 {
			t.Errorf("two checks opened %d connections, want 2", got)
		}
	})

	// A monitor with its own TLS floor runs on a cloned transport, which
	// must not bring back the reuse the shared one turned off. HTTP/2 again:
	// an HTTP/1.1 connection whose body is closed unread is not reused
	// either way, so it could not tell the two apart.
	t.Run("own TLS floor", func(t *testing.T) {
		var conns connCounter
		srv := httptest.NewUnstartedServer(ok)
		srv.EnableHTTP2 = true
		srv.Config.ConnState = conns.track
		srv.StartTLS()
		defer srv.Close()

		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())
		c := trustingChecker(pool)
		m := monitor(srv.URL)
		m.MinTLSVersion = tls.VersionTLS13
		for i := range 2 {
			if res := c.Check(context.Background(), m); !res.OK {
				t.Fatalf("check %d failed: %s", i+1, res.Error)
			}
		}
		if got := conns.n.Load(); got != 2 {
			t.Errorf("two checks opened %d connections, want 2", got)
		}
	})
}

func TestHTTPSeesARenewedCertificateOnTheNextCheck(t *testing.T) {
	now := time.Now()
	expiring, pool := newTestCert(t, certOpts{notBefore: now.Add(-85 * 24 * time.Hour), notAfter: now.Add(5 * 24 * time.Hour)})
	renewed, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	pool.AddCert(renewed.Leaf)

	var current atomic.Pointer[tls.Certificate]
	current.Store(&expiring)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// GetConfigForClient runs on every handshake, also for a client that
	// sends no SNI (the target is an IP address), which GetCertificate does
	// not when a default certificate is set.
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{*current.Load()},
			}, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	c := trustingChecker(pool)
	m := monitor(srv.URL)
	m.SSLWarnDays = 14

	before := c.Check(context.Background(), m)
	if before.OK || before.Kind != FailCertExpiry {
		t.Fatalf("before renewal: ok %v kind %q (%s), want a cert_expiry failure", before.OK, before.Kind, before.Error)
	}

	current.Store(&renewed)
	after := c.Check(context.Background(), m)
	if !after.OK {
		t.Fatalf("the check after renewal still failed: %s (kind %q)", after.Error, after.Kind)
	}
	if !after.CertExpiry.Equal(renewed.Leaf.NotAfter) {
		t.Errorf("reported expiry %v, want the renewed certificate's %v", after.CertExpiry, renewed.Leaf.NotAfter)
	}
}

func TestHTTPFailsOnceTheServerStopsAcceptingConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("all good"))
	}))
	defer srv.Close()

	c := testChecker()
	if res := c.Check(context.Background(), monitor(srv.URL)); !res.OK {
		t.Fatalf("first check failed: %s", res.Error)
	}

	// Closing the listener refuses every new connection while the server
	// goes on answering on any connection it had already accepted, which is
	// what a firewall change that only lets established traffic through
	// looks like from outside.
	if err := srv.Listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	res := c.Check(context.Background(), monitor(srv.URL))
	if res.OK {
		t.Fatal("the check stayed green on a server that accepts no new connections")
	}
	if res.Kind != FailConnection {
		t.Errorf("kind = %q (%s), want %q", res.Kind, res.Error, FailConnection)
	}
}
