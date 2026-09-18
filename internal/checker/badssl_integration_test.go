//go:build badssl

// This file is behind a build tag and does not run in `make test` or in CI.
//
// It is the acceptance suite for the TLS diagnosis: badssl.com hosts one
// subdomain per TLS failure mode, which is the failure matrix this package has
// to have an opinion about. Running it needs the public internet and the state
// of someone else's certificates, so it is not a unit test and must never gate
// a build — but the table below is the ground truth the unit tests are
// abstractions of, and it is worth being able to re-run it deliberately.
//
// Run it with:
//
//	go test -tags badssl -v ./internal/checker/ -run TestBadSSL
//
// Verify it still compiles, without running it, with:
//
//	go vet -tags badssl ./internal/checker/
//
// # Why it fails instead of skipping when the network is down
//
// A network-dependent test that calls t.Skip on a connection error reports
// green for a run that proved nothing, which is worse than having no test:
// nobody reads a skip, and "the suite passed" becomes a lie the day the
// diagnosis regresses and the sandbox happens to be offline. So reachability
// is established once, up front, against a known-good endpoint, and the whole
// suite fails loudly if it cannot be. Beyond that point every endpoint is
// asserted, never skipped.

package checker

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

// badsslCase is one endpoint and the verdict it must produce.
type badsslCase struct {
	// target is host[:port] for the SSL checker; the HTTP checker prefixes
	// https:// and reuses it.
	target string

	// wantKind is the FailureKind expected. FailNone means the check must
	// pass.
	wantKind FailureKind

	// wantPhrases are substrings the message must contain. They are the point
	// of the ticket: the kind says which family the failure is in, the phrase
	// says whether the sentence sends the reader to the right place.
	wantPhrases []string

	// mustNotContain are the old wrong wordings. Asserting their absence is
	// what stops finding 1 and finding 4 from quietly coming back.
	mustNotContain []string

	// minTLS overrides the monitor's negotiation floor.
	minTLS uint16

	// knownGap records a verdict we know is wrong, with the issue that owns
	// it. It is written down rather than asserted as correct so the table
	// cannot be read as a claim that the current answer is right.
	knownGap string
}

// badsslCases is the table.
//
// # Which endpoints are in it, and which are deliberately not
//
// Many badssl subdomains have themselves expired — sha1-intermediate,
// no-common-name, superfish, extended-validation, 10000-sans and others. They
// do fail, but on expiry rather than on the thing they are named for, so
// asserting on them would be asserting on badssl's certificate renewal
// schedule and not on our diagnosis. They are left out.
//
// What remains is what held when this table was written and re-verified:
// expiry, hostname, self-signed, untrusted root, the incomplete chain, the
// revoked certificate, the cipher subdomains and the protocol ports.
var badsslCases = []badsslCase{
	// --- Finding 1: weak ciphers. Every one of these used to report
	// kind=connection, "connection failed: tls: handshake failure".
	{
		target:         "rc4.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "rc4-md5.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "3des.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "null.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "dh480.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "dh512.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},
	{
		target:         "dh1024.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"cipher"},
		mustNotContain: []string{"connection failed"},
	},

	// --- Finding 2: old protocol versions. At the default floor these fail,
	// but as a TLS fault naming the version rather than "protocol version
	// 301"; with the floor lowered they become monitorable, which is the
	// point of making it configurable.
	{
		target:         "tls-v1-0.badssl.com:1010",
		wantKind:       FailTLS,
		wantPhrases:    []string{"TLS 1.0", "TLS 1.2"},
		mustNotContain: []string{"301", "connection failed", "request failed"},
	},
	{
		target:         "tls-v1-0.badssl.com:1010",
		minTLS:         tls.VersionTLS10,
		wantKind:       FailNone,
		mustNotContain: []string{"301"},
	},
	{
		target:         "tls-v1-1.badssl.com:1011",
		wantKind:       FailTLS,
		wantPhrases:    []string{"TLS 1.1", "TLS 1.2"},
		mustNotContain: []string{"302", "connection failed", "request failed"},
	},
	{
		target:   "tls-v1-1.badssl.com:1011",
		minTLS:   tls.VersionTLS11,
		wantKind: FailNone,
	},
	{
		target:   "tls-v1-2.badssl.com:1012",
		wantKind: FailNone,
	},
	{
		// The inverse use: a raised floor as a deliberate assertion about the
		// server. This endpoint serves TLS 1.2 only, so requiring TLS 1.3 must
		// fail — and say so in those terms.
		//
		// Note what it does *not* produce. A raised floor is refused by the
		// peer with alert 40, the same alert a server with only RC4 sends,
		// not with the "server selected unsupported protocol version" error
		// that a *lowered* offer produces locally. The message therefore has
		// to name the monitor's own floor rather than the server's ciphers.
		target:         "tls-v1-2.badssl.com:1012",
		minTLS:         tls.VersionTLS13,
		wantKind:       FailTLS,
		wantPhrases:    []string{"TLS 1.3"},
		mustNotContain: []string{"cipher", "connection failed"},
	},

	// --- Finding 3: revocation. Recorded as the wrong answer it is, not
	// asserted as correct.
	{
		target:   "revoked.badssl.com",
		wantKind: FailNone,
		knownGap: "SUB-44: the certificate is revoked and every browser refuses this site; " +
			"we have no OCSP or CRL check, so we report it healthy. The expectation below " +
			"records what we currently do, not what is right.",
	},

	// --- Finding 4: incomplete chain versus an authority that really is
	// unknown. Same x509 error, opposite fixes.
	{
		target:         "incomplete-chain.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"incomplete certificate chain", "intermediate"},
		mustNotContain: []string{"unknown authority"},
	},
	{
		target:         "untrusted-root.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"unknown authority"},
		mustNotContain: []string{"incomplete"},
	},
	{
		target:         "self-signed.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"unknown authority"},
		mustNotContain: []string{"incomplete"},
	},

	// --- Finding 5: the shared register. These two are the wordings both
	// checkers must now produce; TestBadSSLBothCheckersAgree compares them
	// directly.
	{
		target:         "expired.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"certificate expired", "on 2015-04-12"},
		mustNotContain: []string{"x509:", "current time"},
	},
	{
		target:         "wrong.host.badssl.com",
		wantKind:       FailTLS,
		wantPhrases:    []string{"does not match hostname", "wrong.host.badssl.com"},
		mustNotContain: []string{"unknown authority"},
	},
}

