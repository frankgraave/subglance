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
	"sync"
	"time"
)

// MaxSnapshotBytes caps how much of a failed response body is kept.
//
// This is a diagnostic prefix, not a guarantee that the cause fits. The
// synthetic prefix and SQLite measurements in docs/response-snapshot-sizing.md
// record the 1/2/4 KiB tradeoff. Headers cost additional bytes; neither this
// body limit nor the runner's per-outage allowance caps total retained bytes.
const MaxSnapshotBytes = 2048

// snapshotHeaders is the allowlist of response headers kept with a snapshot.
//
// An allowlist, never the whole set. Set-Cookie and Authorization would put a
// live session token in a database that every user with read access can query,
// and "capture everything except the ones I thought of" fails the first time a
// service invents a header.
//
// Each of these answers a triage question: what did it send, who sent it, when
// should I retry, and which request was it — the identifier you would quote to
// the other side's support desk.
var snapshotHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Server",
	"Date",
	"Retry-After",
	"Location",
	"X-Request-Id",
	"X-Correlation-Id",
	"Cf-Ray",
}

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

	// transport is the shared pool, kept so a monitor asking for a different
	// TLS floor can be given a clone of it rather than a fresh one built per
	// check — see minVersionClient.
	transport *http.Transport

	// minVersionClients caches one client per non-default TLS floor. A
	// monitor with a lowered floor must not share a connection pool with the
	// default one: http.Transport keys idle connections on host and scheme,
	// not on tls.Config, so a reused connection would silently carry the
	// wrong negotiated terms.
	minVersionMu      sync.Mutex
	minVersionClients map[uint16]*http.Client
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
		// Stated rather than left to the default so that the https path and
		// the ssl path negotiate on identical terms; a monitor that asks for
		// another floor gets its own transport in Check.
		TLSClientConfig: &tls.Config{MinVersion: minTLSVersion}, //nolint:gosec // minTLSVersion is TLS 1.2
	}

	c := &HTTPChecker{
		guard:             opts.Guard,
		ua:                opts.UserAgent,
		transport:         transport,
		minVersionClients: make(map[uint16]*http.Client),
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
	if v := effectiveMinTLSVersion(m); v != minTLSVersion {
		client = c.minVersionClient(v)
	}
	if !m.FollowRedirects {
		// Copy rather than mutate: the checker is shared across goroutines and
		// assigning to c.client.CheckRedirect here would be a data race that
		// silently changes behaviour for every other monitor.
		noRedirect := *client
		noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &noRedirect
	}

	resp, err := client.Do(req)
	if err != nil {
		// A negotiation that failed over ciphers or versions is a TLS fault,
		// not a network one. classifyRequestError sees only the net.OpError
		// underneath and would report "connection failed", which sends the
		// reader to their firewall for a server that is plainly answering.
		if res, handled := classifyTLSHandshakeError(start, err, m); handled {
			return res
		}
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
		// Capture before stamping the latency: reading the first 2 KiB is
		// part of the check's cost and hiding it would make the recorded
		// latency disagree with the time the check actually took.
		res.Response = captureResponse(m, resp, nil)
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
				res.Response = captureResponse(m, resp, payload)
				res.Latency = time.Since(start)
				res.OK = false
				res.Kind = FailKeyword
				res.Error = fmt.Sprintf("keyword %q not found in response", m.Keyword)
				return res
			}
		case KeywordMustNotContain:
			if present {
				res.Response = captureResponse(m, resp, payload)
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

// captureResponse keeps the beginning of a failed response, when the monitor
// asked for it.
//
// payload is the body if the caller has already read it — the keyword check
// has — and nil otherwise. Reading it twice is not possible: the body is a
// stream, and a second read would return nothing and quietly store an empty
// snapshot next to a failure that did have a body.
func captureResponse(m Monitor, resp *http.Response, payload []byte) *ResponseSnapshot {
	if !m.CaptureResponse || resp == nil {
		return nil
	}

	var (
		raw       []byte
		truncated bool
	)
	if payload != nil {
		raw = payload
		if len(raw) > MaxSnapshotBytes {
			raw, truncated = raw[:MaxSnapshotBytes], true
		}
	} else {
		// One byte past the cap, so a body of exactly MaxSnapshotBytes is not
		// reported as truncated while a longer one is.
		buf, err := io.ReadAll(io.LimitReader(resp.Body, MaxSnapshotBytes+1))
		if err != nil && len(buf) == 0 {
			// A body that cannot be read is not a second failure worth
			// reporting: the check has already failed for its own reason.
			return nil
		}
		raw = buf
		if len(raw) > MaxSnapshotBytes {
			raw, truncated = raw[:MaxSnapshotBytes], true
		}
	}

	snap := &ResponseSnapshot{
		Body:      sanitiseBody(raw),
		Truncated: truncated,
	}

	for _, name := range snapshotHeaders {
		if v := resp.Header.Get(name); v != "" {
			if snap.Headers == nil {
				snap.Headers = make(map[string]string, len(snapshotHeaders))
			}
			snap.Headers[http.CanonicalHeaderKey(name)] = v
		}
	}

	if !snap.informative() {
		// Nothing was learned. An empty row would still cost a write and
		// would show the UI a "response" panel with nothing in it.
		return nil
	}
	return snap
}

// informative reports whether a snapshot says anything the heartbeat does not.
//
// An empty body is not automatically worthless: `Retry-After: 120` or a
// request id on a bodyless 503 is real triage material. What is worthless is
// an empty body whose only header restates that emptiness, which is what a
// bodyless error response from net/http produces — `Content-Length: 0`.
func (s *ResponseSnapshot) informative() bool {
	if s.Body != "" {
		return true
	}
	for name := range s.Headers {
		if name != "Content-Length" {
			return true
		}
	}
	return false
}

// sanitiseBody makes arbitrary bytes safe to store in a STRICT TEXT column.
//
// A monitored endpoint may answer with anything — a gzip frame, an image, a
// latin-1 error page — and the cut above can leave a trailing partial
// character. Invalid bytes are dropped rather than replaced: a truncated
// snapshot that ends in U+FFFD reads as corruption in the monitored service's
// response, when it is only an artefact of where the cut landed.
func sanitiseBody(b []byte) string {
	return strings.ToValidUTF8(string(b), "")
}

// minVersionClient returns the client for a monitor that asked for a TLS floor
// other than the default, building it on first use.
//
// The transport is cloned so the monitor keeps every other transport setting —
// the SSRF-guarded dialer above all — and differs only in the one field it
// asked to differ in.
func (c *HTTPChecker) minVersionClient(v uint16) *http.Client {
	c.minVersionMu.Lock()
	defer c.minVersionMu.Unlock()

	if cl, ok := c.minVersionClients[v]; ok {
		return cl
	}

	tr := c.transport.Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{} //nolint:gosec // MinVersion set on the next line
	}
	tr.TLSClientConfig.MinVersion = v

	cl := &http.Client{Transport: tr, CheckRedirect: c.client.CheckRedirect}
	c.minVersionClients[v] = cl
	return cl
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

	// Go wraps verification failures in CertificateVerificationError, which
	// carries the chain the server actually presented — the one thing the raw
	// message does not have and that the diagnosis needs. It must be checked
	// before the specific x509 types below: errors.As would still find them,
	// but only the outer type has the certificates.
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		return fail(start, FailTLS, "%s",
			describeCertProblem("", verifyErr.UnverifiedCertificates, verifyErr.Err, time.Now()))
	}

	// The same x509 failures reaching us unwrapped — a custom VerifyConnection
	// hook, or a path that verified by hand. No chain in hand here, so
	// describeCertProblem can only word the error itself.
	var certErr x509.CertificateInvalidError
	if errors.As(err, &certErr) {
		return fail(start, FailTLS, "%s", describeCertProblem("", nil, certErr, time.Now()))
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return fail(start, FailTLS, "%s", describeCertProblem("", nil, hostErr, time.Now()))
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return fail(start, FailTLS, "%s", describeCertProblem("", nil, authErr, time.Now()))
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
