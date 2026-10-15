package api

import "testing"

// Creating a monitor whose target cannot possibly work for its type should
// fail while the user is still looking at the form, not silently produce a
// monitor that errors forever.
func TestValidateTargetForType(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		target  string
		wantErr bool
		wantMsg string
	}{
		// HTTP
		{name: "http with https url", typ: "http", target: "https://example.com"},
		{name: "http with http url", typ: "http", target: "http://example.com/health"},
		{name: "http without scheme", typ: "http", target: "example.com", wantErr: true, wantMsg: "http://"},
		{name: "http with tcp scheme", typ: "http", target: "tcp://example.com:80", wantErr: true},

		// TCP
		{name: "tcp with port", typ: "tcp", target: "db.example.com:5432"},
		{name: "tcp ipv6 with port", typ: "tcp", target: "[2001:db8::1]:5432"},
		{name: "tcp without port", typ: "tcp", target: "db.example.com", wantErr: true, wantMsg: "port"},
		{name: "tcp with bad port", typ: "tcp", target: "db.example.com:99999", wantErr: true},

		// SSL — a port is optional because 443 is the obvious default.
		{name: "ssl bare host", typ: "ssl", target: "example.com"},
		{name: "ssl with port", typ: "ssl", target: "example.com:8443"},
		{name: "ssl with https url", typ: "ssl", target: "https://example.com"},
		{name: "ssl with bad port", typ: "ssl", target: "example.com:0", wantErr: true},

		// Ping — a port means the user wanted a TCP check.
		{name: "ping bare host", typ: "ping", target: "example.com"},
		{name: "ping ip", typ: "ping", target: "192.0.2.10"},
		{name: "ping with port", typ: "ping", target: "example.com:80", wantErr: true, wantMsg: "tcp monitor"},
		// A URL in a ping monitor used to be rejected for "using a port" —
		// which the user never typed. The message must name the real mistake.
		{name: "ping with url", typ: "ping", target: "http://example.com", wantErr: true, wantMsg: "not a URL"},
		{name: "ping with url and path", typ: "ping", target: "https://example.com/health", wantErr: true, wantMsg: "example.com"},
		// A whitespace-only target passed every check and was persisted: the
		// "target is required" guard above only rejects the empty string, and
		// the ping case discarded ParseHostPort's error. The result was a
		// monitor that failed its lookup forever, which is what this function
		// exists to prevent.
		{name: "ping whitespace only", typ: "ping", target: " ", wantErr: true, wantMsg: "hostname or IP"},
		{name: "ping tab only", typ: "ping", target: "\t", wantErr: true, wantMsg: "hostname or IP"},
		{name: "ping with space in host", typ: "ping", target: "a b.example.com", wantErr: true, wantMsg: "spaces"},
		{name: "ping with nonbreaking space", typ: "ping", target: "a\u00a0b.example.com", wantErr: true, wantMsg: "spaces"},
		{name: "ping with em space", typ: "ping", target: "a\u2003b.example.com", wantErr: true, wantMsg: "spaces"},
		{name: "ping with narrow nonbreaking space", typ: "ping", target: "a\u202fb.example.com", wantErr: true, wantMsg: "spaces"},
		{name: "ping with ideographic space", typ: "ping", target: "a\u3000b.example.com", wantErr: true, wantMsg: "spaces"},
		{name: "ping with surrounding Unicode whitespace", typ: "ping", target: "\u00a0example.com\u2003"},
		// A path, query, fragment or credentials would be silently dropped by
		// the parser, storing a target that does not say what it does.
		{name: "ping with path", typ: "ping", target: "example.com/health", wantErr: true, wantMsg: "nothing after it"},
		{name: "ping with query", typ: "ping", target: "example.com?x=1", wantErr: true, wantMsg: "nothing after it"},
		{name: "ping with fragment", typ: "ping", target: "example.com#top", wantErr: true, wantMsg: "nothing after it"},
		{name: "ping with credentials", typ: "ping", target: "user@example.com", wantErr: true, wantMsg: "nothing after it"},
		// Still accepted: the shapes a ping monitor is actually for. A target
		// with surrounding whitespace is a paste, not a mistake.
		{name: "ping host with surrounding whitespace", typ: "ping", target: "  example.com  "},
		{name: "ping ipv6", typ: "ping", target: "2001:db8::1"},

		// A hostname the resolver refuses without asking a server. Saved, it
		// failed every check as "host not found", which reads as an outage of
		// a real host instead of a typo in the form. The full rule is tested
		// in internal/checker; these prove every type applies it.
		{name: "http empty label", typ: "http", target: "https://exa..mple.com/health", wantErr: true, wantMsg: "empty label"},
		{name: "http bad character", typ: "http", target: "https://exa!mple.com/", wantErr: true, wantMsg: `contains "!"`},
		{name: "http leading hyphen", typ: "http", target: "http://-example.com", wantErr: true, wantMsg: "starts with a hyphen"},
		{name: "http short ipv4", typ: "http", target: "http://10.0.1/", wantErr: true, wantMsg: "not a valid IP address"},
		// net/url takes any digits as a port; the dial refuses these on
		// every check, as a tcp monitor's target already is at save.
		{name: "http port zero", typ: "http", target: "http://example.com:0/", wantErr: true, wantMsg: "port 0 is out of range"},
		{name: "http port out of range", typ: "http", target: "https://example.com:65536/", wantErr: true, wantMsg: "port 65536 is out of range"},
		{name: "http ipv6 port out of range", typ: "http", target: "http://[2001:db8::1]:70000/", wantErr: true, wantMsg: "port 70000 is out of range"},
		{name: "http highest port", typ: "http", target: "http://example.com:65535/"},
		{name: "http empty port", typ: "http", target: "http://example.com:/"},
		{name: "tcp empty label", typ: "tcp", target: "exa..mple.com:443", wantErr: true, wantMsg: "empty label"},
		{name: "tcp out-of-range octet", typ: "tcp", target: "999.1.1.1:80", wantErr: true, wantMsg: "not a valid IP address"},
		{name: "tcp trailing hyphen", typ: "tcp", target: "db-.example.com:5432", wantErr: true, wantMsg: "ends with a hyphen"},
		{name: "ssl bad character", typ: "ssl", target: "exa!mple.com", wantErr: true, wantMsg: `contains "!"`},
		{name: "ssl url with empty label", typ: "ssl", target: "https://exa..mple.com/path", wantErr: true, wantMsg: "empty label"},
		{name: "ping empty label", typ: "ping", target: "exa_mple..com", wantErr: true, wantMsg: "empty label"},
		{name: "ping leading hyphen", typ: "ping", target: "-x.com", wantErr: true, wantMsg: "starts with a hyphen"},
		// tcp, ssl and ping hand the name to the resolver as written, and it
		// does not convert an internationalised name; net/http does.
		{name: "tcp unicode host", typ: "tcp", target: "b\u00fccher.de:443", wantErr: true, wantMsg: "xn--"},
		{name: "ssl unicode host", typ: "ssl", target: "b\u00fccher.de", wantErr: true, wantMsg: "xn--"},
		{name: "ping unicode host", typ: "ping", target: "b\u00fccher.de", wantErr: true, wantMsg: "xn--"},
		{name: "http unicode host", typ: "http", target: "https://b\u00fccher.de/"},
		{name: "tcp punycode host", typ: "tcp", target: "xn--bcher-kva.de:443"},
		// Kept: shapes that look odd and work.
		{name: "tcp single label", typ: "tcp", target: "db:5432"},
		{name: "tcp underscore", typ: "tcp", target: "my_service.internal:8080"},
		{name: "ssl trailing dot", typ: "ssl", target: "example.com.:443"},
		{name: "http bracketed ipv6", typ: "http", target: "http://[2001:db8::1]:8080/"},
		{name: "http ipv6 with zone", typ: "http", target: "http://[fe80::1%25eth0]/"},
		{name: "ping localhost", typ: "ping", target: "localhost"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validateTargetForType(tc.typ, tc.target)

			if tc.wantErr {
				if p.ok() {
					t.Fatalf("expected a validation error for %s target %q", tc.typ, tc.target)
				}
				if tc.wantMsg != "" && !contains(p.msg, tc.wantMsg) {
					t.Errorf("message %q should mention %q", p.msg, tc.wantMsg)
				}
				// Every rejection here is about the target the caller typed,
				// including the ones worded as a complaint about the type.
				// A form has one input to highlight and this is it.
				if p.field != "target" {
					t.Errorf("field = %q, want \"target\" for %s target %q", p.field, tc.typ, tc.target)
				}
				return
			}
			if !p.ok() {
				t.Errorf("unexpected validation error: %s", p.msg)
			}
		})
	}
}

