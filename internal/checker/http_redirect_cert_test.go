package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// tlsHost is one HTTPS server with a certificate of its own, standing in for
// one host name in a redirect chain.
type tlsHost struct {
	srv  *httptest.Server
	leaf *x509.Certificate
}

// startTLSHost serves h over TLS with a certificate that expires in validFor.
// The certificate is added to pool, so one checker can trust every host in a
// chain.
func startTLSHost(t *testing.T, name string, validFor time.Duration, pool *x509.CertPool, h http.Handler) tlsHost {
	t.Helper()
	cert, _ := newTestCert(t, certOpts{
		notBefore: time.Now().Add(-time.Hour),
		notAfter:  time.Now().Add(validFor),
		hosts:     []string{name, "127.0.0.1"},
	})
	pool.AddCert(cert.Leaf)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return tlsHost{srv: srv, leaf: cert.Leaf}
}

// redirectTo answers every request with a 302 to target, the way a single
// sign-on proxy answers a visitor without a session.
func redirectTo(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	})
}

var loginPage = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("sign in"))
})

const (
	expiresSoon = 4 * 24 * time.Hour
	healthyFor  = 90 * 24 * time.Hour
)

func TestHTTPRedirectReportsTheMonitoredHostsCertificate(t *testing.T) {
	t.Run("an expiring site behind a healthy login host is expiring", func(t *testing.T) {
		pool := x509.NewCertPool()
		login := startTLSHost(t, "auth.test", healthyFor, pool, loginPage)
		site := startTLSHost(t, "app.test", expiresSoon, pool, redirectTo(login.srv.URL+"/login"))

		m := monitor(site.srv.URL)
		m.SSLWarnDays = 14
		res := trustingChecker(pool).Check(context.Background(), m)

		if !res.OK || !res.Expiring || res.Kind != FailCertExpiry {
			t.Fatalf("ok %v expiring %v kind %q (%s), want a pass marked expiring", res.OK, res.Expiring, res.Kind, res.Error)
		}
		if !res.CertExpiry.Equal(site.leaf.NotAfter) {
			t.Errorf("reported expiry %v, want the site's %v, not the login host's %v",
				res.CertExpiry, site.leaf.NotAfter, login.leaf.NotAfter)
		}
		if want := site.leaf.NotAfter.Format(time.DateOnly); !strings.Contains(res.Error, want) {
			t.Errorf("message %q does not name the site's expiry date %s", res.Error, want)
		}
	})

	t.Run("a healthy site behind an expiring login host is not expiring", func(t *testing.T) {
		pool := x509.NewCertPool()
		login := startTLSHost(t, "auth.test", expiresSoon, pool, loginPage)
		site := startTLSHost(t, "app.test", healthyFor, pool, redirectTo(login.srv.URL+"/login"))

		m := monitor(site.srv.URL)
		m.SSLWarnDays = 14
		res := trustingChecker(pool).Check(context.Background(), m)

		if !res.OK || res.Expiring || res.Kind != "" {
			t.Fatalf("ok %v expiring %v kind %q (%s), want a plain pass: the login host is not the one watched here",
				res.OK, res.Expiring, res.Kind, res.Error)
		}
		if !res.CertExpiry.Equal(site.leaf.NotAfter) {
			t.Errorf("reported expiry %v, want the site's %v, not the login host's %v",
				res.CertExpiry, site.leaf.NotAfter, login.leaf.NotAfter)
		}
	})

	t.Run("without a redirect the server's own certificate is reported", func(t *testing.T) {
		pool := x509.NewCertPool()
		site := startTLSHost(t, "app.test", expiresSoon, pool, loginPage)

		m := monitor(site.srv.URL)
		m.SSLWarnDays = 14
		res := trustingChecker(pool).Check(context.Background(), m)

		if !res.OK || !res.Expiring {
			t.Fatalf("ok %v expiring %v (%s), want a pass marked expiring", res.OK, res.Expiring, res.Error)
		}
		if !res.CertExpiry.Equal(site.leaf.NotAfter) {
			t.Errorf("reported expiry %v, want %v", res.CertExpiry, site.leaf.NotAfter)
		}
	})

	t.Run("an http target upgraded to https reports the upgraded host", func(t *testing.T) {
		pool := x509.NewCertPool()
		site := startTLSHost(t, "app.test", expiresSoon, pool, loginPage)
		plain := httptest.NewServer(redirectTo(site.srv.URL + "/"))
		t.Cleanup(plain.Close)

		m := monitor(plain.URL)
		m.SSLWarnDays = 14
		res := trustingChecker(pool).Check(context.Background(), m)

		if !res.OK || !res.Expiring {
			t.Fatalf("ok %v expiring %v (%s), want a pass marked expiring: the visitor lands on that certificate",
				res.OK, res.Expiring, res.Error)
		}
		if !res.CertExpiry.Equal(site.leaf.NotAfter) {
			t.Errorf("reported expiry %v, want the upgraded host's %v", res.CertExpiry, site.leaf.NotAfter)
		}
	})

	t.Run("an http target reports the first hop that spoke TLS", func(t *testing.T) {
		// http://app -> https://app -> https://auth: the visitor is upgraded
		// to app, so app's certificate is the one this monitor is about.
		pool := x509.NewCertPool()
		login := startTLSHost(t, "auth.test", healthyFor, pool, loginPage)
		site := startTLSHost(t, "app.test", expiresSoon, pool, redirectTo(login.srv.URL+"/login"))
		plain := httptest.NewServer(redirectTo(site.srv.URL + "/"))
		t.Cleanup(plain.Close)

		m := monitor(plain.URL)
		m.SSLWarnDays = 14
		res := trustingChecker(pool).Check(context.Background(), m)

		if !res.OK || !res.Expiring {
			t.Fatalf("ok %v expiring %v (%s), want a pass marked expiring", res.OK, res.Expiring, res.Error)
		}
		if !res.CertExpiry.Equal(site.leaf.NotAfter) {
			t.Errorf("reported expiry %v, want the first TLS hop's %v, not the login host's %v",
				res.CertExpiry, site.leaf.NotAfter, login.leaf.NotAfter)
		}
	})

	t.Run("an http target that never reaches TLS reports no certificate", func(t *testing.T) {
		plain := httptest.NewServer(loginPage)
		t.Cleanup(plain.Close)

		m := monitor(plain.URL)
		m.SSLWarnDays = 14
		res := testChecker().Check(context.Background(), m)

		if !res.OK || res.Expiring || !res.CertExpiry.IsZero() {
			t.Fatalf("ok %v expiring %v expiry %v (%s), want a plain pass with no certificate",
				res.OK, res.Expiring, res.CertExpiry, res.Error)
		}
	})
}
