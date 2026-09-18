package checker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests drive the TLS diagnosis without touching the network. Findings 1
// and 2 need a real handshake to produce a real error, so they get a listener
// on loopback configured to offer only the terms under test; the rest is
// synthesised, because the point being tested is the wording and the
// FailureKind, not crypto/tls's behaviour.

// chainOpts describes a small CA hierarchy to mint for a test.
type chainOpts struct {
	// aia is the Authority Information Access URL placed on the leaf, which
	// is what tells an incomplete chain apart from an untrusted CA.
	aia string
}

// newChain mints root -> intermediate -> leaf and returns them leaf-first,
// plus a pool holding the root alone.
//
// It exists because the incomplete-chain diagnosis is about which certificates
// the server sends, so a test has to be able to hand over a genuine leaf whose
// issuer is absent rather than a hand-built struct.
func newChain(t *testing.T, opts chainOpts) (leaf, intermediate, root *x509.Certificate, roots *x509.CertPool) {
	t.Helper()

	mint := func(tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		signer, signerKey := parent, parentKey
		if signer == nil {
			signer, signerKey = tmpl, key // self-signed
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parse certificate: %v", err)
		}
		return cert, key
	}

	now := time.Now()
	root, rootKey := mint(&x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "SubGlance Test Root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// The root carries an AIA URL too. Real roots do — a cross-signed
		// root names where its other signature can be fetched — and without
		// it the untrusted-root case would pass for the wrong reason: the
		// absence of an AIA, rather than the chain closing on a self-issued
		// certificate, would be what stops it reading as incomplete.
		IssuingCertificateURL: []string{"http://aia.test/root.crt"},
	}, nil, nil)

	intermediate, interKey := mint(&x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "SubGlance Test Intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// Real intermediates name where their issuer is published, and that
		// matters here: a chain of leaf and intermediate with an untrusted
		// root omitted is the case that must NOT read as a missing
		// intermediate. Without an AIA it would pass for the wrong reason.
		IssuingCertificateURL: []string{"http://aia.test/root.crt"},
	}, root, rootKey)

	leafTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(3),
		Subject:               pkix.Name{CommonName: "leaf.test"},
		DNSNames:              []string{"leaf.test"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if opts.aia != "" {
		leafTmpl.IssuingCertificateURL = []string{opts.aia}
	}
	leaf, _ = mint(leafTmpl, intermediate, interKey)

	roots = x509.NewCertPool()
	roots.AddCert(root)
	return leaf, intermediate, root, roots
}

// verifyErrFor runs the verification a checker would run and hands back the
// error, so a test asserts on the real x509 error rather than on a guess at it.
func verifyErrFor(t *testing.T, chain []*x509.Certificate, roots *x509.CertPool) error {
	t.Helper()
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{
		DNSName:       "leaf.test",
		Roots:         roots,
		Intermediates: inter,
	})
	if err == nil {
		t.Fatal("expected verification to fail")
	}
	return err
}

// Finding 4. A server that forgets its intermediate and a CA that is genuinely
// not trusted produce the identical x509 error and have opposite fixes; the
// message has to tell them apart or it sends the reader to the wrong place.
func TestIncompleteChainIsNotBlamedOnTheCA(t *testing.T) {
	leaf, _, _, roots := newChain(t, chainOpts{aia: "http://aia.test/intermediate.crt"})

	chain := []*x509.Certificate{leaf} // the intermediate is what the server forgot
	err := verifyErrFor(t, chain, roots)

	var authErr x509.UnknownAuthorityError
	if !errors.As(err, &authErr) {
		t.Fatalf("precondition: want UnknownAuthorityError, got %T %v", err, err)
	}

	got := describeCertProblem("leaf.test", chain, err, time.Now())

	if !strings.Contains(got, "incomplete certificate chain") {
		t.Errorf("message = %q, want it to name the incomplete chain", got)
	}
	if !strings.Contains(got, "intermediate") {
		t.Errorf("message = %q, want it to say the intermediate is missing", got)
	}
	if !strings.Contains(got, "SubGlance Test Intermediate") {
		t.Errorf("message = %q, want it to name the issuer the server did not send", got)
	}
	if !strings.Contains(got, "http://aia.test/intermediate.crt") {
		t.Errorf("message = %q, want it to quote the AIA URL that would close the chain", got)
	}
	// The old message. Keeping it would send the reader to their CA for a
	// problem that is one line of their own server configuration.
	if strings.Contains(got, "unknown authority") {
		t.Errorf("message = %q, still blames the CA for the server's missing intermediate", got)
	}
}