// requireBadSSLReachable establishes that badssl is reachable at all, and
// fails the suite rather than skipping it when it is not. See the file
// comment.
//
// It deliberately does not reuse this package's requireNetwork, which skips
// under -short so that a filtered CI runner or a third-party host having a bad
// day cannot turn the repository red. That is the right trade for a test that
// runs on every push. This suite does not run on any push — it is behind a
// build tag and is only ever invoked on purpose — so the same trade would buy
// nothing and cost the only thing the suite is for: somebody typed the tag
// because they wanted an answer, and a skip is not one.
func requireBadSSLReachable(t *testing.T) {
	t.Helper()

	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.Dial("tcp", "badssl.com:443")
	if err != nil {
		t.Fatalf("badssl.com is unreachable: %v\n"+
			"This suite asserts against the live internet by design and will not "+
			"skip itself into a green run. Either run it with network access or "+
			"do not run it at all (it is behind -tags badssl for exactly this reason).", err)
	}
	_ = conn.Close()
}

func badsslMonitor(c badsslCase, typ Type) Monitor {
	target := c.target
	if typ == TypeHTTP {
		target = "https://" + c.target
	}
	return Monitor{
		Type:          typ,
		Target:        target,
		Timeout:       20 * time.Second,
		MinTLSVersion: c.minTLS,
	}
}

func checkCase(t *testing.T, c badsslCase, res Result) {
	t.Helper()

	if c.knownGap != "" {
		t.Logf("KNOWN GAP — %s", c.knownGap)
	}

	gotKind := res.Kind
	if res.OK {
		gotKind = FailNone
	}
	if gotKind != c.wantKind {
		t.Errorf("kind = %q, want %q (message: %s)", gotKind, c.wantKind, res.Error)
	}
	for _, want := range c.wantPhrases {
		if !strings.Contains(res.Error, want) {
			t.Errorf("message %q does not contain %q", res.Error, want)
		}
	}
	for _, bad := range c.mustNotContain {
		if strings.Contains(res.Error, bad) {
			t.Errorf("message %q still contains the old wording %q", res.Error, bad)
		}
	}
	t.Logf("ok=%v kind=%-10s %s", res.OK, gotKind, res.Error)
}

func TestBadSSLSSLChecker(t *testing.T) {
	requireBadSSLReachable(t)

	c := NewSSLChecker(NewGuard(false))
	for _, tc := range badsslCases {
		name := tc.target
		if tc.minTLS != 0 {
			name += "@" + tlsVersionName(tc.minTLS)
		}
		t.Run(name, func(t *testing.T) {
			res := c.Check(context.Background(), badsslMonitor(tc, TypeSSL))
			checkCase(t, tc, res)
		})
	}
}

// TestBadSSLBothCheckersAgree is finding 5 against the live endpoints: the
// same host through both checkers has to produce the same sentence.
//
// Only the certificate cases are compared. The cipher and protocol endpoints
// legitimately differ — an http monitor of a server offering nothing but RC4
// never gets a response either way, but the two paths reach that conclusion
// through different libraries and there is no shared certificate to describe.
func TestBadSSLBothCheckersAgree(t *testing.T) {
	requireBadSSLReachable(t)

	shared := []string{
		"expired.badssl.com",
		"wrong.host.badssl.com",
		"self-signed.badssl.com",
		"untrusted-root.badssl.com",
		"incomplete-chain.badssl.com",
	}

	sslC := NewSSLChecker(NewGuard(false))
	httpC := NewHTTPChecker(HTTPOptions{Guard: NewGuard(false)})

	for _, host := range shared {
		t.Run(host, func(t *testing.T) {
			m := badsslCase{target: host}
			sslRes := sslC.Check(context.Background(), badsslMonitor(m, TypeSSL))
			httpRes := httpC.Check(context.Background(), badsslMonitor(m, TypeHTTP))

			if sslRes.Kind != httpRes.Kind {
				t.Errorf("kind: ssl=%q http=%q", sslRes.Kind, httpRes.Kind)
			}
			if sslRes.Error != httpRes.Error {
				t.Errorf("one failure, two registers:\n  ssl:  %s\n  http: %s",
					sslRes.Error, httpRes.Error)
			}
			t.Logf("both: %s", sslRes.Error)
		})
	}
}
