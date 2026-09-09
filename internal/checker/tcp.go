package checker

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TCPChecker probes whether a port accepts connections.
//
// This is the check for everything that speaks a protocol we do not: a
// database, an SMTP server, a game server, a message broker. It answers one
// question — can a client complete a TCP handshake — and deliberately sends no
// payload afterwards. Speaking a protocol badly is a good way to end up in
// someone's fail2ban rules.
type TCPChecker struct {
	dialer *net.Dialer
}

// NewTCPChecker returns a checker that enforces the SSRF guard at dial time.
func NewTCPChecker(guard *Guard) *TCPChecker {
	if guard == nil {
		guard = NewGuard(false) // fail closed
	}
	return &TCPChecker{
		dialer: &net.Dialer{
			Control: guard.ControlFunc(),
			// No KeepAlive: the connection is closed immediately.
		},
	}
}

// Check opens a TCP connection and closes it again.
func (c *TCPChecker) Check(ctx context.Context, m Monitor) Result {
	start := time.Now()

	host, port, err := ParseHostPort(m.Target, 0)
	if err != nil {
		return fail(start, FailInternal, "%v", err)
	}
	if port == 0 {
		return fail(start, FailInternal, "no port in target %q: a TCP check needs host:port", m.Target)
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
	// Close before measuring: the handshake is what we timed, and leaving the
	// socket open would hold a file descriptor for every monitor on the
	// interval boundary.
	_ = conn.Close()

	return ok(start, 0)
}

// ParseHostPort splits a check target into host and port.
//
// It accepts the shapes people actually type, because rejecting a target on a
// technicality is a support ticket:
//
//	db.example.com:5432     host and port
//	example.com             host only, port comes from defaultPort
//	tcp://example.com:5432  scheme is ignored
//	[::1]:5432              bracketed IPv6
//	https://example.com     scheme implies 443 when defaultPort is 0
//
// A defaultPort of 0 means "no default": the caller decides whether a missing
// port is an error.
func ParseHostPort(target string, defaultPort int) (host string, port int, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0, fmt.Errorf("target is empty")
	}

	// A scheme tells us the conventional port, which is friendlier than
	// refusing an https:// URL on an SSL monitor.
	if i := strings.Index(target, "://"); i >= 0 {
		scheme := strings.ToLower(target[:i])
		target = target[i+3:]

		if defaultPort == 0 {
			switch scheme {
			case "https", "wss":
				defaultPort = 443
			case "http", "ws":
				defaultPort = 80
			}
		}
	}

	// Drop anything after the authority: a path or query has no meaning for a
	// TCP or TLS check, and silently ignoring it beats an error.
	if i := strings.IndexAny(target, "/?#"); i >= 0 {
		target = target[:i]
	}
	// Credentials in the authority are not our business either.
	if i := strings.LastIndex(target, "@"); i >= 0 {
		target = target[i+1:]
	}
	if target == "" {
		return "", 0, fmt.Errorf("no host in target")
	}

	// Bracketed IPv6, with or without a port.
	if strings.HasPrefix(target, "[") {
		end := strings.Index(target, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("unbalanced brackets in target %q", target)
		}
		host = target[1:end]
		rest := target[end+1:]
		if rest == "" {
			return host, defaultPort, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("unexpected %q after IPv6 address", rest)
		}
		port, err = parsePort(rest[1:])
		return host, port, err
	}

	// A bare IPv6 address has several colons and no port.
	if strings.Count(target, ":") > 1 {
		return target, defaultPort, nil
	}

	if h, p, splitErr := net.SplitHostPort(target); splitErr == nil {
		port, err = parsePort(p)
		if err != nil {
			return "", 0, err
		}
		if h == "" {
			return "", 0, fmt.Errorf("no host in target")
		}
		return h, port, nil
	}

	return target, defaultPort, nil
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d out of range 1-65535", n)
	}
	return n, nil
}

// hostFromURL returns the hostname of a URL-shaped target, or the target
// itself when it does not parse as one.
func hostFromURL(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Hostname()
	}
	host, _, err := ParseHostPort(target, 0)
	if err != nil {
		return target
	}
	return host
}
