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

// maxBodyRead caps how much of a response body is read for keyword matching
// and JSON assertions.
//
// Without a cap, one monitored endpoint streaming an endless body would pin a
// worker and grow the heap until the process dies. 1 MiB is far more than any
// health endpoint needs.
const maxBodyRead = 1 << 20

// HTTPChecker probes HTTP and HTTPS endpoints.
//
// It is safe for concurrent use and is shared by every HTTP monitor. Sharing
// it does not share connections: each check dials its own, see
// NewHTTPChecker.
type HTTPChecker struct {
	client *http.Client
	guard  *Guard
	ua     string

	// transport is the shared transport, kept so a monitor asking for a
	// different TLS floor can be given a clone of it rather than a fresh one
	// built per check — see minVersionClient.
	transport *http.Transport

	// minVersionClients caches one client per non-default TLS floor. The
	// floor lives in the transport's tls.Config, so a monitor with another
	// floor needs a transport of its own; the clone keeps every other
	// setting, including the one that gives each check a fresh connection.
	minVersionMu      sync.Mutex
	minVersionClients map[uint16]*http.Client
}

// HTTPOptions configures NewHTTPChecker.
type HTTPOptions struct {
	// Guard enforces the SSRF policy. Required.
	Guard *Guard

	// UserAgent identifies SubGlance to the monitored service. Sites that
	// block unknown agents are a real support burden, and an honest agent
	// string lets an admin see who is polling them.
	UserAgent string
}

// NewHTTPChecker returns a checker whose every check opens its own connection.
//
// Keep-alive is off on purpose. A check that reuses a warm connection only
// proves that the server still answers on a socket that was already open, not
// that a visitor can reach it: a firewall change that blocks new connections,
// a DNS record pointing at a dead host or a broken TLS reload all leave an
// established connection working. A reused TLS session also keeps the
// certificate it was opened with, so a renewed certificate went unseen and
// its expiry warning stayed up until the connection happened to close. And
// latency over a warm connection leaves out the DNS lookup and the TCP and TLS
// handshakes a visitor waits for.
//
// The cost is one TCP and one TLS handshake per check: at 500 monitors on a
// 60-second interval, about eight a second, which is nothing for this host and
// little for the servers being checked.
func NewHTTPChecker(opts HTTPOptions) *HTTPChecker {
	if opts.Guard == nil {
		opts.Guard = NewGuard(false) // fail closed
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
		DialContext: dialer.DialContext,
		// One connection per request: HTTP/1.1 sends Connection: close and
		// hangs up after the response, HTTP/2 marks its connection single-use.
		// A redirect within one check dials again too, even to the same host.
		DisableKeepAlives:     true,
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
	scope := &headerScope{headers: m.Headers, ua: c.ua}
	res := c.check(ctx, m, scope)
	if !res.OK && scope.withheldFrom != "" && (res.Kind == FailStatus || res.Kind == FailKeyword || res.Kind == FailAssertion) {
		// A 401 on its own sends the reader to their credentials, which are
		// fine. Say which server went without them, and what to change.
		res.Error = fmt.Sprintf("%s (custom request headers were not sent to %s, "+
			"because a redirect left %s; point the monitor at the final URL if that server needs them)",
			res.Error, scope.withheldFrom, scope.origin)
	}
	return res
}

// headerScope keeps a monitor's own request headers on the origin they were
// configured for.
//
// Following a redirect copies every header to the next hop, and net/http only
// strips Authorization, Www-Authenticate and Cookie — and only when the host
// name changes, not the port or the scheme. A monitor carrying an API key in
// X-Api-Key would hand it to whichever server the target redirected to: an
// expired domain, a CDN, an identity provider. So every header the monitor
// set is dropped on a hop to another origin, not just the three the standard
// library knows are secret.
type headerScope struct {
	headers map[string]string
	ua      string

	// origin is the monitored URL's origin; withheldFrom is the other origin
	// the latest hop went to, empty while the request is on the monitored
	// origin. Each Check has its own scope, so these are never shared between
	// goroutines.
	origin       string
	withheldFrom string
}

// checkRedirect is the CheckRedirect for a monitor that follows redirects.
func (s *headerScope) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(s.headers) == 0 {
		return nil
	}
	if sameOrigin(via[0].URL, req.URL) {
		// Once a redirect has changed host, net/http strips Authorization and
		// Cookie from every later hop, including one back to the monitored
		// origin. Put the monitor's own headers back, and forget the other
		// origin: this hop is not missing anything.
		for k, v := range s.headers {
			req.Header.Set(k, v)
		}
		s.withheldFrom = ""
		return nil
	}
	for k := range s.headers {
		req.Header.Del(k)
	}
	// A monitor may override the two headers SubGlance sets itself. Dropping
	// the override must not leave the hop without them.
	if hasHeader(s.headers, "User-Agent") {
		req.Header.Set("User-Agent", s.ua)
	}
	if hasHeader(s.headers, "Accept") {
		req.Header.Set("Accept", "*/*")
	}
	// The latest hop is the one whose response a failure describes.
	s.origin = originOf(via[0].URL)
	s.withheldFrom = originOf(req.URL)
	return nil
}

