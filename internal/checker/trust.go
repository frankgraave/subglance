package checker

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// This file holds the two ways a check can trust a certificate the host's
// trust store does not: a pin on one monitor, and an extra certificate
// authority for the whole instance.
//
// # Why there is no "do not verify" switch
//
// The common answer to a self-signed certificate is a switch that turns
// verification off. A monitor with that switch on accepts whatever answers on
// the address, so it stays green when something else answers there: the
// question it was set up to answer is the one it can no longer ask. A pin
// says precisely what is trusted — this certificate, on this monitor — and
// still fails, with its own sentence, the day the server presents another.
//
// A pin cannot follow a private CA that issues certificates for a day at a
// time; that is what the extra CA is for. It is added beside the system trust
// store, never instead of it, so public certificates keep passing.

// CertFingerprint is the SHA-256 fingerprint of a certificate's DER encoding,
// written the way openssl, browsers and appliance web interfaces print it:
// upper-case hex pairs joined by colons. It is how a pin is stored, so the
// reader can compare it character for character with what the service shows.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return formatFingerprint(sum[:])
}

func formatFingerprint(sum []byte) string {
	h := strings.ToUpper(hex.EncodeToString(sum))
	var b strings.Builder
	b.Grow(len(h) + len(h)/2)
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

// ParseCertFingerprint reads a SHA-256 fingerprint as a person copies it —
// with or without colons or spaces, in either case — and returns it in
// CertFingerprint's form. ok is false for anything that is not 32 bytes of
// hex.
func ParseCertFingerprint(s string) (string, bool) {
	clean := strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(s))
	if len(clean) != 2*sha256.Size {
		return "", false
	}
	sum, err := hex.DecodeString(clean)
	if err != nil {
		return "", false
	}
	return formatFingerprint(sum), true
}

// CertificateInfo describes the certificate a failed check was presented
// with, when trusting it would have made the check pass.
//
// It is what a form shows beside "Trust this certificate": a pin is
// trust-on-first-use, and the fingerprint is there so it can be compared with
// the one the service itself displays before anyone accepts it.
type CertificateInfo struct {
	Subject   string
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	SHA256    string
}

func certificateInfo(leaf *x509.Certificate) *CertificateInfo {
	return &CertificateInfo{
		Subject:   leaf.Subject.String(),
		Issuer:    leaf.Issuer.String(),
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
		SHA256:    CertFingerprint(leaf),
	}
}

// pinnable reports whether trusting leaf would let a check pass at now. An
// expired certificate fails pinned or not, so offering to trust it would
// promise a fix that does not come.
func pinnable(leaf *x509.Certificate, now time.Time) bool {
	return !now.After(leaf.NotAfter) && !now.Before(leaf.NotBefore)
}

// offerPin returns the certificate to offer for trusting, when the chain
// failed only because nobody this host trusts vouches for it: self-signed, or
// issued by a CA the trust store does not hold. Nil otherwise.
//
// Two failures that a pin would also silence are left out on purpose. A
// publicly trusted certificate for another host name is a server serving the
// wrong certificate, and a chain missing its intermediate is a server
// configuration every other client trips over too; a pin would turn either
// green while the site stays broken for its visitors. The host name is
// therefore not asked here — an appliance reached by its IP address has a
// self-signed certificate that names neither, and is exactly what this is for.
func offerPin(certs []*x509.Certificate, roots *x509.CertPool, now time.Time) *CertificateInfo {
	if len(certs) == 0 || !pinnable(certs[0], now) || missingIntermediate(certs) != "" {
		return nil
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now})
	var authErr x509.UnknownAuthorityError
	if !errors.As(err, &authErr) {
		return nil
	}
	return certificateInfo(certs[0])
}

// pinMismatchError is a pinned monitor's server presenting a certificate
// other than the trusted one.
type pinMismatchError struct {
	certs []*x509.Certificate
	since time.Time
}

func (e *pinMismatchError) Error() string {
	msg := "certificate changed since it was trusted"
	if !e.since.IsZero() {
		msg += " on " + e.since.Format(time.DateOnly)
	}
	return msg + "; the server now presents SHA-256 " + CertFingerprint(e.certs[0])
}

