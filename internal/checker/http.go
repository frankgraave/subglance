package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBodyRead caps how much of a response body is read for keyword matching.
//
// Without a cap, one monitored endpoint streaming an endless body would pin a
// worker and grow the heap until the process dies. 1 MiB is far more than any
// health endpoint needs.
const maxBodyRead = 1 << 20

// HTTPChecker probes HTTP and HTTPS endpoints.
//
// It is safe for concurrent use and should be shared: the embedded transport
// pools connections, which matters when hundreds of monitors run on the same
// interval.
type HTTPChecker struct {
	client *http.Client
	guard  *Guard
	ua     string
}

// HTTPOptions configures NewHTTPChecker.
type HTTPOptions struct {
	// Guard enforces the SSRF policy. Required.
	Guard *Guard

	// MaxIdleConnsPerHost bounds the connection pool. Zero means 2.
	MaxIdleConnsPerHost int

	// UserAgent identifies SubGlance to the monitored service. Sites that
	// block unknown agents are a real support burden, and an honest agent
	// string lets an admin see who is polling them.
	UserAgent string
}

// NewHTTPChecker returns a checker sharing one connection pool.
func NewHTTPChecker(opts HTTPOptions) *HTTPChecker {
	if opts.Guard == nil {
		opts.Guard = NewGuard(false) // fail closed
	}
	if opts.MaxIdleConnsPerHost == 0 {
		opts.MaxIdleConnsPerHost = 2
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "SubGlance/1.0 (+https://subglance.com)"
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   opts.Guard.ControlFunc(),
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableCompression:    false,
	}

	c := &HTTPChecker{
		guard: opts.Guard,
		ua:    opts.UserAgent,
		client: &http.Client{
			Transport: transport,
			// Per-request deadlines come from the context, so no client-level
			// timeout: it would silently override a monitor's own setting.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				return nil
			},
		},
	}
	return c
}