// The other half of finding 4: a chain that closes on a root the server sent
// itself is not incomplete, and must keep reading as an untrusted CA.
func TestCompleteChainWithUntrustedRootStillSaysUnknownAuthority(t *testing.T) {
	leaf, intermediate, root, _ := newChain(t, chainOpts{aia: "http://aia.test/intermediate.crt"})

	// Everything sent, nothing trusted — badssl's untrusted-root.
	chain := []*x509.Certificate{leaf, intermediate, root}
	err := verifyErrFor(t, chain, x509.NewCertPool())

	got := describeCertProblem("leaf.test", chain, err, time.Now())
	if !strings.Contains(got, "unknown authority") {
		t.Errorf("message = %q, want it to name the untrusted authority", got)
	}
	if strings.Contains(got, "incomplete") {
		t.Errorf("message = %q, calls a complete chain incomplete", got)
	}
}

// A leaf with no AIA gives us nothing to assert with, so we must not guess.
func TestChainWithoutAIAIsNotCalledIncomplete(t *testing.T) {
	leaf, _, _, roots := newChain(t, chainOpts{}) // no AIA

	chain := []*x509.Certificate{leaf}
	err := verifyErrFor(t, chain, roots)

	got := describeCertProblem("leaf.test", chain, err, time.Now())
	if strings.Contains(got, "incomplete") {
		t.Errorf("message = %q, claims an incomplete chain with no AIA to back it", got)
	}
	if !strings.Contains(got, "unknown authority") {
		t.Errorf("message = %q, want the unknown-authority wording", got)
	}
}

// Finding 5, in the small: expiry is stated from the certificate and in the
// SSL checker's register, whatever error the library happened to raise.
func TestDescribeCertProblemPrefersExpiryOverTheLibrarysError(t *testing.T) {
	leaf, _, _, roots := newChain(t, chainOpts{})
	chain := []*x509.Certificate{leaf}
	err := verifyErrFor(t, chain, roots) // unknown authority

	// Ten days past the leaf's NotAfter.
	now := leaf.NotAfter.Add(10 * 24 * time.Hour)
	got := describeCertProblem("leaf.test", chain, err, now)

	if !strings.Contains(got, "certificate expired 10 days ago") {
		t.Errorf("message = %q, want the readable expiry wording", got)
	}
	if !strings.Contains(got, leaf.NotAfter.Format(time.DateOnly)) {
		t.Errorf("message = %q, want the expiry date", got)
	}
	// The raw x509 comparison is what the HTTP checker used to print.
	if strings.Contains(got, "is after") || strings.Contains(got, "x509:") {
		t.Errorf("message = %q, leaked the raw x509 string into the alert", got)
	}
}

func TestTLSVersionNames(t *testing.T) {
	cases := map[uint16]string{
		tls.VersionTLS10: "TLS 1.0",
		tls.VersionTLS11: "TLS 1.1",
		tls.VersionTLS12: "TLS 1.2",
		tls.VersionTLS13: "TLS 1.3",
	}
	for v, want := range cases {
		if got := tlsVersionName(v); got != want {
			t.Errorf("tlsVersionName(0x%04x) = %q, want %q", v, got, want)
		}
	}
	// Finding 2's symptom: "protocol version 301" is what a user was shown.
	if got := tlsVersionName(0x0301); got != "TLS 1.0" {
		t.Errorf("tlsVersionName(0x0301) = %q, want the name a person writes", got)
	}
}