func TestValidateCreateMonitorRejectsBadTargets(t *testing.T) {
	// A TCP monitor without a port is the mistake people actually make.
	p := validateCreateMonitor(createMonitorRequest{
		Name: "db", Type: "tcp", Target: "db.example.com",
	})
	if p.ok() {
		t.Error("expected a tcp monitor without a port to be rejected")
	}
	if p.field != "target" {
		t.Errorf("field = %q, want \"target\"", p.field)
	}

	// And the same request with a port should be accepted.
	p = validateCreateMonitor(createMonitorRequest{
		Name: "db", Type: "tcp", Target: "db.example.com:5432",
	})
	if !p.ok() {
		t.Errorf("unexpected error: %s", p.msg)
	}

	// A whitespace-only ping target reached the store: the emptiness guard
	// compares against "" and the ping case threw away the parser's error.
	// Asserted through validateCreateMonitor rather than validateTargetForType
	// because the bug lived in the gap between the two checks, and only the
	// whole path proves the gap is closed.
	p = validateCreateMonitor(createMonitorRequest{
		Name: "gateway", Type: "ping", Target: " ",
	})
	if p.ok() {
		t.Error("expected a whitespace-only ping target to be rejected")
	}
	if p.field != "target" {
		t.Errorf("field = %q, want \"target\"", p.field)
	}
}