// checkPin is the verdict on a chain presented to a monitor that trusts one
// certificate. Only the leaf is compared: the pin is the trust decision, so
// the chain above it, the host name in it and the issuer's opinion of it are
// not asked. Its validity period still is — a pinned certificate that has
// expired is as expired as any other.
func checkPin(certs []*x509.Certificate, pin string, since, now time.Time) error {
	leaf := certs[0]
	if CertFingerprint(leaf) != pin {
		return &pinMismatchError{certs: certs, since: since}
	}
	if !pinnable(leaf, now) {
		return &tls.CertificateVerificationError{
			UnverifiedCertificates: certs,
			Err:                    x509.CertificateInvalidError{Cert: leaf, Reason: x509.Expired},
		}
	}
	return nil
}

// verifyPeer is the verification crypto/tls performs itself, for a hop of a
// pinned monitor that the pin does not cover.
func verifyPeer(serverName string, certs []*x509.Certificate, roots *x509.CertPool, now time.Time) error {
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
	})
	if err != nil {
		return &tls.CertificateVerificationError{UnverifiedCertificates: certs, Err: err}
	}
	return nil
}

// pinHop is the request a pinned HTTP check is on: the monitored URL, then
// each redirect hop. The handshake runs on the transport's goroutine, so it
// is read under a lock.
type pinHop struct {
	mu       sync.Mutex
	origin   string
	hostname string
}

func (h *pinHop) set(u *url.URL) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.origin, h.hostname = originOf(u), u.Hostname()
}

func (h *pinHop) get() (origin, hostname string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.origin, h.hostname
}

// pinnedVerifier is the VerifyConnection hook of an HTTP monitor that trusts
// one certificate. The pin speaks for the monitored origin only: a redirect
// to another host or port is verified the ordinary way, against roots and the
// hop's own host name, so trusting a router's certificate does not also trust
// whatever it redirects to.
func pinnedVerifier(m Monitor, pinned string, hop *pinHop, roots *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		certs := cs.PeerCertificates
		if len(certs) == 0 {
			return errors.New("tls: server presented no certificate")
		}
		now := time.Now()
		origin, hostname := hop.get()
		if origin == pinned {
			return checkPin(certs, m.TrustedCertSHA256, m.TrustedCertAt, now)
		}
		// The host name of the URL rather than cs.ServerName, which is
		// empty for an IP address: x509 checks an address against the
		// certificate's IP entries, and an empty name would check nothing.
		return verifyPeer(hostname, certs, roots, now)
	}
}

// untrustedCertificate returns the certificate an HTTP check failed to
// trust, when pinning it would let the check pass: the monitored origin
// itself presented it, and offerPin would offer it. A pinned monitor
// presented another certificate is offered the new one, so it can be trusted
// in turn.
func untrustedCertificate(err error, target *url.URL, roots *x509.CertPool, now time.Time) *CertificateInfo {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// A redirect hop elsewhere fails under that hop's URL. Its
		// certificate is not the monitored origin's to pin.
		if u, perr := url.Parse(urlErr.URL); perr != nil || originOf(u) != originOf(target) {
			return nil
		}
	}
	var verifyErr *tls.CertificateVerificationError
	var mismatch *pinMismatchError
	switch {
	case errors.As(err, &mismatch):
		if pinnable(mismatch.certs[0], now) {
			return certificateInfo(mismatch.certs[0])
		}
	case errors.As(err, &verifyErr):
		return offerPin(verifyErr.UnverifiedCertificates, roots, now)
	}
	return nil
}

// ExtraRoots returns the system trust store with every certificate in the PEM
// file at path added to it, and how many were added.
//
// A file that holds no certificate is refused rather than treated as empty:
// someone who set the option expects a CA to be trusted, and starting without
// it would show up only as every check behind that CA failing.
func ExtraRoots(path string) (*x509.CertPool, int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied CA file path, read once at start
	if err != nil {
		return nil, 0, fmt.Errorf("read extra CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	n, err := addPEMCertificates(pool, data)
	if err != nil {
		return nil, 0, fmt.Errorf("extra CA file %s: %w", path, err)
	}
	return pool, n, nil
}

// addPEMCertificates adds every CERTIFICATE block in data to pool. Other
// blocks — a private key that ended up in the same bundle — are skipped, but
// a CERTIFICATE block that does not parse is an error: a CA silently left out
// is the failure this file exists to avoid.
func addPEMCertificates(pool *x509.CertPool, data []byte) (int, error) {
	n := 0
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return 0, fmt.Errorf("certificate %d does not parse: %w", n+1, err)
		}
		pool.AddCert(cert)
		n++
	}
	if n == 0 {
		return 0, errors.New("holds no PEM certificate")
	}
	return n, nil
}
