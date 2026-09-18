package checker

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// This file holds the one interpretation of a TLS failure that both the SSL
// checker and the HTTP checker report from.
//
// # Why it is shared rather than duplicated
//
// The two checkers used to describe the same event in two registers. An
// expired certificate read as `certificate expired 11 years 158 days ago (on
// 2015-04-12)` from the SSL checker and as `TLS certificate verification
// failed: x509: certificate has expired or is not yet valid: current time
// 2026-09-15T13:59:00Z is after 2015-04-12T23:59:59Z` from the HTTP checker.
// An https monitor with an expired certificate is the most common TLS failure
// there is, so that was the single most-read line the product produces, and
// which of the two wordings a user got depended on which monitor type they
// had happened to create — a distinction that means nothing to them.
//
// The register kept is the SSL checker's, because it answers the question the
// reader has (how long, and on what day) instead of restating the comparison
// the library just performed.

// minTLSVersion is the floor the checkers negotiate with when a monitor does
// not ask for another one.
//
// TLS 1.2 is the right default: everything currently maintained speaks it, and
// a monitoring tool that silently negotiates TLS 1.0 would report green for a
// server no browser will open. It is a default and not a rule because the
// thing a self-hoster most wants to watch is often the old appliance in the
// cupboard that only speaks TLS 1.0 — see Monitor.MinTLSVersion.
const minTLSVersion = tls.VersionTLS12

// effectiveMinTLSVersion resolves a monitor's negotiation floor.
func effectiveMinTLSVersion(m Monitor) uint16 {
	if m.MinTLSVersion == 0 {
		return minTLSVersion
	}
	return m.MinTLSVersion
}

// tlsVersionName renders a version constant the way a person writes it.
//
// crypto/tls reports an unknown version as the four hex digits on the wire,
// which is how `unsupported protocol version 301` reached a user who wanted to
// be told "TLS 1.0". Anything we do not have a name for keeps the hex, since a
// number we cannot name is still better than a blank.
func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	case 0x0300:
		return "SSL 3.0"
	}
	return fmt.Sprintf("unknown version 0x%04x", v)
}

// serverSelectedVersionPrefix is the fixed part of crypto/tls's error for a
// server that answered below our floor. Matched as a string because the
// library returns a bare errors.New for it — there is no typed error and no
// exported sentinel to compare against. The suffix is the version in hex.
const serverSelectedVersionPrefix = "tls: server selected unsupported protocol version "

// classifyTLSHandshakeError recognises a handshake that failed over the terms
// of the connection rather than over a certificate.
//
// It returns ok=false when the error is not one of those, so the caller falls
// through to its ordinary transport classification.
//
// # Why these are not connection failures
//
// A rejected cipher suite and a rejected protocol version both surface as a
// net.OpError, which the transport classifier quite reasonably reported as
// `connection failed: tls: handshake failure` with kind=connection. That sends
// the reader to their firewall. But the TCP connection succeeded — the server
// is up, reachable and answering — and what failed is the negotiation. The
// kind is therefore FailTLS, and the message names the negotiation.
//
// FailTLS is reused rather than a new kind invented. Every consumer of
// FailureKind already words it ("TLS failure"), and a cipher rejection is a TLS
// failure by any reading; a new constant would render as its own raw key in
// every client that had not been taught it yet, which is a worse answer than
// the correct general one.
func classifyTLSHandshakeError(start time.Time, err error, m Monitor) (Result, bool) {
	msg := err.Error()

	// Our own stack refused to go on: the server picked a version below the
	// floor we offered. This is a local error with no typed form.
	if i := strings.Index(msg, serverSelectedVersionPrefix); i >= 0 {
		var v uint16
		if _, scanErr := fmt.Sscanf(msg[i+len(serverSelectedVersionPrefix):], "%x", &v); scanErr == nil && v != 0 {
			return fail(start, FailTLS,
				"server speaks %s, below this monitor's minimum of %s (lower the minimum to monitor it anyway)",
				tlsVersionName(v), tlsVersionName(effectiveMinTLSVersion(m))), true
		}
		return fail(start, FailTLS,
			"server selected a TLS version below this monitor's minimum of %s",
			tlsVersionName(effectiveMinTLSVersion(m))), true
	}

	// No version in common at all — we never got far enough to be told which
	// one the server wanted.
	if strings.Contains(msg, "tls: no supported versions satisfy MinVersion and MaxVersion") ||
		strings.Contains(msg, "protocol version not supported") {
		return fail(start, FailTLS,
			"no TLS version in common: this monitor requires at least %s",
			tlsVersionName(effectiveMinTLSVersion(m))), true
	}

	// A fatal alert from the peer. net.OpError's Op is "remote error" only for
	// an alert the other side sent us, which is never a network condition:
	// the packets arrived, were understood, and were refused. Matching on Op
	// rather than on the message is deliberate — crypto/tls keeps the alert
	// type unexported, so Op is the only structural handle there is.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "remote error" {
		alert := ""
		if opErr.Err != nil {
			alert = strings.TrimPrefix(opErr.Err.Error(), "tls: ")
		}
		switch {
		case strings.Contains(alert, "handshake failure"):
			// Alert 40 means only "nothing you offered is acceptable to me";
			// it does not say which term failed. Which reading is right
			// depends on what we offered.
			//
			// At the default floor, the offer was every version from TLS 1.2
			// up and every suite Go still implements, so what is left to
			// refuse is the cipher list — and for a server still running RC4,
			// 3DES, NULL or a sub-1024-bit DH group that is a permanent
			// position: Go removed those suites, so there is nothing to
			// negotiate down to.
			//
			// Above the default the monitor has narrowed the offer itself,
			// and naming the ciphers would be a guess that sends the reader
			// looking at the wrong half of their configuration. Verified
			// against tls-v1-2.badssl.com:1012, a TLS 1.2-only server, which
			// answers a TLS 1.3 floor with this same alert 40 rather than
			// with a version error.
			if minVer := effectiveMinTLSVersion(m); minVer > minTLSVersion {
				return fail(start, FailTLS,
					"server rejected the TLS handshake: it offers nothing this monitor accepts at %s or above",
					tlsVersionName(minVer)), true
			}
			return fail(start, FailTLS,
				"server rejected the TLS handshake: no cipher suite in common — it offers only ciphers modern clients no longer accept"), true
		case strings.Contains(alert, "protocol version"):
			return fail(start, FailTLS,
				"server rejected the TLS handshake: it does not accept %s or newer",
				tlsVersionName(effectiveMinTLSVersion(m))), true
		case alert != "":
			return fail(start, FailTLS, "server rejected the TLS handshake: %s", alert), true
		}
	}

	return Result{}, false
}

