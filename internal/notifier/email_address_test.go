package notifier

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"

	"github.com/frankgraave/subglance/internal/state"
)

// recordingRelay is an SMTP server that accepts everything and keeps the
// envelope commands and the message headers it was sent, so a test can read
// what a real relay would have received.
type recordingRelay struct {
	host, port string

	ehlo string

	mu      sync.Mutex
	lines   []string
	headers []string
}

func startRecordingRelay(t *testing.T) *recordingRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return serveRelay(t, ln, "")
}

// serveRelay runs a recording relay on ln, which may be a TLS listener.
// ehlo is the reply to EHLO, set here rather than later so the relay's
// goroutines never race the test for it; empty means a bare "250 relay".
func serveRelay(t *testing.T, ln net.Listener, ehlo string) *recordingRelay {
	t.Helper()
	t.Cleanup(func() { _ = ln.Close() })
	r := &recordingRelay{ehlo: ehlo}
	r.host, r.port, _ = net.SplitHostPort(ln.Addr().String())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn)
		}
	}()
	return r
}

func (r *recordingRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	in := bufio.NewReader(conn)
	reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	reply("220 relay")
	inData, inHeaders := false, false
	for {
		l, err := in.ReadString('\n')
		if err != nil {
			return
		}
		l = strings.TrimRight(l, "\r\n")
		r.mu.Lock()
		switch {
		case inData && l == ".":
			inData = false
			reply("250 queued")
		case inData && l == "":
			inHeaders = false
		case inData && inHeaders:
			r.headers = append(r.headers, l)
		case inData:
		case strings.HasPrefix(l, "EHLO"):
			if r.ehlo != "" {
				reply(r.ehlo)
			} else {
				reply("250 relay")
			}
		case strings.HasPrefix(l, "AUTH "):
			r.lines = append(r.lines, l)
			reply("235 accepted")
		case l == "DATA":
			inData, inHeaders = true, true
			reply("354 go on")
		case l == "QUIT":
			reply("221 bye")
			r.mu.Unlock()
			return
		default:
			r.lines = append(r.lines, l)
			reply("250 ok")
		}
		r.mu.Unlock()
	}
}

// envelope is the MAIL and RCPT commands received, in order.
func (r *recordingRelay) envelope() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.lines {
		if strings.HasPrefix(l, "MAIL ") || strings.HasPrefix(l, "RCPT ") {
			out = append(out, l)
		}
	}
	return out
}

func (r *recordingRelay) header(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.headers {
		if strings.HasPrefix(h, name+": ") {
			return strings.TrimPrefix(h, name+": ")
		}
	}
	return ""
}

