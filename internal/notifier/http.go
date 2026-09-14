package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// httpSend performs one outbound delivery and classifies the outcome.
//
// Every HTTP-based channel funnels through here so that retry classification
// is decided once. Getting it wrong in five places would mean a 404 retried
// forever on one channel and a 503 given up on immediately on another.
func httpSend(ctx context.Context, client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, checker.ErrPrivateTarget) {
			// The guard refused the address. Nothing about that
			// improves on the sixth attempt, and retrying would
			// turn a blocked probe into a slow one instead of a
			// failed one. Report it permanently so the operator
			// sees the real reason in the outbox.
			return fmt.Errorf("delivery blocked: %w", err)
		}
		// A transport error is a network that is not working right now:
		// DNS, connection refused, timeout. All worth another try.
		return retryable("%w", err)
	}
	defer func() {
		// Drain a bounded amount so the connection can be reused. The
		// limit is what stops a hostile or broken endpoint from feeding
		// a stream that would be read forever.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil

	case resp.StatusCode == http.StatusTooManyRequests:
		// Being told to slow down is the clearest retryable signal there
		// is; the backoff schedule already does what is being asked.
		return retryable("rate limited by the endpoint (429)")

	case resp.StatusCode >= 500:
		return retryable("endpoint returned %d", resp.StatusCode)

	default:
		// 4xx other than 429 is this request being wrong — a bad URL, a
		// revoked token, a malformed body. Retrying cannot fix any of
		// them, and pretending otherwise delays the moment the operator
		// learns the channel is misconfigured.
		body := readSnippet(resp.Body)
		if body != "" {
			return fmt.Errorf("endpoint rejected the alert (%d): %s", resp.StatusCode, body)
		}
		return fmt.Errorf("endpoint rejected the alert (%d)", resp.StatusCode)
	}
}

// readSnippet returns the first line of a response body, for error messages.
//
// Bounded and single-line on purpose: this text ends up in a UI field and in a
// log, and an endpoint that answers with an HTML error page should not fill
// either of them.
func readSnippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 200))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// jsonRequest builds a POST with a JSON body.
func jsonRequest(ctx context.Context, target string, payload any) (*http.Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// userAgent identifies SubGlance to receiving endpoints, so an operator
// reading their own access log can tell what called them.
const userAgent = "SubGlance/1 (+https://github.com/frankgraave/subglance)"

// validateHTTPSURL checks a configured URL before it is ever called.
//
// Rejecting at configuration time rather than at 03:00 is the whole point:
// a webhook URL with a typo should fail while the person who typed it is
// still looking at the screen.
func validateHTTPSURL(raw, field string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is required", field)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", field, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must be an http or https URL", field)
	}
	if u.Host == "" {
		return fmt.Errorf("%s has no host", field)
	}
	return nil
}

// defaultTimeout bounds one delivery attempt.
//
// Ten seconds: long enough for a slow webhook receiver, short enough that a
// hanging endpoint does not hold a worker slot while a backlog builds behind
// it. A delivery that needs longer than this is failing, it just has not said
// so yet.
const defaultTimeout = 10 * time.Second

// newHTTPClient builds the client the HTTP channels share.
//
// Redirects are not followed. A webhook endpoint that answers with a redirect
// is either misconfigured or trying to send the alert somewhere the operator
// did not name, and quietly following it would hide both.
//
// The guard is the same one the checkers use, for the same reason: a channel
// URL is operator-supplied text that this process then connects to, which is
// the identical exposure a monitor target has. Without it, write access to
// channels is write access to the host network — a webhook pointed at
// 169.254.169.254 would read cloud credentials and post them to the outbox's
// error column. A nil guard means no restriction; that is what tests and a
// deployment without the checker pipeline get.
func newHTTPClient(guard *checker.Guard) *http.Client {
	dialer := &net.Dialer{Timeout: defaultTimeout}
	if guard != nil {
		// Control runs after DNS resolution and before connect(2), so
		// it judges the address the kernel is about to reach. Checking
		// the hostname instead would leave a DNS-rebinding window.
		dialer.Control = guard.ControlFunc()
	}

	return &http.Client{
		Timeout: defaultTimeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
