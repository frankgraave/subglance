package watchdog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The parts of a watchdog URL that grant access to the switch. A test that
// finds any of them in a log line or an error has found the credential.
var urlSecrets = []string{"pa55word", "secret-uuid-1234", "k3y-value", "fr4g"}

func assertNoURLSecret(t *testing.T, where, text string) {
	t.Helper()
	for _, secret := range urlSecrets {
		if strings.Contains(text, secret) {
			t.Errorf("%s contains %q:\n%s", where, secret, text)
		}
	}
}

// lockedBuffer is a log sink a test can read while nothing writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// closedAddr is a loopback address nothing listens on, so a ping to it fails
// at once with a refused connection and never leaves the machine.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func TestLogNeverShowsTheWatchdogURLInFull(t *testing.T) {
	addr := closedAddr(t)
	raw := "http://user:pa55word@" + addr + "/ping/secret-uuid-1234?key=k3y-value#fr4g"
	var out lockedBuffer
	checks := uint64(0)
	w, err := New(Options{
		URL:      raw,
		Interval: time.Hour,
		Liveness: func() Liveness {
			checks++
			return Liveness{ChecksCompleted: checks, Scheduled: 1}
		},
		Log: slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// An alive ping that fails, then the enabled line and the stopped ping
	// Run writes on its way in and out.
	w.tick(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)

	logged := out.String()
	assertNoURLSecret(t, "the log", logged)
	for _, want := range []string{
		`msg="watchdog enabled" url=http://` + addr + "/…",
		`msg="watchdog ping failed" event=alive`,
		`msg="watchdog ping failed" event=stopped`,
		// The cause is what an operator diagnoses from, and it stays.
		`Post \"http://` + addr + `/…\": dial tcp ` + addr,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %s:\n%s", want, logged)
		}
	}
	if got := w.Snapshot().LastResult; got == nil || *got != "transport_error" {
		t.Errorf("last result = %v, want transport_error: the shown URL must not change how a failure is classed", got)
	}
}

func TestLoggableKeepsTheCauseOfAnyURLError(t *testing.T) {
	w := &Watchdog{shown: shownURL("https://hc-ping.com/secret-uuid-1234")}
	cause := errors.New("net/http: invalid header field value")
	err := w.loggable(&url.Error{Op: "Post", URL: "https://hc-ping.com/secret-uuid-1234", Err: cause})
	assertNoURLSecret(t, "the error", err.Error())
	if want := `Post "https://hc-ping.com/…": net/http: invalid header field value`; err.Error() != want {
		t.Errorf("loggable = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Error("loggable lost the cause, which errors.Is must still find")
	}
	plain := errors.New("no URL in here")
	if got := w.loggable(plain); !errors.Is(got, plain) || got.Error() != plain.Error() {
		t.Errorf("loggable(%v) = %v, want it unchanged", plain, got)
	}
}

func TestValidateURLRefusalsDoNotQuoteTheToken(t *testing.T) {
	for _, tc := range []struct {
		raw, reason string
	}{
		{"https://user:pa55word@hc-ping.com:99999/secret-uuid-1234", `"https://hc-ping.com:99999/…": port 99999 is out of range`},
		{"https://hc-ping.com:0/secret-uuid-1234?key=k3y-value", `"https://hc-ping.com:0/…": port 0 is out of range`},
		{"https://hc-p!ng.com/secret-uuid-1234", `"https://hc-p!ng.com/…": hostname "hc-p!ng.com"`},
		{"https://hc-ping.com:abc/secret-uuid-1234", `"https://hc-ping.com:abc/…" is not a URL: invalid port ":abc" after host`},
		// The parser reads "pa55" as a port and would quote it back in
		// its reason, so only the shape is given.
		{"https://user:pa55word/x@hc-ping.com/secret-uuid-1234", `"https://***@hc-ping.com/…" is not a URL`},
		{"ftp://user:pa55word@hc-ping.com/secret-uuid-1234", `"ftp://hc-ping.com/…": want an http or https URL`},
		{"hc-ping.com/secret-uuid-1234", `"hc-ping.com/…": want an http or https URL`},
		{"https:///secret-uuid-1234", `"https:///…": missing host`},
	} {
		err := ValidateURL(tc.raw)
		if err == nil {
			t.Errorf("ValidateURL(%q) = nil, want a refusal", tc.raw)
			continue
		}
		assertNoURLSecret(t, "the refusal of "+tc.raw, err.Error())
		if want := "watchdog-url " + tc.reason; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("ValidateURL(%q) = %q, want it to start %q", tc.raw, err, want)
		}
	}
}

func TestShownURLKeepsSchemeAndHostOnly(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hc-ping.com/secret-uuid-1234":                 "https://hc-ping.com/…",
		"https://hc-ping.com":                                  "https://hc-ping.com",
		"https://hc-ping.com/":                                 "https://hc-ping.com/",
		"https://hc-ping.com?key=k3y-value":                    "https://hc-ping.com/…",
		"https://hc-ping.com/#fr4g":                            "https://hc-ping.com/…",
		"http://user:pa55word@[2001:db8::1]:8080/ping?k=v":     "http://[2001:db8::1]:8080/…",
		"http://subglance-b:3000/api/v1/push/secret-uuid-1234": "http://subglance-b:3000/…",
		"https://user:pa55word/x@hc-ping.com/secret-uuid-1234": "https://***@hc-ping.com/…",
		"hc-ping.com/secret-uuid-1234":                         "hc-ping.com/…",
	} {
		if got := shownURL(raw); got != want {
			t.Errorf("shownURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