func sendThrough(t *testing.T, r *recordingRelay, from, to string) error {
	t.Helper()
	return NewEmailSender(nil).Send(context.Background(), map[string]string{
		"host": r.host, "port": r.port, "from": from, "to": to,
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
}

// A display name is how Uptime Kuma's form asks for the sender, so imported
// channels carry one. It used to go into the envelope as typed, as
// "MAIL FROM:<SubGlance <sg@example.com>>", which a strict relay refuses; the
// envelope has to carry the bare addresses and the headers the names.
func TestEmailEnvelopeCarriesBareAddressesAndHeadersKeepNames(t *testing.T) {
	r := startRecordingRelay(t)
	err := sendThrough(t, r, `"Uptime Kuma" <kuma@example.com>`,
		`Ops <ops@example.com>, "Doe, Jane" <jane@example.com>, oncall@example.com`)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	want := []string{
		"MAIL FROM:<kuma@example.com>",
		"RCPT TO:<ops@example.com>",
		"RCPT TO:<jane@example.com>",
		"RCPT TO:<oncall@example.com>",
	}
	if got := r.envelope(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("envelope = %q, want %q", got, want)
	}
	if got, want := r.header("From"), `"Uptime Kuma" <kuma@example.com>`; got != want {
		t.Errorf("From header = %q, want %q", got, want)
	}
	if got, want := r.header("To"), `"Ops" <ops@example.com>, "Doe, Jane" <jane@example.com>, oncall@example.com`; got != want {
		t.Errorf("To header = %q, want %q", got, want)
	}
}

// A channel written the plain way reads exactly as it did before addresses
// were parsed: SubGlance as the sender's name, the recipients as typed.
func TestEmailPlainAddressesAreSentAsBefore(t *testing.T) {
	r := startRecordingRelay(t)
	if err := sendThrough(t, r, " sg@example.com ", "a@example.com,b@example.com"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := []string{"MAIL FROM:<sg@example.com>", "RCPT TO:<a@example.com>", "RCPT TO:<b@example.com>"}
	if got := r.envelope(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("envelope = %q, want %q", got, want)
	}
	if got, want := r.header("From"), "SubGlance <sg@example.com>"; got != want {
		t.Errorf("From header = %q, want %q", got, want)
	}
	if got, want := r.header("To"), "a@example.com, b@example.com"; got != want {
		t.Errorf("To header = %q, want %q", got, want)
	}
}

// Every value below used to be saved, because it contains an @, and then
// either reach the relay as something that is not an address or fail every
// delivery inside net/smtp. The refusal has to name the field.
func TestEmailRefusesAddressesTheEnvelopeCannotCarry(t *testing.T) {
	tests := []struct{ key, value, want string }{
		{"from", "SubGlance", "from is not an e-mail address"},
		{"from", "sg@", "from is not an e-mail address"},
		{"from", "sg@@example.com", "from is not an e-mail address"},
		{"from", "a@example.com, b@example.com", "from takes one address, not a list"},
		{"to", "ops", "to is not an e-mail address"},
		{"to", "ops@", "to is not an e-mail address"},
		{"to", "@example.com", "to is not an e-mail address"},
		{"to", "ops@@example.com", "to is not an e-mail address"},
		{"to", "ops@example..com", "to is not an e-mail address"},
		{"to", "ops@example.com; oncall@example.com", "commas, not semicolons or line breaks"},
		{"to", "ops@example.com\noncall@example.com", "commas, not semicolons or line breaks"},
		{"to", "ops@example.com, oncall@", `to: "oncall@" is not an e-mail address`},
	}
	for _, tc := range tests {
		cfg := emailConfig("smtp.example.com")
		cfg[tc.key] = tc.value
		err := ValidateConfig("email", cfg)
		if err == nil {
			t.Errorf("%s %q: accepted, want a refusal containing %q", tc.key, tc.value, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %q: refusal %q, want it to contain %q", tc.key, tc.value, err, tc.want)
		}
		if got := ConfigKey(err); got != tc.key {
			t.Errorf("%s %q: refusal names key %q, want %s", tc.key, tc.value, got, tc.key)
		}
	}
}

// The other direction: every way of writing an address the relay can take
// is still accepted.
func TestEmailAcceptsAddressesTheEnvelopeCarries(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"from", "sg@example.com"},
		{"from", "SubGlance <sg@example.com>"},
		{"from", `"Uptime Kuma" <example@kuma.pet>`},
		{"from", "<sg@example.com>"},
		{"from", "  sg@example.com  "},
		{"from", "sg@localhost"},
		{"to", "ops@example.com"},
		{"to", "ops@example.com, oncall@example.com"},
		{"to", "ops@example.com,"},
		{"to", `"Doe, Jane" <jane@example.com>, ops@example.com`},
		{"to", "ops@[192.0.2.1]"},
		{"to", "ops@example.com (Ops)"},
	} {
		cfg := emailConfig("smtp.example.com")
		cfg[tc.key] = tc.value
		if err := ValidateConfig("email", cfg); err != nil {
			t.Errorf("%s %q: refused: %v", tc.key, tc.value, err)
		}
	}
}

// A stored value that does not parse, from before the rule or written to the
// database by hand, fails once and without a connection: it will not parse
// on the next attempt either.
func TestEmailWithAnUnparseableStoredAddressFailsWithoutRetry(t *testing.T) {
	dialled := false
	s := &EmailSender{dial: func(context.Context, string) (net.Conn, error) {
		dialled = true
		return nil, errors.New("not reached")
	}}
	for _, cfg := range []map[string]string{
		{"host": "smtp.example.com", "from": "SubGlance <sg@example.com>", "to": "ops@example.com; b@example.com"},
		{"host": "smtp.example.com", "from": "sg@", "to": "ops@example.com"},
	} {
		err := s.Send(context.Background(), cfg, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
		if err == nil {
			t.Fatalf("%v: delivered", cfg)
		}
		var r *Retryable
		if errors.As(err, &r) {
			t.Errorf("%v: reported as retryable: %v", cfg, err)
		}
	}
	if dialled {
		t.Error("a relay was dialled for an address that cannot be sent")
	}
}

// A display name is operator-supplied text that lands in a header, like a
// monitor name in the Subject. A line break in it must not start a header of
// its own.
func TestEmailDisplayNameCannotInjectAHeader(t *testing.T) {
	from := &mail.Address{Name: "SubGlance\r\nBcc: attacker@evil.test", Address: "sg@example.com"}
	to := []*mail.Address{{Name: "Ops\r\nBcc: attacker@evil.test", Address: "ops@example.com"}}
	msg := buildMessage(from, to, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
	if strings.Contains(msg, "\r\nBcc:") || strings.Contains(msg, "\nBcc:") {
		t.Fatalf("a line break in a display name injected a header:\n%s", msg)
	}
}