// Finding 2, on the local error: our own stack refuses a server that answered
// below the floor, and the version must be named rather than printed in hex.
func TestUnsupportedProtocolVersionIsATLSFaultNamingTheVersion(t *testing.T) {
	err := errors.New("tls: server selected unsupported protocol version 301")

	res, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{})
	if !handled {
		t.Fatal("an unsupported protocol version was not recognised as a TLS fault")
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q — the server answered, so this is not a network fault", res.Kind, FailTLS)
	}
	if !strings.Contains(res.Error, "TLS 1.0") {
		t.Errorf("error = %q, want the version named", res.Error)
	}
	if !strings.Contains(res.Error, "TLS 1.2") {
		t.Errorf("error = %q, want the monitor's own minimum stated", res.Error)
	}
	if strings.Contains(res.Error, "301") {
		t.Errorf("error = %q, still shows the wire version to the reader", res.Error)
	}
	if strings.Contains(res.Error, "connection failed") {
		t.Errorf("error = %q, still points at the network", res.Error)
	}
}

// The monitor's own floor is what the message quotes, not the package default.
func TestUnsupportedProtocolVersionQuotesTheMonitorsMinimum(t *testing.T) {
	err := errors.New("tls: server selected unsupported protocol version 303")

	res, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{MinTLSVersion: tls.VersionTLS13})
	if !handled {
		t.Fatal("not recognised as a TLS fault")
	}
	if !strings.Contains(res.Error, "minimum of TLS 1.3") {
		t.Errorf("error = %q, want the monitor's configured minimum", res.Error)
	}
}

// Finding 1, on the alert: a peer that refuses every suite we offered is a TLS
// fault, not a connection fault, and the message must say what was refused.
func TestHandshakeFailureAlertIsATLSFaultAboutCiphers(t *testing.T) {
	// The shape crypto/tls produces for alert 40, as observed against
	// rc4.badssl.com: an OpError whose Op is "remote error".
	err := &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}

	res, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{})
	if !handled {
		t.Fatal("a handshake_failure alert was not recognised as a TLS fault")
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q — the TCP connection succeeded", res.Kind, FailTLS)
	}
	if !strings.Contains(res.Error, "cipher") {
		t.Errorf("error = %q, want it to name the cipher negotiation", res.Error)
	}
	if strings.Contains(res.Error, "connection failed") {
		t.Errorf("error = %q, still sends the reader to their firewall", res.Error)
	}
}

// A local alert — one we sent — is not this. It must fall through so the
// transport classifier keeps its say.
func TestLocalErrorIsNotTreatedAsACipherRejection(t *testing.T) {
	err := &net.OpError{Op: "local error", Err: errors.New("tls: bad record MAC")}
	if _, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{}); handled {
		t.Error("an error we raised ourselves was reported as the peer refusing our ciphers")
	}
}

// An ordinary connection failure must be left alone, or fixing finding 1 would
// relabel every refused connection as a TLS problem.
func TestConnectionRefusedIsNotClaimedByTheTLSClassifier(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}
	if _, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{}); handled {
		t.Error("a refused connection was reported as a TLS negotiation failure")
	}
}