// Each validator must name the field it rejected, and the name must be the
// JSON one the client sent — "ssl_warn_days", not "SSLWarnDays". A client
// looks the input up by that string; a Go field name finds nothing.
func TestValidatorsNameTheirField(t *testing.T) {
	warnDays := 0
	tests := []struct {
		name string
		req  createMonitorRequest
		want string
	}{
		{"missing name", createMonitorRequest{Type: "http", Target: "https://example.com"}, "name"},
		{"missing target", createMonitorRequest{Name: "a", Type: "http"}, "target"},
		{"missing type", createMonitorRequest{Name: "a", Target: "https://example.com"}, "type"},
		{"unknown type", createMonitorRequest{Name: "a", Type: "gopher", Target: "x"}, "type"},
		{"interval out of range", createMonitorRequest{
			Name: "a", Type: "http", Target: "https://example.com", IntervalS: 5,
		}, "interval_s"},
		{"timeout out of range", createMonitorRequest{
			Name: "a", Type: "http", Target: "https://example.com", TimeoutS: 999,
		}, "timeout_s"},
		{"bad method", createMonitorRequest{
			Name: "a", Type: "http", Target: "https://example.com", Method: "FETCH",
		}, "method"},
		{"bad keyword mode", createMonitorRequest{
			Name: "a", Type: "http", Target: "https://example.com", KeywordMode: "maybe",
		}, "keyword_mode"},
		{"ssl warn days out of range", createMonitorRequest{
			Name: "a", Type: "http", Target: "https://example.com", SSLWarnDays: &warnDays,
		}, "ssl_warn_days"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validateCreateMonitor(tc.req)
			if p.ok() {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
			if p.field != tc.want {
				t.Errorf("field = %q, want %q (message: %s)", p.field, tc.want, p.msg)
			}
		})
	}
}

// A problem that is not about a field must not claim one. An empty string and
// "absent" are the same value once this reaches JavaScript, so bodyProblem has
// to leave the key out entirely rather than send "".
func TestBodyProblemHasNoField(t *testing.T) {
	p := bodyProblem("invalid JSON")
	if p.field != "" {
		t.Errorf("field = %q, want empty", p.field)
	}
	if p.ok() {
		t.Error("a problem with a message must not report ok")
	}
	if !(problem{}).ok() {
		t.Error("the zero problem must report ok")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}