// hasHeader reports whether headers names key, in any letter case.
func hasHeader(headers map[string]string, key string) bool {
	for k := range headers {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// originOf renders scheme://host:port with the default port filled in, so
// http://example.com and http://example.com:80 are one origin.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), defaultPort(scheme, u.Port()))
}

func defaultPort(scheme, port string) string {
	if port != "" {
		return port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// sameOrigin decides whether a redirect hop may carry the monitor's headers.
//
// One step beyond strict origin equality is allowed: http on the default port
// upgrading to https on the default port of the same host name. It is the
// most common legitimate redirect there is, and it only makes the connection
// safer. The reverse, https down to http, is a different origin.
func sameOrigin(from, to *url.URL) bool {
	if originOf(from) == originOf(to) {
		return true
	}
	fs, ts := strings.ToLower(from.Scheme), strings.ToLower(to.Scheme)
	return fs == "http" && ts == "https" &&
		strings.EqualFold(from.Hostname(), to.Hostname()) &&
		defaultPort(fs, from.Port()) == "80" && defaultPort(ts, to.Port()) == "443"
}

// check runs one HTTP probe with its headers confined to scope.
func (c *HTTPChecker) check(ctx context.Context, m Monitor, scope *headerScope) Result {
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
	} else if len(m.Headers) > 0 {
		// The same copy, for the same reason: the scope belongs to this check.
		scoped := *client
		scoped.CheckRedirect = scope.checkRedirect
		client = &scoped
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
	// Closed without reading the rest: the connection is never reused, so
	// there is nothing to drain it for, and reading on would only spend the
	// check's time on bytes no verdict depends on.
	defer func() { _ = resp.Body.Close() }()

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

	wantKeyword := m.KeywordMode != "" && m.KeywordMode != KeywordIgnore && m.Keyword != ""
	var (
		payload  []byte
		oversize bool
	)
	if wantKeyword || m.JSONAssertion != nil {
		// The keyword search keeps its old reach, the first maxBodyRead
		// bytes, and stops there: asking for one byte more would leave a
		// keyword-only check waiting on a stream that has sent exactly the
		// cap. The assertion does read one byte past the cap, so a body
		// that does not fit is known not to fit; a JSON document cut at the
		// cap is not a document, so it is refused instead of parsed half.
		limit := int64(maxBodyRead)
		if m.JSONAssertion != nil {
			limit++
		}
		buf, err := io.ReadAll(io.LimitReader(resp.Body, limit))
		if err != nil {
			res.Latency = time.Since(start)
			res.OK = false
			res.Kind = FailConnection
			// A body that stalls after its headers is a timeout, not a
			// broken connection, and is reported as one.
			var netErr net.Error
			if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded ||
				(errors.As(err, &netErr) && netErr.Timeout()) {
				res.Kind = FailTimeout
			}
			res.Error = fmt.Sprintf("read body: %v", err)
			return res
		}
		if len(buf) > maxBodyRead {
			buf, oversize = buf[:maxBodyRead], true
		}
		payload = buf
	}

	if wantKeyword {
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

	if a := m.JSONAssertion; a != nil {
		var msg string
		if oversize {
			msg = fmt.Sprintf("response body is larger than %d KiB, so %s was not checked; "+
				"point the monitor at a smaller health endpoint", maxBodyRead>>10, a.Path)
		} else {
			msg = evaluateJSONAssertion(*a, payload)
		}
		if msg != "" {
			res.Response = captureResponse(m, resp, payload)
			res.Latency = time.Since(start)
			res.OK = false
			res.Kind = FailAssertion
			res.Error = msg
			return res
		}
	}

	// A certificate about to expire passes, marked as expiring, so it
	// surfaces as a notice rather than a detail nobody reads until the site
	// breaks, without counting as downtime while the site answers. One that
	// has expired fails: visitors' browsers refuse it.
	if m.SSLWarnDays > 0 && !res.CertExpiry.IsZero() {
		remaining := time.Until(res.CertExpiry)
		if remaining < time.Duration(m.SSLWarnDays)*24*time.Hour {
			res.Latency = time.Since(start)
			res.Kind = FailCertExpiry
			if remaining <= 0 {
				res.OK = false
				res.Error = fmt.Sprintf("TLS certificate expired on %s", res.CertExpiry.Format(time.DateOnly))
			} else {
				res.Expiring = true
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