// startCipherlessTLSServer serves TLS on loopback offering only cipher suites
// that cannot be used with the ECDSA certificate it holds, so a well-behaved
// client gets a genuine handshake_failure alert. This is badssl's rc4 and 3des
// ports reproduced in-process: no network, same error.
func startCipherlessTLSServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12, // TLS 1.3 ignores CipherSuites
		// RSA key exchange only, against an ECDSA certificate: nothing in
		// common, which is the position a server still offering RC4 or 3DES is
		// permanently in with any modern client.
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA},
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
				if tc, ok := conn.(*tls.Conn); ok {
					_ = tc.HandshakeContext(context.Background())
				}
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// Finding 1, end to end through the SSL checker.
func TestSSLCheckReportsCipherRejectionAsTLSNotConnection(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})
	addr := startCipherlessTLSServer(t, cert)

	c := sslCheckerTrusting(pool, now)
	res := c.Check(context.Background(), Monitor{Type: TypeSSL, Target: addr, Timeout: 5 * time.Second})

	if res.OK {
		t.Fatal("expected failure when no cipher suite is in common")
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q (was %q, which sends the reader to their firewall): %s",
			res.Kind, FailTLS, FailConnection, res.Error)
	}
	if !strings.Contains(res.Error, "cipher") {
		t.Errorf("error = %q, want it to name the cipher negotiation", res.Error)
	}
}

// Finding 2, end to end: the monitor's floor is what the handshake uses, so a
// legacy endpoint can be monitored at all.
func TestSSLCheckHonoursTheMonitorsMinimumTLSVersion(t *testing.T) {
	now := time.Now()
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-24 * time.Hour),
		notAfter:  now.Add(90 * 24 * time.Hour),
	})

	// A server that will not go above TLS 1.2 — stand-in for the appliance in
	// the cupboard. Asking for TLS 1.3 must fail; the default must not.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
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
				if tc, ok := conn.(*tls.Conn); ok {
					_ = tc.HandshakeContext(context.Background())
				}
				_ = conn.Close()
			}()
		}
	}()
	addr := ln.Addr().String()

	c := sslCheckerTrusting(pool, now)

	if res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
	}); !res.OK {
		t.Fatalf("default floor should reach a TLS 1.2 server, got %s: %s", res.Kind, res.Error)
	}

	// Raised floor as a deliberate assertion about the server.
	res := c.Check(context.Background(), Monitor{
		Type: TypeSSL, Target: addr, Timeout: 5 * time.Second,
		MinTLSVersion: tls.VersionTLS13,
	})
	if res.OK {
		t.Fatal("a monitor requiring TLS 1.3 passed against a TLS 1.2-only server")
	}
	if res.Kind != FailTLS {
		t.Errorf("kind = %q, want %q: %s", res.Kind, FailTLS, res.Error)
	}
}

func TestEffectiveMinTLSVersionDefaultsToTLS12(t *testing.T) {
	if got := effectiveMinTLSVersion(Monitor{}); got != tls.VersionTLS12 {
		t.Errorf("default floor = 0x%04x, want TLS 1.2", got)
	}
	if got := effectiveMinTLSVersion(Monitor{MinTLSVersion: tls.VersionTLS10}); got != tls.VersionTLS10 {
		t.Errorf("configured floor = 0x%04x, want TLS 1.0", got)
	}
}

// Finding 5. The same expired certificate, on the same host, through both
// checkers: one sentence, not two registers.
//
// This is the most-read line the product produces — an https monitor whose
// certificate expired — and which wording a user got used to depend on whether
// they had created an ssl monitor or an http one, a distinction that means
// nothing to them.
func TestSSLAndHTTPDescribeAnExpiredCertificateIdentically(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-10 * 24 * time.Hour)
	cert, pool := newTestCert(t, certOpts{
		notBefore: now.Add(-100 * 24 * time.Hour),
		notAfter:  expiredAt,
	})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")

	sslRes := sslCheckerTrusting(pool, time.Time{}).
		Check(context.Background(), Monitor{Type: TypeSSL, Target: host, Timeout: 5 * time.Second})
	httpRes := NewHTTPChecker(HTTPOptions{Guard: NewGuard(true)}).
		Check(context.Background(), Monitor{Type: TypeHTTP, Target: srv.URL, Timeout: 5 * time.Second})

	if sslRes.OK || httpRes.OK {
		t.Fatalf("expected both checks to fail; ssl.OK=%v http.OK=%v", sslRes.OK, httpRes.OK)
	}
	if sslRes.Kind != FailTLS || httpRes.Kind != FailTLS {
		t.Errorf("kinds = %q and %q, want both %q", sslRes.Kind, httpRes.Kind, FailTLS)
	}
	if sslRes.Error != httpRes.Error {
		t.Errorf("the two checkers describe one failure in two registers:\n  ssl:  %s\n  http: %s",
			sslRes.Error, httpRes.Error)
	}
	if !strings.Contains(httpRes.Error, "certificate expired 10 days ago") {
		t.Errorf("http error = %q, want the readable expiry wording ssl.go already used", httpRes.Error)
	}
	// The wording the HTTP checker used to emit, verbatim from the ticket.
	if strings.Contains(httpRes.Error, "x509:") || strings.Contains(httpRes.Error, "current time") {
		t.Errorf("http error = %q, still dumps the raw x509 string into the alert", httpRes.Error)
	}
}

