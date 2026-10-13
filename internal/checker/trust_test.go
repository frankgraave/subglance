package checker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// trustedOn is the day the tests' pins were set, as a failure words it.
var trustedOn = time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)

// noRoots is a trust store that vouches for nothing, so the tests do not
// depend on the host's.
func noRoots() *x509.CertPool { return x509.NewCertPool() }

func TestCertFingerprintIsTheFormEveryoneElsePrints(t *testing.T) {
	cert, _ := newTestCert(t, certOpts{notBefore: time.Now().Add(-time.Hour), notAfter: time.Now().Add(time.Hour)})
	fp := CertFingerprint(cert.Leaf)
	if len(fp) != 95 || strings.Count(fp, ":") != 31 || strings.ToUpper(fp) != fp {
		t.Fatalf("fingerprint %q is not 32 upper-case hex pairs joined by colons", fp)
	}

	bare := strings.ToLower(strings.ReplaceAll(fp, ":", ""))
	for _, in := range []string{fp, bare, " " + strings.ReplaceAll(fp, ":", " ") + " ", strings.ToLower(fp)} {
		got, ok := ParseCertFingerprint(in)
		if !ok || got != fp {
			t.Errorf("ParseCertFingerprint(%q) = %q, %v; want %q", in, got, ok, fp)
		}
	}
	for _, in := range []string{"", bare[:62], bare + "00", "zz" + bare[2:], "sha256:" + bare} {
		if got, ok := ParseCertFingerprint(in); ok {
			t.Errorf("ParseCertFingerprint(%q) = %q, accepted; want refused", in, got)
		}
	}
}

// The ssl checker's half of the acceptance: refused and offered, then passing
// once pinned, with the expiry still read.
func TestSSLOffersAnUntrustedCertificateAndPassesOncePinned(t *testing.T) {
	now := time.Now()
	cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	addr := startTLSServer(t, cert)
	c := sslCheckerTrusting(noRoots(), now)
	m := Monitor{Type: TypeSSL, Target: addr, Timeout: 5 * time.Second, SSLWarnDays: 14}

	res := c.Check(context.Background(), m)
	if res.OK || res.Kind != FailTLS {
		t.Fatalf("untrusted certificate: ok=%v kind=%q, want a tls failure", res.OK, res.Kind)
	}
	if res.Certificate == nil {
		t.Fatalf("no certificate offered for a self-signed one (error %q)", res.Error)
	}
	want := CertFingerprint(cert.Leaf)
	if res.Certificate.SHA256 != want || !strings.Contains(res.Certificate.Subject, "localhost") ||
		!res.Certificate.NotAfter.Equal(cert.Leaf.NotAfter) {
		t.Errorf("offered %+v, want the server's certificate %s", res.Certificate, want)
	}

	m.TrustedCertSHA256, m.TrustedCertAt = want, trustedOn
	res = c.Check(context.Background(), m)
	if !res.OK || res.Kind != FailNone || res.Certificate != nil {
		t.Fatalf("pinned: ok=%v kind=%q error=%q certificate=%v, want a plain pass", res.OK, res.Kind, res.Error, res.Certificate)
	}
	if !res.CertExpiry.Equal(cert.Leaf.NotAfter) {
		t.Errorf("cert expiry = %v, want %v", res.CertExpiry, cert.Leaf.NotAfter)
	}
}

func TestSSLPinnedCertificateThatChangedFailsWithItsOwnSentence(t *testing.T) {
	now := time.Now()
	trusted, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	presented, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(90 * 24 * time.Hour)})
	addr := startTLSServer(t, presented)

	res := sslCheckerTrusting(noRoots(), now).Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
		TrustedCertSHA256: CertFingerprint(trusted.Leaf), TrustedCertAt: trustedOn,
	})
	if res.OK || res.Kind != FailTLS {
		t.Fatalf("ok=%v kind=%q, want a tls failure", res.OK, res.Kind)
	}
	next := CertFingerprint(presented.Leaf)
	if !strings.Contains(res.Error, "certificate changed since it was trusted on 2026-10-11") || !strings.Contains(res.Error, next) {
		t.Errorf("error = %q, want it to say the certificate changed since 2026-10-11 and name %s", res.Error, next)
	}
	if res.Certificate == nil || res.Certificate.SHA256 != next {
		t.Errorf("offered %+v, want the new certificate so it can be trusted in turn", res.Certificate)
	}
}