// describeCertProblem words a chain that did not verify.
//
// certs is the chain exactly as the server presented it, leaf first; it may be
// empty when the failure happened before any certificate was read.
//
// The order is the order of usefulness, not the order the library checks in.
// Expiry is stated first and from the certificate rather than from the
// verification error, because expiry is the specific and actionable answer and
// x509 otherwise reports an expired leaf through whichever check happened to
// fail first.
func describeCertProblem(host string, certs []*x509.Certificate, err error, now time.Time) string {
	if len(certs) > 0 {
		leaf := certs[0]
		switch {
		case now.After(leaf.NotAfter):
			return fmt.Sprintf("certificate expired %s ago (on %s)",
				humanDuration(now.Sub(leaf.NotAfter)), leaf.NotAfter.Format(time.DateOnly))
		case now.Before(leaf.NotBefore):
			return fmt.Sprintf("certificate is not valid until %s",
				leaf.NotBefore.Format(time.DateOnly))
		}
	}

	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		named := host
		if named == "" {
			named = hostErr.Host
		}
		return fmt.Sprintf("certificate does not match hostname %s: %v", named, err)
	}

	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		if missing := missingIntermediate(certs); missing != "" {
			return missing
		}
		return "certificate signed by unknown authority"
	}

	if err == nil {
		return "certificate verification failed"
	}
	return fmt.Sprintf("certificate verification failed: %v", err)
}

// missingIntermediate reports an incomplete chain, or "" when the chain the
// server sent is complete as far as it goes and the CA really is unknown.
//
// # Why this is worth telling apart
//
// A server that forgets to send its intermediate produces exactly the same
// `certificate signed by unknown authority` as a genuinely untrusted CA. The
// two have opposite fixes: one is a line in the server's configuration, the
// other is a decision about trust. Pointing the first at the CA is how a
// five-minute fix becomes a support ticket, and it is one of the most common
// TLS misconfigurations there is.
//
// # Why the leaf alone, and not any chain that does not close
//
// The tell has to identify the absent certificate as an *intermediate*, not
// merely as absent. A server that sends leaf and intermediate but omits an
// untrusted root has a chain that also does not close and whose top also
// carries an AIA URL — and telling its operator to add the missing
// intermediate would be wrong twice over: nothing is missing from the chain
// they are required to send, and the certificate they would be sent to fetch
// is a root, which belongs in a trust store and not in a server's chain.
//
// So the diagnosis is made only when the server sent the leaf and nothing
// else. Then the absent issuer is the leaf's own issuer, and a certificate
// that signs an end-entity certificate is an intermediate by definition — a
// root does not sign leaves directly in any chain a public CA issues. That is
// also exactly the shape badssl's incomplete-chain endpoint has.
//
// The narrower rule costs a longer chain that is missing a middle
// certificate, which falls back to the unknown-authority wording. That is the
// right way to be wrong: a vaguer true sentence beats a specific false one
// that sends the reader to fix something that is not broken.
//
// The AIA URL is read and never fetched. Fetching it would make the check pass
// for a server that is still misconfigured for every real client, which is the
// opposite of the job; it would also give a monitoring probe an outbound
// request to an address the monitored server chooses.
func missingIntermediate(certs []*x509.Certificate) string {
	if len(certs) != 1 {
		return ""
	}
	leaf := certs[0]

	// Self-issued: the chain closes on a certificate the server signed itself.
	// Nothing is missing — it is simply not trusted here.
	if leaf.Issuer.String() == leaf.Subject.String() {
		return ""
	}
	if leaf.IsCA {
		// Not an end-entity certificate, so its issuer need not be an
		// intermediate and the inference above does not hold.
		return ""
	}
	if len(leaf.IssuingCertificateURL) == 0 {
		return ""
	}

	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	return fmt.Sprintf(
		"incomplete certificate chain: the server did not send the intermediate certificate for issuer %q, which the certificate says is published at %s — add it to the server's certificate chain",
		issuer, leaf.IssuingCertificateURL[0])
}
