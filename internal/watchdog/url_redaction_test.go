package watchdog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

// refusingTransport fails every request the way a switch's transport can:
// with a *url.Error of its own that quotes the full request URL, around a
// refused dial. http.Client.Do then wraps it in its own *url.Error, so the
// URL is in the chain twice. No socket is opened, so the short suite stays
// hermetic.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: refusedAddr(req.URL.Host),
		Err:  errors.New("connection refused"),
	}}
}

// refusedAddr is a dial address as net.OpError prints it.
type refusedAddr string

func (a refusedAddr) Network() string { return "tcp" }
func (a refusedAddr) String() string  { return string(a) }

func TestLogNeverShowsTheWatchdogURLInFull(t *testing.T) {
	const addr = "hc-ping.example:8443"
	raw := "http://user:pa55word@" + addr + "/ping/secret-uuid-1234?key=k3y-value#fr4g"
	var out lockedBuffer
	checks := uint64(0)
	w, err := New(Options{
		URL:      raw,
		Client:   &http.Client{Transport: refusingTransport{}},
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
		// The cause is what an operator diagnoses from, and it stays,
		// below both the client's *url.Error and the transport's own.
		`Post \"http://` + addr + `/…\": Post \"http://` + addr + `/…\": dial tcp ` + addr + ": connection refused",
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
	const secret = "https://user:pa55word@hc-ping.com/secret-uuid-1234?key=k3y-value"
	cause := errors.New("net/http: invalid header field value")
	inner := &url.Error{Op: "Post", URL: secret, Err: cause}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"the client's own", &url.Error{Op: "Post", URL: secret, Err: cause},
			`Post "https://hc-ping.com/…": net/http: invalid header field value`},
		// http.Client.Do wraps a transport's *url.Error in another.
		{"nested", &url.Error{Op: "Post", URL: "https://hc-ping.com/secret-uuid-1234", Err: inner},
			`Post "https://hc-ping.com/…": Post "https://hc-ping.com/…": net/http: invalid header field value`},
		// A transport's wrapper that is not a *url.Error itself.
		{"wrapped", &url.Error{Op: "Post", URL: secret, Err: fmt.Errorf("proxy for %s: %w", secret, inner)},
			`Post "https://hc-ping.com/…": proxy for https://hc-ping.com/…: Post "https://hc-ping.com/…": net/http: invalid header field value`},
		{"wrapped from the top", fmt.Errorf("ping: %w", inner),
			`ping: Post "https://hc-ping.com/…": net/http: invalid header field value`},
	} {
		got := loggable(tc.err)
		assertNoURLSecret(t, "the "+tc.name+" error", got.Error())
		if got.Error() != tc.want {
			t.Errorf("%s: loggable = %q, want %q", tc.name, got.Error(), tc.want)
		}
		if !errors.Is(got, cause) {
			t.Errorf("%s: loggable lost the cause, which errors.Is must still find", tc.name)
		}
	}
	plain := errors.New("no URL in here")
	if got := loggable(plain); !errors.Is(got, plain) || got.Error() != plain.Error() {
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
		// A port that does not parse may be a password whose "@" was
		// left out, so neither the address nor the reason quotes it.
		{"https://hc-ping.com:abc/secret-uuid-1234", `"https://hc-ping.com:***/…" is not a URL: invalid port after host`},
		{"https://user:pa55word/secret-uuid-1234", `"https://user:***/…" is not a URL: invalid port after host`},
		{"https://user:pa55word?key=k3y-value", `"https://user:***/…" is not a URL: invalid port after host`},
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
		if strings.Contains(err.Error(), "invalid port \"") {
			t.Errorf("ValidateURL(%q) = %q, which quotes the text the parser took for a port", tc.raw, err)
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
		"https://user:pa55word/secret-uuid-1234":               "https://user:***/…",
		"https://hc-ping.com:abc/secret-uuid-1234":             "https://hc-ping.com:***/…",
		"http://[2001:db8::1]:pa55word/x":                      "http://[2001:db8::1]:***/…",
	} {
		if got := shownURL(raw); got != want {
			t.Errorf("shownURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