func TestSSLPinnedCertificateStillWarnsAndExpires(t *testing.T) {
	now := time.Now()
	t.Run("expiring", func(t *testing.T) {
		cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(5 * 24 * time.Hour)})
		res := sslCheckerTrusting(noRoots(), now).Check(context.Background(), Monitor{
			Type: TypeSSL, Target: startTLSServer(t, cert), Timeout: 5 * time.Second, SSLWarnDays: 14,
			TrustedCertSHA256: CertFingerprint(cert.Leaf), TrustedCertAt: trustedOn,
		})
		if !res.OK || !res.Expiring || res.Kind != FailCertExpiry {
			t.Errorf("ok=%v expiring=%v kind=%q error=%q, want a pass marked expiring", res.OK, res.Expiring, res.Kind, res.Error)
		}
	})
	t.Run("expired", func(t *testing.T) {
		cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-10 * 24 * time.Hour), notAfter: now.Add(-24 * time.Hour)})
		res := sslCheckerTrusting(noRoots(), now).Check(context.Background(), Monitor{
			Type: TypeSSL, Target: startTLSServer(t, cert), Timeout: 5 * time.Second,
			TrustedCertSHA256: CertFingerprint(cert.Leaf), TrustedCertAt: trustedOn,
		})
		if res.OK || res.Kind != FailTLS || !strings.Contains(res.Error, "certificate expired") {
			t.Errorf("ok=%v kind=%q error=%q, want an expired failure", res.OK, res.Kind, res.Error)
		}
		if res.Certificate != nil {
			t.Errorf("offered %+v for an expired certificate, which a pin does not fix", res.Certificate)
		}
	})
}

// A pin would turn these green while every other client still fails, so
// nothing is offered for them.
func TestSSLOffersNothingAPinWouldOnlyHide(t *testing.T) {
	now := time.Now()
	t.Run("trusted certificate for another host", func(t *testing.T) {
		cert, pool := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(24 * time.Hour), hosts: []string{"other.test"}})
		res := sslCheckerTrusting(pool, now).Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, cert), Timeout: 5 * time.Second})
		if res.OK || !strings.Contains(res.Error, "does not match hostname") {
			t.Fatalf("ok=%v error=%q, want a host name mismatch", res.OK, res.Error)
		}
		if res.Certificate != nil {
			t.Errorf("offered %+v for a trusted certificate on the wrong host", res.Certificate)
		}
	})
	t.Run("expired and untrusted", func(t *testing.T) {
		cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-48 * time.Hour), notAfter: now.Add(-24 * time.Hour)})
		res := sslCheckerTrusting(noRoots(), now).Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, cert), Timeout: 5 * time.Second})
		if res.OK || res.Certificate != nil {
			t.Errorf("ok=%v certificate=%+v, want a failure offering nothing", res.OK, res.Certificate)
		}
	})
}

func TestOfferPinTellsAnUnknownCAFromAMissingIntermediate(t *testing.T) {
	leaf, intermediate, _, roots := newChain(t, chainOpts{aia: "http://aia.test/intermediate.crt"})
	now := time.Now()
	if got := offerPin([]*x509.Certificate{leaf}, roots, now); got != nil {
		t.Errorf("offered %+v for a chain missing its intermediate", got)
	}
	if got := offerPin([]*x509.Certificate{leaf, intermediate}, noRoots(), now); got == nil || got.SHA256 != CertFingerprint(leaf) {
		t.Errorf("offered %+v for a complete chain from an unknown CA, want the leaf", got)
	}
	if got := offerPin([]*x509.Certificate{leaf, intermediate}, roots, now); got != nil {
		t.Errorf("offered %+v for a chain the trust store vouches for", got)
	}
}