// Check runs one HTTP probe.
func (c *HTTPChecker) Check(ctx context.Context, m Monitor) Result {
	start := time.Now()

	matcher, err := ParseStatusMatcher(m.ExpectedStatus)
	if err != nil {
		return fail(start, FailInternal, "invalid expected status %q: %v", m.ExpectedStatus, err)
	}

	target, err := url.Parse(m.Target)
	if err != nil {
		return fail(start, FailInternal, "invalid URL: %v", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fail(start, FailInternal, "unsupported scheme %q: want http or https", target.Scheme)
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	method := strings.ToUpper(strings.TrimSpace(m.Method))
	if method == "" {
		method = http.MethodGet
	}

	var body io.Reader
	if m.Body != "" {
		body = strings.NewReader(m.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, m.Target, body)
	if err != nil {
		return fail(start, FailInternal, "build request: %v", err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "*/*")
	for k, v := range m.Headers {
		req.Header.Set(k, v)
	}

	client := c.client
	if !m.FollowRedirects {
		// Copy rather than mutate: the checker is shared across goroutines and
		// assigning to c.client.CheckRedirect here would be a data race that
		// silently changes behaviour for every other monitor.
		noRedirect := *c.client
		noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &noRedirect
	}

	resp, err := client.Do(req)
	if err != nil {
		return classifyRequestError(start, ctx, err)
	}
	defer func() {
		// Drain before closing so the connection can be reused; an undrained
		// body forces a new TCP handshake on every single check.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
		_ = resp.Body.Close()
	}()

	res := Result{
		OK:         true,
		StatusCode: resp.StatusCode,
		CheckedAt:  start,
	}

	// Report certificate expiry even on success, so the UI can warn ahead of
	// time instead of only once the certificate has already expired.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		res.CertExpiry = resp.TLS.PeerCertificates[0].NotAfter
	}

	if !matcher.Matches(resp.StatusCode) {
		res.Latency = time.Since(start)
		res.OK = false
		res.Kind = FailStatus
		res.Error = fmt.Sprintf("status %d, expected %s", resp.StatusCode, matcher)
		return res
	}

	if m.KeywordMode != "" && m.KeywordMode != KeywordIgnore && m.Keyword != "" {
		payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
		if err != nil {
			res.Latency = time.Since(start)
			res.OK = false
			res.Kind = FailConnection
			res.Error = fmt.Sprintf("read body: %v", err)
			return res
		}
		present := strings.Contains(string(payload), m.Keyword)

		switch m.KeywordMode {
		case KeywordMustContain:
			if !present {
				res.Latency = time.Since(start)
				res.OK = false
				res.Kind = FailKeyword
				res.Error = fmt.Sprintf("keyword %q not found in response", m.Keyword)
				return res
			}
		case KeywordMustNotContain:
			if present {
				res.Latency = time.Since(start)
				res.OK = false
				res.Kind = FailKeyword
				res.Error = fmt.Sprintf("keyword %q found in response", m.Keyword)
				return res
			}
		}
	}

	// Certificate about to expire fails the check, so it surfaces as an
	// incident rather than a detail nobody reads until the site breaks.
	if m.SSLWarnDays > 0 && !res.CertExpiry.IsZero() {
		remaining := time.Until(res.CertExpiry)
		if remaining < time.Duration(m.SSLWarnDays)*24*time.Hour {
			res.Latency = time.Since(start)
			res.OK = false
			res.Kind = FailCertExpiry
			if remaining <= 0 {
				res.Error = fmt.Sprintf("TLS certificate expired on %s", res.CertExpiry.Format(time.DateOnly))
			} else {
				res.Error = fmt.Sprintf("TLS certificate expires in %d days (on %s)",
					int(remaining.Hours()/24), res.CertExpiry.Format(time.DateOnly))
			}
			return res
		}
	}

	res.Latency = time.Since(start)
	return res
}

// classifyRequestError turns a transport error into a FailureKind.
//
// "Monitor is down" is not useful on its own. A DNS failure, a refused
// connection and an expired certificate are three different problems with three
// different fixes, and whoever gets paged needs to know which one they have.
func classifyRequestError(start time.Time, ctx context.Context, err error) Result {
	// The guard's own rejection must be reported plainly, or an admin will
	// spend an afternoon wondering why an internal URL never checks.
	if errors.Is(err, ErrPrivateTarget) {
		return fail(start, FailInternal,
			"refused to connect: %v (enable --allow-private-targets to monitor internal addresses)",
			unwrapMessage(err))
	}

	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return fail(start, FailTimeout, "timed out after %s", time.Since(start).Truncate(time.Millisecond))
	}
	if errors.Is(err, context.Canceled) {
		return fail(start, FailInternal, "check cancelled")
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return fail(start, FailDNS, "DNS lookup failed: host %s not found", dnsErr.Name)
		}
		return fail(start, FailDNS, "DNS lookup failed: %s", dnsErr.Err)
	}

	// Go wraps verification failures in CertificateVerificationError, so this
	// must be checked before the specific x509 types below — errors.As would
	// still find them, but the outer type carries the clearer message.
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		return fail(start, FailTLS, "TLS certificate verification failed: %v", unwrapMessage(verifyErr.Err))
	}

	var certErr *x509.CertificateInvalidError
	if errors.As(err, &certErr) {
		return fail(start, FailTLS, "TLS certificate invalid: %v", certErr)
	}
	var hostErr *x509.HostnameError
	if errors.As(err, &hostErr) {
		return fail(start, FailTLS, "TLS certificate does not match hostname: %v", hostErr)
	}
	var authErr *x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return fail(start, FailTLS, "TLS certificate signed by unknown authority")
	}
	var recordErr *tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return fail(start, FailTLS, "TLS handshake failed: %v", recordErr)
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		switch {
		case opErr.Timeout():
			return fail(start, FailTimeout, "connection timed out")
		case strings.Contains(opErr.Error(), "connection refused"):
			return fail(start, FailConnection, "connection refused")
		case strings.Contains(opErr.Error(), "no route to host"):
			return fail(start, FailConnection, "no route to host")
		case strings.Contains(opErr.Error(), "network is unreachable"):
			return fail(start, FailConnection, "network is unreachable")
		}
		return fail(start, FailConnection, "connection failed: %v", opErr.Err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fail(start, FailTimeout, "timed out after %s", time.Since(start).Truncate(time.Millisecond))
	}

	if strings.Contains(err.Error(), "stopped after 10 redirects") {
		return fail(start, FailConnection, "too many redirects")
	}

	return fail(start, FailConnection, "request failed: %v", unwrapMessage(err))
}

// unwrapMessage strips the noisy url.Error wrapper, which repeats the full URL
// in every message and pushes the actual cause off the end of the UI.
func unwrapMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}
