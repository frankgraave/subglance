package checker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// certOpts describes a certificate to mint for a test.
type certOpts struct {
	notBefore time.Time
	notAfter  time.Time
	hosts     []string
}

// newTestCert mints a self-signed certificate and the CA pool that trusts it.
func newTestCert(t *testing.T, opts certOpts) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	if len(opts.hosts) == 0 {
		opts.hosts = []string{"localhost", "127.0.0.1"}
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: opts.hosts[0]},
		NotBefore:             opts.notBefore,
		NotAfter:              opts.notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range opts.hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, pool
}

// startTLSServer serves TLS on loopback with the given certificate.
func startTLSServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				// Force the handshake, then hang up.
				if tc, ok := conn.(*tls.Conn); ok {
					_ = tc.HandshakeContext(context.Background())
				}
				_ = conn.Close()
			}()
		}
	}()

	return ln.Addr().String()
}

// sslCheckerTrusting returns a checker that trusts the given pool and can see
// loopback, with a controllable clock.
func sslCheckerTrusting(pool *x509.CertPool, now time.Time) *SSLChecker {
	c := NewSSLChecker(NewGuard(true))
	c.rootsForTest = pool
	if !now.IsZero() {
		c.now = func() time.Time { return now }
	}
	return c
}

func TestSSLCheckValidCertificate(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})
	addr := startTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)
	res := c.Check(context.Background(), Monitor{
		Type:    TypeSSL,
		Target:  addr,
		Timeout: 5 * time.Second,
	})

	if !res.OK {
		t.Fatalf("expected success, got %s: %s", res.Kind, res.Error)
	}
	// The expiry date is the whole point of this check type; it must be
	// reported even when everything is fine.
	if res.CertExpiry.IsZero() {
		t.Error("expected the certificate expiry to be reported on success")
	}
	if got := res.CertExpiry.Sub(now).Hours() / 24; got < 89 || got > 91 {
		t.Errorf("expiry is %.0f days out, want ~90", got)
	}
}

// The reason this checker verifies by hand: an expired certificate must still
// yield its dates, not just a handshake error.
func TestSSLCheckExpiredCertificateStillReportsDate(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-10 * 24 * time.Hour)
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-100 * 24 * time.Hour),
		notAfter:  expiredAt,
	})
	addr := startTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)
	res := c.Check(context.Background(), Monitor{
		Type:    TypeSSL,
		Target:  addr,
		Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected failure for an expired certificate")
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q", res.Kind, FailTLS)
	}
	if res.CertExpiry.IsZero() {
		t.Fatal("expiry date lost; this is exactly what manual verification exists to prevent")
	}
	if !strings.Contains(res.Error, "expired") || !strings.Contains(res.Error, "10 days") {
		t.Errorf("error = %q, want it to say how long ago it expired", res.Error)
	}
}

func TestSSLCheckNotYetValid(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(48 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})
	addr := startTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected failure for a not-yet-valid certificate")
	}
	if !strings.Contains(res.Error, "not valid until") {
		t.Errorf("error = %q, want it to name the start date", res.Error)
	}
}

// A certificate that is still valid but close to expiry should fail, so it
// becomes an incident while there is time to renew.
func TestSSLCheckWarnsBeforeExpiry(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(5 * 24 * time.Hour),
	})
	addr := startTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)

	// Threshold below the remaining days: still fine.
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second, SSLWarnDays: 3,
	})
	if !res.OK {
		t.Errorf("5 days left with a 3-day threshold should pass, got: %s", res.Error)
	}

	// Threshold above the remaining days: warn.
	res = c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second, SSLWarnDays: 14,
	})
	if res.OK {
		t.Fatal("5 days left with a 14-day threshold should fail")
	}
	if res.Kind != FailCertExpiry {
		t.Errorf("kind = %q, want %q", res.Kind, FailCertExpiry)
	}
	if !strings.Contains(res.Error, "expires in") {
		t.Errorf("error = %q, want it to say how many days remain", res.Error)
	}
	// Still a valid certificate, so the expiry date must be present.
	if res.CertExpiry.IsZero() {
		t.Error("expected the expiry date on a warning")
	}
}

func TestSSLCheckHostnameMismatch(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
		hosts:     []string{"wrong.example.com"},
	})
	addr := startTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("expected failure when the certificate does not cover the host")
	}
	if !strings.Contains(res.Error, "hostname") {
		t.Errorf("error = %q, want it to mention the hostname mismatch", res.Error)
	}
}

func TestSSLCheckUntrustedIssuer(t *testing.T) {
	now := time.Now()
	cert, _ := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})
	addr := startTLSServer(t, cert)

	// Deliberately not trusting the certificate: system roots only.
	c := NewSSLChecker(NewGuard(true))
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
	})

	if res.OK {
		t.Fatal("a self-signed certificate should not verify against system roots")
	}
	if !strings.Contains(res.Error, "unknown authority") {
		t.Errorf("error = %q, want it to name the untrusted issuer", res.Error)
	}
}

func TestSSLCheckDefaultsToPort443(t *testing.T) {
	// No server needed: the check should attempt 443 and fail on connection,
	// not on a missing port.
	c := NewSSLChecker(NewGuard(true))
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: "198.51.100.1", Timeout: 1 * time.Second,
	})

	if res.OK {
		t.Fatal("expected a connection failure")
	}
	if res.Kind == FailInternal {
		t.Errorf("a missing port should default to 443, not error: %s", res.Error)
	}
}

func TestSSLCheckRespectsGuard(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})
	addr := startTLSServer(t, cert)

	c := NewSSLChecker(NewGuard(false)) // guard on
	c.rootsForTest = pool

	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
	})
	if res.OK {
		t.Fatal("guard should have refused a loopback target")
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30 seconds"},
		{5 * time.Minute, "5 minutes"},
		{3 * time.Hour, "3 hours"},
		{50 * time.Hour, "2 days"},
		{10 * 24 * time.Hour, "10 days"},
		{400 * 24 * time.Hour, "1 years 35 days"},
		{730 * 24 * time.Hour, "2 years"},
		{-10 * 24 * time.Hour, "10 days"}, // sign is the caller's business
	}

	for _, tc := range tests {
		if got := humanDuration(tc.d); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