// httpsPinned serves 200 over TLS with the given certificate, or with
// httptest's own when cert is nil.
func httpsServer(t *testing.T, cert *tls.Certificate, h http.Handler) *httptest.Server {
	t.Helper()
	if h == nil {
		h = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	srv := httptest.NewUnstartedServer(h)
	if cert != nil {
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func httpCheckerTrusting(roots *x509.CertPool) *HTTPChecker {
	return NewHTTPChecker(HTTPOptions{Guard: NewGuard(true), Roots: roots})
}

// The http checker's half of the acceptance.
func TestHTTPOffersAnUntrustedCertificateAndPassesOncePinned(t *testing.T) {
	srv := httpsServer(t, nil, nil)
	c := httpCheckerTrusting(noRoots())
	m := monitor(srv.URL)

	res := c.Check(context.Background(), m)
	if res.OK || res.Kind != FailTLS {
		t.Fatalf("untrusted certificate: ok=%v kind=%q error=%q, want a tls failure", res.OK, res.Kind, res.Error)
	}
	want := CertFingerprint(srv.Certificate())
	if res.Certificate == nil || res.Certificate.SHA256 != want {
		t.Fatalf("offered %+v, want the server's certificate %s", res.Certificate, want)
	}

	m.TrustedCertSHA256, m.TrustedCertAt = want, trustedOn
	res = c.Check(context.Background(), m)
	if !res.OK || res.StatusCode != http.StatusOK || res.Certificate != nil {
		t.Fatalf("pinned: ok=%v status=%d error=%q, want a pass", res.OK, res.StatusCode, res.Error)
	}
	if !res.CertExpiry.Equal(srv.Certificate().NotAfter) {
		t.Errorf("cert expiry = %v, want %v", res.CertExpiry, srv.Certificate().NotAfter)
	}
}

func TestHTTPPinnedCertificateThatChangedFailsWithItsOwnSentence(t *testing.T) {
	now := time.Now()
	other, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(24 * time.Hour)})
	srv := httpsServer(t, nil, nil)
	m := monitor(srv.URL)
	m.TrustedCertSHA256, m.TrustedCertAt = CertFingerprint(other.Leaf), trustedOn

	res := httpCheckerTrusting(noRoots()).Check(context.Background(), m)
	next := CertFingerprint(srv.Certificate())
	if res.OK || res.Kind != FailTLS {
		t.Fatalf("ok=%v kind=%q error=%q, want a tls failure", res.OK, res.Kind, res.Error)
	}
	if !strings.Contains(res.Error, "certificate changed since it was trusted on 2026-10-11") || !strings.Contains(res.Error, next) {
		t.Errorf("error = %q, want it to say the certificate changed since 2026-10-11 and name %s", res.Error, next)
	}
	if res.Certificate == nil || res.Certificate.SHA256 != next {
		t.Errorf("offered %+v, want the new certificate", res.Certificate)
	}
}

func TestHTTPPinnedCertificateStillWarnsBeforeExpiry(t *testing.T) {
	now := time.Now()
	cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-time.Hour), notAfter: now.Add(5 * 24 * time.Hour)})
	srv := httpsServer(t, &cert, nil)
	m := monitor(srv.URL)
	m.SSLWarnDays = 14
	m.TrustedCertSHA256, m.TrustedCertAt = CertFingerprint(cert.Leaf), trustedOn

	res := httpCheckerTrusting(noRoots()).Check(context.Background(), m)
	if !res.OK || !res.Expiring || res.Kind != FailCertExpiry {
		t.Errorf("ok=%v expiring=%v kind=%q error=%q, want a pass marked expiring", res.OK, res.Expiring, res.Kind, res.Error)
	}
}

// The handshake is the only place an http check reads a pinned certificate's
// dates when the expiry warning is off, so the pin must not waive them.
func TestHTTPPinnedCertificateThatExpiredFails(t *testing.T) {
	now := time.Now()
	cert, _ := newTestCert(t, certOpts{notBefore: now.Add(-10 * 24 * time.Hour), notAfter: now.Add(-24 * time.Hour)})
	srv := httpsServer(t, &cert, nil)
	m := monitor(srv.URL)
	m.SSLWarnDays = 0
	m.TrustedCertSHA256, m.TrustedCertAt = CertFingerprint(cert.Leaf), trustedOn

	res := httpCheckerTrusting(noRoots()).Check(context.Background(), m)
	if res.OK || res.Kind != FailTLS || !strings.Contains(res.Error, "certificate expired") {
		t.Errorf("ok=%v kind=%q error=%q, want an expired failure", res.OK, res.Kind, res.Error)
	}
	if res.Certificate != nil {
		t.Errorf("offered %+v for an expired certificate", res.Certificate)
	}
}

// The pin speaks for the monitored origin. httptest gives every TLS server the
// same certificate, so the second server below presents exactly the pinned
// one: if the pin followed the redirect, the check would pass.
func TestHTTPPinDoesNotFollowARedirectToAnotherOrigin(t *testing.T) {
	elsewhere := httpsServer(t, nil, nil)
	pinned := httpsServer(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/here" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/stay" {
			http.Redirect(w, r, "/here", http.StatusFound)
			return
		}
		http.Redirect(w, r, elsewhere.URL+"/", http.StatusFound)
	}))
	fp := CertFingerprint(pinned.Certificate())
	if fp != CertFingerprint(elsewhere.Certificate()) {
		t.Fatal("the test's premise: both httptest servers present the same certificate")
	}

	check := func(roots *x509.CertPool, path string) Result {
		m := monitor(pinned.URL + path)
		m.FollowRedirects = true
		m.TrustedCertSHA256, m.TrustedCertAt = fp, trustedOn
		return httpCheckerTrusting(roots).Check(context.Background(), m)
	}

	if res := check(noRoots(), "/stay"); !res.OK {
		t.Errorf("redirect within the pinned origin: %s %q, want a pass", res.Kind, res.Error)
	}
	res := check(noRoots(), "/away")
	if res.OK || res.Kind != FailTLS || !strings.Contains(res.Error, "unknown authority") {
		t.Errorf("redirect to another origin: ok=%v kind=%q error=%q, want it verified against the trust store", res.OK, res.Kind, res.Error)
	}
	if res.Certificate != nil {
		t.Errorf("offered %+v for another origin's certificate", res.Certificate)
	}

	trusting := x509.NewCertPool()
	trusting.AddCert(elsewhere.Certificate())
	if res := check(trusting, "/away"); !res.OK {
		t.Errorf("redirect to an origin the trust store vouches for: %s %q, want a pass", res.Kind, res.Error)
	}
}

