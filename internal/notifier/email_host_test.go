package notifier

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

func emailConfig(host string) map[string]string {
	return map[string]string{"host": host, "from": "sg@example.com", "to": "ops@example.com"}
}

// Every host below used to be saved and then fail each delivery before a
// connection was made, reported as "no such host" or "missing port". The
// refusal has to name the host field and say what to change.
func TestEmailRefusesAHostEveryDeliveryRefuses(t *testing.T) {
	tests := []struct{ host, want string }{
		{"smtp.example.com:2525", "set port to 2525 instead"},
		{"[::1]:25", "set port to 25 instead"},
		{"smtp.example.com:", "must not end with a colon"},
		{"smtp://smtp.example.com", "not a URL"},
		{"[::1]", "without brackets"},
		{"smtp..example.com", "empty label"},
		{"-smtp.example.com", "starts with a hyphen"},
		{"999.1.1.1", "not a valid IP address"},
		{"smtp.example.com/", `contains "/"`},
		{"smtp example.com", `contains " "`},
		{"smtp.ex\u00e4mple.com", "xn-- form"},
		{"relay.onion", "Tor onion address"},
	}
	for _, tc := range tests {
		err := ValidateConfig("email", emailConfig(tc.host))
		if err == nil {
			t.Errorf("host %q: accepted, want a refusal containing %q", tc.host, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("host %q: refusal %q, want it to contain %q", tc.host, err, tc.want)
		}
		if !strings.HasPrefix(err.Error(), "host") {
			t.Errorf("host %q: refusal %q does not start with the key it is about", tc.host, err)
		}
		if got := ConfigKey(err); got != "host" {
			t.Errorf("host %q: refusal names key %q, want host", tc.host, got)
		}
	}
}

// The other direction: every way of writing a relay that the dialer reaches
// is still accepted, padding included, because Send trims it as well.
func TestEmailAcceptsAHostTheDialerReaches(t *testing.T) {
	for _, host := range []string{
		"smtp.example.com",
		"smtp.example.com.",
		"mail",
		"postfix_relay",
		"xn--exmple-cua.com",
		"192.0.2.25",
		"2001:db8::25",
		"::1",
		"fe80::1%eth0",
		" smtp.example.com ",
	} {
		if err := ValidateConfig("email", emailConfig(host)); err != nil {
			t.Errorf("host %q: refused: %v", host, err)
		}
	}
}

// Validate judges the trimmed host, so Send has to dial the trimmed host: a
// padded host that was saved must reach the relay, not be looked up with its
// spaces as part of the name.
func TestEmailSendDialsTheTrimmedHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		accepted <- struct{}{}
		_, _ = conn.Write([]byte("421 go away\r\n"))
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	cfg := emailConfig(" " + host + "\t")
	cfg["port"] = port
	if err := ValidateConfig("email", cfg); err != nil {
		t.Fatalf("padded host refused: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = NewEmailSender(nil).Send(ctx, cfg, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})

	select {
	case <-accepted:
	default:
		t.Fatalf("the relay was never dialled: %v", err)
	}
}
