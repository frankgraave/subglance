package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// SSLChecker inspects a TLS certificate: is it valid, and how long until it
// expires.
//
// # Why it does its own verification
//
// The obvious implementation hands the hostname to crypto/tls and lets the
// handshake fail. That is exactly wrong for this check. An expired certificate
// makes the handshake fail, so the one piece of information the user wants —
// *when* did it expire, and by how much — is thrown away with the error.
//
// So the handshake runs with InsecureSkipVerify and verification is performed
// afterwards, by hand, against the certificate we now hold. Nothing is trusted
// implicitly; the result is a verdict *plus* the expiry date, on both the happy
// and the unhappy path.
type SSLChecker struct {
	dialer *net.Dialer

	// now is overridable so tests can reason about expiry without waiting.
	now func() time.Time

	// rootsForTest replaces the system trust store. Only tests set it; in
	// production a nil pool means x509 uses the host's roots, which is what
	// users expect a monitoring tool to judge their certificates against.
	rootsForTest *x509.CertPool
}

// NewSSLChecker returns a checker that enforces the SSRF guard at dial time.
func NewSSLChecker(guard *Guard) *SSLChecker {
	if guard == nil {
		guard = NewGuard(false) // fail closed
	}
	return &SSLChecker{
		dialer: &net.Dialer{Control: guard.ControlFunc()},
		now:    time.Now,
	}
}

// Check performs a TLS handshake and inspects the presented certificate.
func (c *SSLChecker) Check(ctx context.Context, m Monitor) Result {
	start := time.Now()

	// TLS without a port almost always means 443.
	host, port, err := ParseHostPort(m.Target, 443)
	if err != nil {
		return fail(start, FailInternal, "%v", err)
	}
	if port == 0 {
		port = 443
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := net.JoinHostPort(host, strconv.Itoa(port))

	conn, err := c.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return classifyRequestError(start, ctx, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		_ = conn.SetDeadline(deadline)
	}

	// InsecureSkipVerify is deliberate — see the type comment. Verification
	// happens below, where a failure still leaves the certificate readable.
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec // verified manually below
		MinVersion:         tls.VersionTLS12,
	})

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return classifyRequestError(start, ctx, err)
	}

	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fail(start, FailTLS, "server presented no certificate")
	}

	leaf := certs[0]
	res := Result{
		OK:         true,
		Latency:    time.Since(start),
		CertExpiry: leaf.NotAfter,
		CheckedAt:  start,
	}

	now := c.now()

	// Expiry first: it is the specific, actionable answer, and it would
	// otherwise be reported as the vaguer "unknown authority" style error that
	// chain verification produces for an expired leaf.
	switch {
	case now.After(leaf.NotAfter):
		res.OK = false
		res.Kind = FailTLS
		res.Error = fmt.Sprintf("certificate expired %s ago (on %s)",
			humanDuration(now.Sub(leaf.NotAfter)), leaf.NotAfter.Format(time.DateOnly))
		return res

	case now.Before(leaf.NotBefore):
		res.OK = false
		res.Kind = FailTLS
		res.Error = fmt.Sprintf("certificate is not valid until %s",
			leaf.NotBefore.Format(time.DateOnly))
		return res
	}

	if err := verifyChain(host, certs, now, c.rootsForTest); err != nil {
		res.OK = false
		res.Kind = FailTLS
		res.Error = err.Error()
		return res
	}

	// A certificate that is valid but about to expire fails on purpose. A
	// warning nobody reads is how certificates expire on a Sunday; an incident
	// gets someone's attention while there is still time to renew.
	if m.SSLWarnDays > 0 {
		remaining := leaf.NotAfter.Sub(now)
		if remaining < time.Duration(m.SSLWarnDays)*24*time.Hour {
			res.OK = false
			res.Kind = FailCertExpiry
			res.Error = fmt.Sprintf("certificate expires in %d days (on %s)",
				int(remaining.Hours()/24), leaf.NotAfter.Format(time.DateOnly))
			return res
		}
	}

	return res
}

// verifyChain performs the validation that InsecureSkipVerify skipped.
func verifyChain(host string, certs []*x509.Certificate, now time.Time, roots *x509.CertPool) error {
	leaf := certs[0]

	if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("certificate does not match hostname %s: %w", host, err)
	}

	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}

	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots, // nil means the system trust store
		Intermediates: intermediates,
		CurrentTime:   now,
	})
	if err != nil {
		var authErr x509.UnknownAuthorityError
		if errors.As(err, &authErr) {
			return fmt.Errorf("certificate signed by unknown authority")
		}
		return fmt.Errorf("certificate verification failed: %w", err)
	}
	return nil
}

// humanDuration renders a duration the way someone reading an alert at 3am
// wants it: days and hours, not 847h13m22.9s.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}

	years := int(d.Hours() / 24 / 365)
	days := int(d.Hours()/24) % 365
	if days == 0 {
		return fmt.Sprintf("%d years", years)
	}
	return fmt.Sprintf("%d years %d days", years, days)
}
