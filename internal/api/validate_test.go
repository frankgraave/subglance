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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateTargetForType(tc.typ, tc.target)

			if tc.wantErr {
				if msg == "" {
					t.Fatalf("expected a validation error for %s target %q", tc.typ, tc.target)
				}
				if tc.wantMsg != "" && !contains(msg, tc.wantMsg) {
					t.Errorf("message %q should mention %q", msg, tc.wantMsg)
				}
				return
			}
			if msg != "" {
				t.Errorf("unexpected validation error: %s", msg)
			}
		})
	}
}

func TestValidateCreateMonitorRejectsBadTargets(t *testing.T) {
	// A TCP monitor without a port is the mistake people actually make.
	msg := validateCreateMonitor(createMonitorRequest{
		Name: "db", Type: "tcp", Target: "db.example.com",
	})
	if msg == "" {
		t.Error("expected a tcp monitor without a port to be rejected")
	}

	// And the same request with a port should be accepted.
	msg = validateCreateMonitor(createMonitorRequest{
		Name: "db", Type: "tcp", Target: "db.example.com:5432",
	})
	if msg != "" {
		t.Errorf("unexpected error: %s", msg)
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
