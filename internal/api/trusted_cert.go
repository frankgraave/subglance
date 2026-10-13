package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// certificateWire is the certificate a failed check was presented, on a
// preview or a manual check, when trusting it would let the check pass. It is
// what a form shows beside "Trust this certificate"; sha256 is the value to
// send back as trusted_cert_sha256.
type certificateWire struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	SHA256    string    `json:"sha256"`
}

func certificateToWire(c *checker.CertificateInfo) *certificateWire {
	if c == nil {
		return nil
	}
	return &certificateWire{
		Subject: c.Subject, Issuer: c.Issuer,
		NotBefore: c.NotBefore.UTC(), NotAfter: c.NotAfter.UTC(),
		SHA256: c.SHA256,
	}
}

// pinCarrier reports whether a monitor of this type and target can trust a
// certificate: an ssl monitor, or an http monitor of an https address. A pin
// on anything else would be stored and never read.
func pinCarrier(typ, target string) bool {
	switch typ {
	case "ssl":
		return true
	case "http":
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(target)), "https://")
	}
	return false
}

// parseTrustedCert validates trusted_cert_sha256 for a monitor of typ and
// target and returns it in the stored spelling. "" means no pin.
func parseTrustedCert(label *string, typ, target string) (string, problem) {
	if label == nil || strings.TrimSpace(*label) == "" {
		return "", problem{}
	}
	pin, ok := checker.ParseCertFingerprint(*label)
	if !ok {
		return "", fieldProblem("trusted_cert_sha256",
			"trusted_cert_sha256 must be a SHA-256 fingerprint: 64 hex digits, colons optional; got "+
				strconv.Quote(*label))
	}
	if !pinCarrier(typ, target) {
		return "", fieldProblem("trusted_cert_sha256",
			"trusted_cert_sha256 applies only to an ssl monitor or an http monitor of an https address")
	}
	return pin, problem{}
}

// applyTrustedCert sets the pin a create or patch request carries on m, after
// m's type and target are final. A pin that is sent again unchanged keeps the
// time it was first set, so an import of a monitor's own export does not
// move it; a monitor that stops being able to carry a pin loses it.
func applyTrustedCert(m *store.Monitor, label *string) problem {
	if label != nil {
		pin, p := parseTrustedCert(label, m.Type, m.Target)
		if !p.ok() {
			return p
		}
		if pin != m.TrustedCertSHA256 {
			m.TrustedCertSHA256 = pin
			m.TrustedCertAt = time.Time{}
			if pin != "" {
				m.TrustedCertAt = time.Now().UTC().Truncate(time.Second)
			}
		}
	}
	if !pinCarrier(m.Type, m.Target) {
		m.TrustedCertSHA256 = ""
		m.TrustedCertAt = time.Time{}
	}
	return problem{}
}
