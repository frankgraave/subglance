package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/state"
)

// capturedRequest is what a fake push server saw.
type capturedRequest struct {
	method, path string
	header       http.Header
	body         map[string]any
}

// pushServer is an in-process stand-in for ntfy or Gotify. It records the one
// request it receives and answers with the given status.
func pushServer(t *testing.T, status int) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.header = r.Method, r.URL.Path, r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got.body); err != nil {
			t.Errorf("body is not JSON: %v: %s", err, b)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func downAlert() Alert {
	return Alert{
		MonitorName: "api",
		Target:      "https://api.example",
		Event:       string(state.EventIncidentConfirmed),
		Cause:       "status 503",
		At:          time.Date(2026, 9, 28, 3, 12, 0, 0, time.UTC),
	}
}

func upAlert() Alert {
	a := downAlert()
	a.Event = string(state.EventIncidentResolved)
	a.StartedAt = a.At.Add(-5 * time.Minute)
	return a
}

// TestNtfyPublishesJSONToTheServerRoot checks the request shape ntfy's JSON
// publishing expects: a POST to the root with the topic in the body, not in
// the path.
func TestNtfyPublishesJSONToTheServerRoot(t *testing.T) {
	srv, got := pushServer(t, http.StatusOK)
	s := NewNtfySender(nil)
	cfg := map[string]string{"url": srv.URL, "topic": "homelab-alerts", "token": "tk_secret"}
	if err := s.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := s.Send(context.Background(), cfg, downAlert()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got.method != http.MethodPost || got.path != "/" {
		t.Errorf("request = %s %s, want POST /", got.method, got.path)
	}
	if got.body["topic"] != "homelab-alerts" {
		t.Errorf("topic = %v, want homelab-alerts", got.body["topic"])
	}
	if got.body["title"] != "api is down" {
		t.Errorf("title = %v, want the monitor name in it", got.body["title"])
	}
	if msg, _ := got.body["message"].(string); !strings.Contains(msg, "status 503") {
		t.Errorf("message = %q, want the cause in it", msg)
	}
	if got.body["priority"] != float64(ntfyPriorityDown) {
		t.Errorf("priority = %v, want %d for an outage", got.body["priority"], ntfyPriorityDown)
	}
	if tags, _ := got.body["tags"].([]any); len(tags) != 1 || tags[0] != "rotating_light" {
		t.Errorf("tags = %v, want [rotating_light]", got.body["tags"])
	}
	if h := got.header.Get("Authorization"); h != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want the access token as a bearer token", h)
	}
}

func TestNtfyRecoveryIsDefaultPriority(t *testing.T) {
	srv, got := pushServer(t, http.StatusOK)
	cfg := map[string]string{"url": srv.URL, "topic": "t", "username": "me", "password": "pw"}
	if err := NewNtfySender(nil).Send(context.Background(), cfg, upAlert()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.body["priority"] != float64(ntfyPriorityUp) {
		t.Errorf("priority = %v, want %d for a recovery", got.body["priority"], ntfyPriorityUp)
	}
	if msg, _ := got.body["message"].(string); !strings.Contains(msg, "Down for 5 minutes") {
		t.Errorf("message = %q, want the outage duration", msg)
	}
	user, pass, ok := (&http.Request{Header: got.header}).BasicAuth()
	if !ok || user != "me" || pass != "pw" {
		t.Errorf("basic auth = %q/%q (%v), want me/pw", user, pass, ok)
	}
}

func TestNtfyValidate(t *testing.T) {
	s := NewNtfySender(nil)
	bad := map[string]map[string]string{
		"no topic":              {},
		"topic with a slash":    {"topic": "a/b"},
		"topic with a space":    {"topic": "a b"},
		"non-http server":       {"topic": "t", "url": "ftp://push.example"},
		"token and basic auth":  {"topic": "t", "token": "x", "username": "u", "password": "p"},
		"username, no password": {"topic": "t", "username": "u"},
		"whitespace token":      {"topic": "t", "token": "  \t"},
		"topic path in server":  {"topic": "t", "url": "https://push.example/alerts"},
		"query on server":       {"topic": "t", "url": "https://push.example/?x=1"},
	}
	for name, cfg := range bad {
		if err := s.Validate(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The server is optional: the public ntfy server is the default.
	if err := s.Validate(map[string]string{"topic": "alerts_1"}); err != nil {
		t.Errorf("topic alone rejected: %v", err)
	}
	// The server root is accepted with or without its trailing slash.
	for _, server := range []string{"https://push.example", "https://push.example/"} {
		if err := s.Validate(map[string]string{"topic": "t", "url": server}); err != nil {
			t.Errorf("server %q rejected: %v", server, err)
		}
	}
	// Whitespace inside credentials is left alone: ntfy passwords may hold it.
	if err := s.Validate(map[string]string{"topic": "t", "username": "u", "password": " p w "}); err != nil {
		t.Errorf("password with spaces rejected: %v", err)
	}
}

// TestGotifyPostsToMessageEndpoint checks Gotify's request shape: POST
// /message with the application token in X-Gotify-Key, never in the URL.
func TestGotifyPostsToMessageEndpoint(t *testing.T) {
	srv, got := pushServer(t, http.StatusOK)
	s := NewGotifySender(nil)
	cfg := map[string]string{"url": srv.URL + "/gotify/", "token": "AppTok3n", "priority_down": "9"}
	if err := s.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := s.Send(context.Background(), cfg, downAlert()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The path prefix survives: a Gotify behind a reverse proxy under a
	// sub-path is the common case.
	if got.method != http.MethodPost || got.path != "/gotify/message" {
		t.Errorf("request = %s %s, want POST /gotify/message", got.method, got.path)
	}
	if got.header.Get("X-Gotify-Key") != "AppTok3n" {
		t.Errorf("X-Gotify-Key = %q, want the app token", got.header.Get("X-Gotify-Key"))
	}
	if got.body["title"] != "api is down" {
		t.Errorf("title = %v", got.body["title"])
	}
	if got.body["priority"] != float64(9) {
		t.Errorf("priority = %v, want the configured 9", got.body["priority"])
	}
}

func TestGotifyDefaultPriorities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alert Alert
		want  int
	}{
		{"down", downAlert(), gotifyPriorityDown},
		{"up", upAlert(), gotifyPriorityUp},
	} {
		srv, got := pushServer(t, http.StatusOK)
		cfg := map[string]string{"url": srv.URL, "token": "t"}
		if err := NewGotifySender(nil).Send(context.Background(), cfg, tc.alert); err != nil {
			t.Fatalf("%s: Send: %v", tc.name, err)
		}
		if got.body["priority"] != float64(tc.want) {
			t.Errorf("%s: priority = %v, want %d", tc.name, got.body["priority"], tc.want)
		}
		if got.path != "/message" {
			t.Errorf("%s: path = %q, want /message", tc.name, got.path)
		}
	}
}

func TestGotifyValidate(t *testing.T) {
	s := NewGotifySender(nil)
	bad := map[string]map[string]string{
		"no url":            {"token": "t"},
		"no token":          {"url": "https://push.example"},
		"priority too high": {"url": "https://push.example", "token": "t", "priority_down": "11"},
		"priority not int":  {"url": "https://push.example", "token": "t", "priority_up": "high"},
	}
	for name, cfg := range bad {
		if err := s.Validate(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestGotifyRejectionIsNotRetried: a 401 means the token is wrong, and six
// attempts over half an hour would only delay the operator finding out.
func TestGotifyRejectionIsNotRetried(t *testing.T) {
	srv, _ := pushServer(t, http.StatusUnauthorized)
	err := NewGotifySender(nil).Send(context.Background(),
		map[string]string{"url": srv.URL, "token": "wrong"}, downAlert())
	if err == nil {
		t.Fatal("a 401 was reported as delivered")
	}
	var r *Retryable
	if errors.As(err, &r) {
		t.Errorf("a 401 was marked retryable: %v", err)
	}
}

// TestPushChannelsOnAPrivateAddressNameTheWayOut is the error the ticket asks
// for: the typical self-hosted ntfy or Gotify is on the LAN, the guard refuses
// it by default, and the refusal has to say which address, why, and which
// setting permits it. The fake server listens on loopback, which the guard
// refuses exactly like a 192.168 address.
func TestPushChannelsOnAPrivateAddressNameTheWayOut(t *testing.T) {
	srv, _ := pushServer(t, http.StatusOK)
	guard := checker.NewGuard(false)

	for name, send := range map[string]func() error{
		"ntfy": func() error {
			return NewNtfySender(guard).Send(context.Background(),
				map[string]string{"url": srv.URL, "topic": "t"}, downAlert())
		},
		"gotify": func() error {
			return NewGotifySender(guard).Send(context.Background(),
				map[string]string{"url": srv.URL, "token": "t"}, downAlert())
		},
	} {
		err := send()
		if err == nil {
			t.Fatalf("%s: delivered to loopback with the guard on", name)
		}
		if !errors.Is(err, checker.ErrPrivateTarget) {
			t.Errorf("%s: error does not unwrap to the guard's: %v", name, err)
		}
		msg := err.Error()
		for _, want := range []string{"127.0.0.1", "loopback", "--allow-private-targets", "SUBGLANCE_ALLOW_PRIVATE_TARGETS"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: error %q does not mention %q", name, msg, want)
			}
		}
		var r *Retryable
		if errors.As(err, &r) {
			t.Errorf("%s: a refused address was marked retryable", name)
		}
	}

	// And with the opt-in, the same delivery goes through.
	allow := checker.NewGuard(true)
	if err := NewGotifySender(allow).Send(context.Background(),
		map[string]string{"url": srv.URL, "token": "t"}, downAlert()); err != nil {
		t.Errorf("gotify with --allow-private-targets: %v", err)
	}
	if err := NewNtfySender(allow).Send(context.Background(),
		map[string]string{"url": srv.URL, "topic": "t"}, downAlert()); err != nil {
		t.Errorf("ntfy with --allow-private-targets: %v", err)
	}
}
