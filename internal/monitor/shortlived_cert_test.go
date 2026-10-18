package monitor

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// mintCert is a self-signed certificate for 127.0.0.1 valid from notBefore to
// notAfter.
func mintCert(t *testing.T, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(notBefore.UnixNano()),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// serveSwappableTLS serves whichever certificate current holds at the time of
// each handshake, so a test can renew it between two checks.
func serveSwappableTLS(t *testing.T, current *atomic.Pointer[tls.Certificate]) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return current.Load(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).HandshakeContext(context.Background())
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// A six-day certificate is expiring only in the last quarter of its life, and
// the notice it opens there closes on the first check that sees the renewed
// certificate. Through the real ssl checker, so the window the checker draws
// and the notice the runner keeps are tested together.
func TestAShortLivedCertificatesNoticeClosesOnRenewal(t *testing.T) {
	const lifetime = 160 * time.Hour
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	cert := func(age time.Duration) *tls.Certificate {
		c := mintCert(t, now.Add(-age), now.Add(lifetime-age))
		return &c
	}

	var current atomic.Pointer[tls.Certificate]
	current.Store(cert(80 * time.Hour))
	addr := serveSwappableTLS(t, &current)

	pool := x509.NewCertPool()
	db := testDB(t)
	rec := &alertRecorder{}
	r := New(Options{DB: db, Log: quietLogger(), Notify: rec.record, AllowPrivateTargets: true, Roots: pool})
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "short-lived", Type: "ssl", Target: addr,
		IntervalS: 86400, TimeoutS: 5, Retries: 2, SSLWarnDays: 14, Enabled: true, RepeatAfterS: 60,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	check := func(c *tls.Certificate) {
		t.Helper()
		pool.AddCert(c.Leaf)
		current.Store(c)
		if _, err := r.CheckNow(ctx, m); err != nil {
			t.Fatalf("CheckNow: %v", err)
		}
	}

	// At the point an ACME client renews, 80 hours left: nothing to say.
	check(current.Load())
	if got := r.Engine().Status(m.ID); got != state.StatusUp {
		t.Fatalf("80 hours left of 160: status %q, want up", got)
	}

	// 35 hours left and not renewed: the notice opens, once.
	check(cert(125 * time.Hour))
	if got := r.Engine().Status(m.ID); got != state.StatusExpiring {
		t.Fatalf("35 hours left of 160: status %q, want expiring", got)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("no notice open: %v", err)
	}
	if !inc.Notice || !strings.Contains(inc.LastError, "so the warning starts 40 hours before expiry") {
		t.Errorf("incident = %+v, want a notice that says why the warning started at 40 hours", inc)
	}

	// Renewed: a new six-day certificate, with far more than 40 hours left.
	check(cert(time.Hour))
	if got := rec.events(); len(got) != 2 || got[0] != state.EventIncidentConfirmed || got[1] != state.EventIncidentResolved {
		t.Fatalf("alerts = %v, want the notice and then its resolution", got)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err == nil {
		t.Error("the notice is still open after the certificate was renewed")
	}
	if got := r.Engine().Status(m.ID); got != state.StatusUp {
		t.Errorf("after renewal: status %q, want up", got)
	}
}