// Alert 40 is ambiguous, and which reading is right depends on what we
// offered. A monitor that raised its own floor must not be told its server's
// ciphers are the problem — verified against tls-v1-2.badssl.com:1012, which
// answers a TLS 1.3 floor with alert 40 and not with a version error.
func TestHandshakeFailureAtARaisedFloorNamesTheFloorNotTheCiphers(t *testing.T) {
	err := &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}

	res, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{MinTLSVersion: tls.VersionTLS13})
	if !handled {
		t.Fatal("not recognised as a TLS fault")
	}
	if !strings.Contains(res.Error, "TLS 1.3") {
		t.Errorf("error = %q, want the monitor's own raised floor named", res.Error)
	}
	if strings.Contains(res.Error, "cipher") {
		t.Errorf("error = %q, blames the server's ciphers for a constraint the monitor imposed", res.Error)
	}
}

// A chain of leaf and intermediate that omits an untrusted root also does not
// close, and its top also carries an AIA URL — but nothing is missing from the
// chain the server is required to send, and what it would be sent to fetch is
// a root, which belongs in a trust store and not in a server's chain. Telling
// that operator to add the intermediate would be wrong twice over.
func TestChainMissingOnlyAnUntrustedRootIsNotCalledIncomplete(t *testing.T) {
	leaf, intermediate, _, _ := newChain(t, chainOpts{aia: "http://aia.test/intermediate.crt"})

	// Everything a correctly configured server sends; the root is absent
	// because a server is not supposed to send it, and it is untrusted here.
	chain := []*x509.Certificate{leaf, intermediate}
	err := verifyErrFor(t, chain, x509.NewCertPool())

	got := describeCertProblem("leaf.test", chain, err, time.Now())
	if strings.Contains(got, "incomplete") {
		t.Errorf("message = %q, tells the operator to add an intermediate they already sent", got)
	}
	if strings.Contains(got, "http://aia.test/root.crt") {
		t.Errorf("message = %q, points the operator at a root certificate to add to their chain", got)
	}
	if !strings.Contains(got, "unknown authority") {
		t.Errorf("message = %q, want the unknown-authority wording", got)
	}
}

// The advice has to point the only way that can work. The server already
// answered below our floor, so raising it widens the gap; lowering it is what
// makes the endpoint monitorable.
func TestUnsupportedProtocolVersionAdvisesLoweringTheFloor(t *testing.T) {
	err := errors.New("tls: server selected unsupported protocol version 301")

	res, handled := classifyTLSHandshakeError(time.Now(), err, Monitor{})
	if !handled {
		t.Fatal("not recognised as a TLS fault")
	}
	if !strings.Contains(res.Error, "lower the minimum") {
		t.Errorf("error = %q, want it to advise lowering the floor", res.Error)
	}
	if strings.Contains(res.Error, "raise the minimum") {
		t.Errorf("error = %q, tells the reader to raise a floor the server is already below", res.Error)
	}
}
