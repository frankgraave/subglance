package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// These tests cover the two ways a credential used to escape: a config key
// nobody had added to a mask list, and a token that travels in a URL path.
//
// Both were found by reading the code with an attacker's eye rather than by a
// failure in production, which is why each test states the attack rather than
// the implementation: a future refactor should keep the promise, not the
// mechanism.

// TestUnusualConfigKeysAreMasked is the deny-by-default proof.
//
// The mask used to be an allowlist of secret key names over a config that
// accepts any key, so anything unanticipated leaked. A viewer is the
// least-trusted role and channels are readable by it, which made this the
// shortest path from read access to someone else's credentials.
func TestUnusualConfigKeysAreMasked(t *testing.T) {
	srv, db := testServerWithDB(t)

	const (
		bearer = "Bearer SUPER-SECRET-KEY"
		hmac   = "hmac-signing-key"
	)

	ch, err := db.CreateChannel(context.Background(), store.Channel{
		Name: "ops",
		Type: store.ChannelWebhook,
		Config: map[string]string{
			"url": "https://example.test/hook",
			// Neither of these was on the old secret list, and
			// neither is a name the code knows about.
			"authorization": bearer,
			"secret":        hmac,
			// Public: where it goes, not what proves the right.
			"username": "subglance",
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/channels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for name, secret := range map[string]string{
		"authorization": bearer,
		"secret":        hmac,
	} {
		if strings.Contains(body, secret) {
			t.Errorf("config key %q leaked its value verbatim: %s", name, body)
		}
	}

	// And the public key is still readable, or the interface would be
	// showing asterisks where a destination should be.
	if !strings.Contains(body, "subglance") {
		t.Error("a public config value was masked; the interface cannot show where alerts go")
	}

	_ = ch
}

// TestMaskedValueRoundTripsForAnyKey guards the other half: a client that
// reads a channel and writes it back must not destroy the credential it never
// saw. The round-trip rule has to follow the same deny-by-default set as the
// mask, or an unusual key would be saved as its own asterisks.
func TestMaskedValueRoundTripsForAnyKey(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := context.Background()

	const secret = "Bearer SUPER-SECRET-KEY"
	ch, err := db.CreateChannel(ctx, store.Channel{
		Name:    "ops",
		Type:    store.ChannelWebhook,
		Config:  map[string]string{"url": "https://example.test/hook", "authorization": secret},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Read it as a client would, then write back exactly what was read.
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/channels/"+itoa(ch.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec.Code)
	}

	var got struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"name":   got.Name,
		"type":   got.Type,
		"config": got.Config,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+itoa(ch.ID), string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	stored, err := db.GetChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if stored.Config["authorization"] != secret {
		t.Errorf("credential became %q after a read-modify-write; the mask was saved as a literal",
			stored.Config["authorization"])
	}
}

// TestPushTokenIsNotLogged covers the credential that travels in a URL path.
//
// The token is the only control on the push endpoint and is never stored in
// the clear, which made logging it the one hole in otherwise careful handling.
// Debug logging is a documented setting, so this is not an obscure path.
func TestPushTokenIsNotLogged(t *testing.T) {
	const token = "sgu_SUPERSECRETTOKENVALUE12345"

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv, _ := testServerWithDB(t)
	srv.log = log

	req := httptest.NewRequest(http.MethodPost, "/api/v1/push/"+token, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if strings.Contains(logged.String(), token) {
		t.Fatalf("the push token was written to the log:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "/api/v1/push/{token}") {
		t.Errorf("the redacted path is not recognisable in the log:\n%s", logged.String())
	}
}

// TestRedactPathLeavesOtherPathsAlone keeps the redaction narrow. A monitor
// target is watched, not secret, and blanking ordinary paths would make the
// log useless for the debugging it exists for.
func TestRedactPathLeavesOtherPathsAlone(t *testing.T) {
	cases := map[string]string{
		"/api/v1/monitors":        "/api/v1/monitors",
		"/api/v1/monitors/42":     "/api/v1/monitors/42",
		"/api/v1/push/":           "/api/v1/push/",
		"/api/v1/push/sgu_abc123": "/api/v1/push/{token}",
		"/":                       "/",
	}

	for in, want := range cases {
		if got := redactPath(in); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}
