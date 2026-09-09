// Package checker runs the actual monitoring probes.
//
// Every check type implements Checker, so adding one — DNS, gRPC, a browser
// check — means adding a file here and nothing elsewhere.
//
// Checkers are stateless and safe for concurrent use: the scheduler runs many
// at once from a bounded worker pool, and each call gets its own context and
// deadline.
package checker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Type identifies a check implementation.
type Type string

const (
	TypeHTTP Type = "http"
	TypeTCP  Type = "tcp"
	TypePing Type = "ping"
	TypeSSL  Type = "ssl"
)

// KeywordMode says how the Keyword field should be interpreted.
type KeywordMode string

const (
	// KeywordIgnore does not inspect the body at all.
	KeywordIgnore KeywordMode = "absent_ok"
	// KeywordMustContain fails the check when the keyword is missing.
	KeywordMustContain KeywordMode = "must_contain"
	// KeywordMustNotContain fails the check when the keyword is present.
	// Useful for catching error pages that still return HTTP 200.
	KeywordMustNotContain KeywordMode = "must_not_contain"
)

// Monitor is everything a checker needs to run one probe.
//
// It is a value type on purpose: the scheduler hands a copy to each worker, so
// a check can never mutate shared state.
type Monitor struct {
	ID     int64
	Name   string
	Type   Type
	Target string

	Timeout time.Duration

	// HTTP-specific.
	Method          string
	ExpectedStatus  string // e.g. "200", "200-299", "200,301,302"
	Keyword         string
	KeywordMode     KeywordMode
	FollowRedirects bool
	Headers         map[string]string
	Body            string

	// SSLWarnDays sets how many days before certificate expiry a check starts
	// failing. Zero disables the check.
	SSLWarnDays int
}

// FailureKind classifies why a check failed.
//
// The state engine and the notifier use this to say something better than
// "monitor is down": a DNS failure and a 500 are different problems and the
// person being paged needs to know which one they have.
type FailureKind string

const (
	FailNone       FailureKind = ""
	FailDNS        FailureKind = "dns"
	FailConnection FailureKind = "connection"
	FailTLS        FailureKind = "tls"
	FailTimeout    FailureKind = "timeout"
	FailStatus     FailureKind = "status"
	FailKeyword    FailureKind = "keyword"
	FailCertExpiry FailureKind = "cert_expiry"
	FailInternal   FailureKind = "internal"
)

// Result is the outcome of a single check.
type Result struct {
	OK      bool
	Latency time.Duration

	// StatusCode is the HTTP status, or 0 for non-HTTP checks.
	StatusCode int

	// Kind classifies the failure; empty when OK.
	Kind FailureKind

	// Error is a human-readable failure description, safe to show in the UI.
	// Empty when OK.
	Error string

	// CertExpiry is the certificate's expiry time for TLS-capable checks,
	// zero otherwise. Reported even when the check passes so the UI can warn
	// ahead of time.
	CertExpiry time.Time

	// CheckedAt is when the probe started.
	CheckedAt time.Time
}

// Checker runs one kind of probe.
//
// Implementations must respect ctx, must not panic, and must return a Result
// rather than an error for check failures: a failing check is normal operation,
// not an exceptional condition.
type Checker interface {
	Check(ctx context.Context, m Monitor) Result
}

// fail builds a failed Result.
func fail(start time.Time, kind FailureKind, format string, args ...any) Result {
	return Result{
		OK:        false,
		Latency:   time.Since(start),
		Kind:      kind,
		Error:     fmt.Sprintf(format, args...),
		CheckedAt: start,
	}
}

// ok builds a successful Result.
func ok(start time.Time, statusCode int) Result {
	return Result{
		OK:         true,
		Latency:    time.Since(start),
		StatusCode: statusCode,
		CheckedAt:  start,
	}
}

// StatusMatcher decides whether an HTTP status code is acceptable.
type StatusMatcher struct {
	spec   string
	ranges []statusRange
}

type statusRange struct{ lo, hi int }

// ParseStatusMatcher parses an expected-status specification.
//
// Accepted forms, comma-separated and combinable:
//
//	200          exactly 200
//	200-299      any 2xx
//	200,301,302  any of these
//	2xx          shorthand for 200-299
//
// An empty spec defaults to 200-299, which is what almost everyone means.
func ParseStatusMatcher(spec string) (StatusMatcher, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "200-299"
	}

	m := StatusMatcher{spec: spec}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Shorthand: 2xx, 3XX, ...
		if len(part) == 3 && strings.EqualFold(part[1:], "xx") {
			d, err := strconv.Atoi(part[:1])
			if err != nil || d < 1 || d > 5 {
				return StatusMatcher{}, fmt.Errorf("invalid status shorthand %q", part)
			}
			m.ranges = append(m.ranges, statusRange{d * 100, d*100 + 99})
			continue
		}

		lo, hi, found := strings.Cut(part, "-")
		if !found {
			code, err := strconv.Atoi(part)
			if err != nil {
				return StatusMatcher{}, fmt.Errorf("invalid status %q", part)
			}
			if code < 100 || code > 599 {
				return StatusMatcher{}, fmt.Errorf("status %d out of range 100-599", code)
			}
			m.ranges = append(m.ranges, statusRange{code, code})
			continue
		}

		loN, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return StatusMatcher{}, fmt.Errorf("invalid status range start %q", lo)
		}
		hiN, err := strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			return StatusMatcher{}, fmt.Errorf("invalid status range end %q", hi)
		}
		if loN > hiN {
			return StatusMatcher{}, fmt.Errorf("status range %d-%d is inverted", loN, hiN)
		}
		if loN < 100 || hiN > 599 {
			return StatusMatcher{}, fmt.Errorf("status range %d-%d out of range 100-599", loN, hiN)
		}
		m.ranges = append(m.ranges, statusRange{loN, hiN})
	}

	if len(m.ranges) == 0 {
		return StatusMatcher{}, fmt.Errorf("status spec %q matches nothing", spec)
	}
	return m, nil
}

// Matches reports whether code is acceptable.
func (m StatusMatcher) Matches(code int) bool {
	for _, r := range m.ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}
	return false
}

// String returns the original specification.
func (m StatusMatcher) String() string { return m.spec }