// mintCA returns a CA certificate and its key; mintLeaf a certificate for
// 127.0.0.1 signed by it.
func mintCA(t *testing.T, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key
}

func mintLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func writePEM(t *testing.T, path string, blocks ...*pem.Block) {
	t.Helper()
	var b []byte
	for _, block := range blocks {
		b = append(b, pem.EncodeToMemory(block)...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Both checkers trust a certificate from an extra CA without a pin.
func TestCheckersTrustAnExtraCA(t *testing.T) {
	ca, caKey := mintCA(t, "Home Lab CA")
	leaf := mintLeaf(t, ca, caKey)
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	srv := httpsServer(t, &leaf, nil)
	if res := httpCheckerTrusting(roots).Check(context.Background(), monitor(srv.URL)); !res.OK {
		t.Errorf("http: %s %q, want a pass", res.Kind, res.Error)
	}
	if res := httpCheckerTrusting(noRoots()).Check(context.Background(), monitor(srv.URL)); res.OK {
		t.Error("http: passed without the CA; the test proves nothing")
	}

	c := NewSSLCheckerWithRoots(NewGuard(true), roots)
	if res := c.Check(context.Background(), Monitor{Type: TypeSSL, Target: startTLSServer(t, leaf), Timeout: 5 * time.Second}); !res.OK {
		t.Errorf("ssl: %s %q, want a pass", res.Kind, res.Error)
	}
}

func TestExtraRootsRefusesAFileWithoutACertificate(t *testing.T) {
	dir := t.TempDir()
	ca, _ := mintCA(t, "One")
	two, _ := mintCA(t, "Two")
	key := &pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not looked at")}

	bundle := filepath.Join(dir, "bundle.pem")
	writePEM(t, bundle, &pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}, key, &pem.Block{Type: "CERTIFICATE", Bytes: two.Raw})
	if _, n, err := ExtraRoots(bundle); err != nil || n != 2 {
		t.Errorf("bundle of two CAs and a key: n=%d err=%v, want 2 and no error", n, err)
	}

	keyOnly := filepath.Join(dir, "key.pem")
	writePEM(t, keyOnly, key)
	broken := filepath.Join(dir, "broken.pem")
	writePEM(t, broken, &pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}, &pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})

	for name, path := range map[string]string{
		"missing":          filepath.Join(dir, "nope.pem"),
		"no certificate":   keyOnly,
		"unparseable cert": broken,
	} {
		if _, _, err := ExtraRoots(path); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// ExtraRoots adds to the system trust store rather than replacing it, so a
// public certificate keeps passing. Proven in a child process whose system
// store is a file this test wrote: SSL_CERT_FILE is read once per process.
func TestExtraRootsKeepsTheSystemTrustStore(t *testing.T) {
	if dir := os.Getenv("SUBGLANCE_TEST_EXTRA_ROOTS_DIR"); dir != "" {
		pool, _, err := ExtraRoots(filepath.Join(dir, "private-ca.pem"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"public-leaf.der", "private-leaf.der"} {
			der, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			leaf, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE sets the system trust store on Linux only")
	}

	dir := t.TempDir()
	public, publicKey := mintCA(t, "Public CA")
	private, privateKey := mintCA(t, "Private CA")
	writePEM(t, filepath.Join(dir, "public-ca.pem"), &pem.Block{Type: "CERTIFICATE", Bytes: public.Raw})
	writePEM(t, filepath.Join(dir, "private-ca.pem"), &pem.Block{Type: "CERTIFICATE", Bytes: private.Raw})
	for name, leaf := range map[string]tls.Certificate{
		"public-leaf.der":  mintLeaf(t, public, publicKey),
		"private-leaf.der": mintLeaf(t, private, privateKey),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), leaf.Certificate[0], 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestExtraRootsKeepsTheSystemTrustStore$", "-test.count=1") // #nosec G204,G702 -- re-runs this test binary
	cmd.Env = append(os.Environ(),
		"SUBGLANCE_TEST_EXTRA_ROOTS_DIR="+dir,
		"SSL_CERT_FILE="+filepath.Join(dir, "public-ca.pem"),
		"SSL_CERT_DIR="+t.TempDir(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
