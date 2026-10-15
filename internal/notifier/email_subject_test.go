package notifier

import (
	"context"
	"mime"
	"net/mail"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/state"
)

// headerLines is the raw header block of a rendered message, one entry per
// physical line, continuation lines included.
func headerLines(t *testing.T, msg string) []string {
	t.Helper()
	head, _, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatalf("no blank line ends the header:\n%s", msg)
	}
	return strings.Split(head, "\r\n")
}

// subjectLines is the Subject header as it stands in the message: its first
// line and every folded continuation.
func subjectLines(t *testing.T, msg string) []string {
	t.Helper()
	var out []string
	in := false
	for _, l := range headerLines(t, msg) {
		switch {
		case strings.HasPrefix(l, "Subject: "):
			in = true
		case in && strings.HasPrefix(l, " "):
		default:
			in = false
		}
		if in {
			out = append(out, l)
		}
	}
	return out
}

func asciiOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// A monitor name outside ASCII used to land in the Subject as raw UTF-8. It
// now travels as RFC 2047 encoded words that decode back to the title
// exactly, on header lines no longer than the 76 characters the RFC allows.
func TestEmailSubjectOutsideASCIIIsEncodedAndDecodesBack(t *testing.T) {
	names := []string{
		"Bücherei Zürich — API",
		"東京データセンター",
		"🔥 checkout",
		"café_api? = yes",
		strings.Repeat("ü", 100),
		strings.Repeat("Zürich ", 14),
		// Plain ASCII that a reader would take for an encoded word: a
		// client would show "x is down" if it went out as typed.
		"=?utf-8?q?x?=",
	}
	from := &mail.Address{Address: "sg@example.com"}
	to := []*mail.Address{{Address: "ops@example.com"}}
	for _, name := range names {
		a := Alert{MonitorName: name, Event: string(state.EventIncidentConfirmed)}
		msg := buildMessage(from, to, a)

		lines := subjectLines(t, msg)
		if len(lines) == 0 {
			t.Fatalf("%q: no Subject header:\n%s", name, msg)
		}
		for _, l := range lines {
			if !asciiOnly(l) {
				t.Errorf("%q: Subject line is not ASCII: %q", name, l)
			}
			if len(l) > 76 {
				t.Errorf("%q: Subject line is %d characters, over 76: %q", name, len(l), l)
			}
		}
		if !strings.HasPrefix(lines[0], "Subject: =?utf-8?q?") {
			t.Errorf("%q: Subject is not an encoded word: %q", name, lines[0])
		}

		m, err := mail.ReadMessage(strings.NewReader(msg))
		if err != nil {
			t.Fatalf("%q: message does not parse: %v", name, err)
		}
		got, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
		if err != nil {
			t.Fatalf("%q: Subject does not decode: %v", name, err)
		}
		if got != a.Title() {
			t.Errorf("%q: Subject decodes to %q, want %q", name, got, a.Title())
		}
	}
}

// An ASCII title is written exactly as before: a receiver may be a filter
// rule matching on the subject.
func TestEmailASCIISubjectIsWrittenAsTyped(t *testing.T) {
	a := Alert{MonitorName: "api (eu-west)", Event: string(state.EventIncidentConfirmed)}
	msg := buildMessage(&mail.Address{Address: "sg@example.com"}, []*mail.Address{{Address: "ops@example.com"}}, a)
	if got := subjectLines(t, msg); len(got) != 1 || got[0] != "Subject: api (eu-west) is down" {
		t.Errorf("Subject = %q, want [\"Subject: api (eu-west) is down\"]", got)
	}
}

// What the relay receives is what decides a bounce: with ASCII addresses,
// every header line the real sender writes, names and subject alike, is
// ASCII. An address outside ASCII cannot be encoded and still needs SMTPUTF8.
func TestEmailEveryHeaderOnTheWireIsASCII(t *testing.T) {
	r := startRecordingRelay(t)
	err := NewEmailSender(nil).Send(context.Background(), map[string]string{
		"host": r.host, "port": r.port,
		"from": "Équipe SubGlance <sg@example.com>",
		"to":   "Zoë <ops@example.com>, oncall@example.com",
	}, Alert{MonitorName: "Bücherei Zürich", Event: string(state.EventIncidentConfirmed)})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	r.mu.Lock()
	headers := append([]string(nil), r.headers...)
	r.mu.Unlock()
	if len(headers) == 0 {
		t.Fatal("the relay received no headers")
	}
	for _, h := range headers {
		if !asciiOnly(h) {
			t.Errorf("header line on the wire is not ASCII: %q", h)
		}
	}
	if s := r.header("Subject"); !strings.HasPrefix(s, "=?utf-8?q?") {
		t.Errorf("Subject on the wire = %q, want an encoded word", s)
	}
}

// A body is 7bit unless it says otherwise (RFC 2045). Grouped alerts and
// digests carry bullets and a monitor name may carry accents, so such a body
// says 8bit; an ASCII body gains no header.
func TestEmailBodyOutsideASCIIIsLabelled8bit(t *testing.T) {
	from := &mail.Address{Address: "sg@example.com"}
	to := []*mail.Address{{Address: "ops@example.com"}}
	cases := []struct {
		name string
		a    Alert
		want string
	}{
		{"ascii", Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)}, ""},
		{"accented name", Alert{MonitorName: "Bücherei", Event: string(state.EventIncidentConfirmed)}, "8bit"},
		{"grouped", Alert{GroupedNames: []string{"api", "web"}, Event: string(state.EventIncidentConfirmed)}, "8bit"},
	}
	for _, c := range cases {
		m, err := mail.ReadMessage(strings.NewReader(buildMessage(from, to, c.a)))
		if err != nil {
			t.Fatalf("%s: message does not parse: %v", c.name, err)
		}
		if got := m.Header.Get("Content-Transfer-Encoding"); got != c.want {
			t.Errorf("%s: Content-Transfer-Encoding = %q, want %q", c.name, got, c.want)
		}
	}
}
