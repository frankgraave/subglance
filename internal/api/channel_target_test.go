package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
)

// A notification channel is an outbound request SubGlance makes from its own
// network position to a destination the user typed — the same exposure a
// monitor target has. The delivery side already refuses blocked addresses at
// dial time. These tests cover the save side, whose job is to say so early and
// to name the field that is wrong.
//
// # How these tests avoid DNS
//
// checker.Guard.CheckHost short-circuits on an address literal: it parses the
// host with netip.ParseAddr and checks that, never reaching the resolver. So
// every test here that uses the real guard uses a literal, which makes them
// hermetic — no resolver, no network, no flake, and nothing to skip under
// -short.
//
// The behaviour that only shows up for a *name* (that a lookup failure is not
// a refusal, and that the host is passed without its scheme or port) is
// covered against a stub guard instead, which is also why WithTargetGuard
// takes an interface rather than *checker.Guard.

// recordingGuard is a TargetGuard that answers from a script and remembers
// what it was asked, so a test can assert on the host string itself.
type recordingGuard struct {
	err   error
	hosts []string
}

func (g *recordingGuard) CheckHost(_ context.Context, host string) error {
	g.hosts = append(g.hosts, host)
	return g.err
}

func guardedServer(t *testing.T, allowPrivate bool) *Server {
	t.Helper()
	srv, _ := testServerWithDB(t)
	return srv.WithTargetGuard(checker.NewGuard(allowPrivate))
}

func channelBody(name, typ, cfg string) string {
	return `{"name":"` + name + `","type":"` + typ + `","config":` + cfg + `}`
}

// TestCreateChannelRefusesBlockedTarget is the ticket in one test: the save
// must fail with a 400, not succeed and fail later at delivery.
func TestCreateChannelRefusesBlockedTarget(t *testing.T) {
	tests := []struct {
		name      string
		typ       string
		config    string
		wantField string
	}{
		// The cloud metadata endpoint, which is the whole reason the
		// guard exists: an editor could otherwise have SubGlance read
		// the host's credentials and post them onwards.
		{
			name:      "webhook to the metadata service",
			typ:       "webhook",
			config:    `{"url":"http://169.254.169.254/latest/meta-data/"}`,
			wantField: "config.url",
		},
		{
			name:      "slack to loopback",
			typ:       "slack",
			config:    `{"url":"https://127.0.0.1/services/T000/B000/x"}`,
			wantField: "config.url",
		},
		{
			name:      "discord to a private address",
			typ:       "discord",
			config:    `{"url":"https://10.0.0.5/api/webhooks/1/x"}`,
			wantField: "config.url",
		},
		// A port must not smuggle the host past the check.
		{
			name:      "webhook to loopback on a non-default port",
			typ:       "webhook",
			config:    `{"url":"http://127.0.0.1:2375/containers/json"}`,
			wantField: "config.url",
		},
		// An IPv4 address written in IPv6-mapped form is the same
		// address; the guard unmaps before judging.
		{
			name:      "webhook to a v4-mapped loopback",
			typ:       "webhook",
			config:    `{"url":"http://[::ffff:127.0.0.1]/x"}`,
			wantField: "config.url",
		},
		// E-mail's destination is the SMTP relay, in a different field.
		{
			name:      "email via a loopback relay",
			typ:       "email",
			config:    `{"host":"127.0.0.1","port":"25","from":"a@example.com","to":"b@example.com"}`,
			wantField: "config.host",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := guardedServer(t, false)

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
				channelBody("blocked", tc.typ, tc.config))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 — a blocked destination was accepted at save time: %s",
					rec.Code, rec.Body.String())
			}
			// SUB-70: an API error says which field it is about.
			if !strings.Contains(rec.Body.String(), tc.wantField) {
				t.Errorf("error does not name the offending field %q: %s",
					tc.wantField, rec.Body.String())
			}
			// The operator needs to know the setting that would
			// permit this, or the message is a dead end.
			if !strings.Contains(rec.Body.String(), "allow-private-targets") {
				t.Errorf("error does not mention the opt-in flag: %s", rec.Body.String())
			}
		})
	}
}

// TestCreateChannelAllowsPublicTarget guards the other direction: the check
// must not refuse the channels people actually configure.
func TestCreateChannelAllowsPublicTarget(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		config string
	}{
		{"slack webhook by address", "slack", `{"url":"https://93.184.216.34/services/T000/B000/x"}`},
		{"email via a public relay", "email",
			`{"host":"93.184.216.34","port":"587","from":"a@example.com","to":"b@example.com"}`},
		// Telegram has no user-supplied destination at all: the API
		// host is a constant in the sender. The guard must therefore
		// have nothing to say, or telegram channels become unsavable.
		{"telegram", "telegram", `{"bot_token":"123456:ABC-DEF","chat_id":"42"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := guardedServer(t, false)

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
				channelBody("fine", tc.typ, tc.config))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestUpdateChannelRefusesBlockedTarget covers the edit path. A channel saved
// while the target was still allowed must not be able to be repointed at a
// blocked one, and a guard added after the fact must bite on the next save.
func TestUpdateChannelRefusesBlockedTarget(t *testing.T) {
	srv := guardedServer(t, false)

	created := createSlackChannel(t, srv, "ops", "https://93.184.216.34/services/T000/B000/x")

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		channelBody("ops", "slack", `{"url":"http://169.254.169.254/latest/meta-data/"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a channel was edited onto a blocked destination: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "config.url") {
		t.Errorf("error does not name the offending field: %s", rec.Body.String())
	}
}

// TestUpdateChannelAllowsPublicTarget proves the edit guard does not simply
// refuse every update, which would make the test above pass for the wrong
// reason.
func TestUpdateChannelAllowsPublicTarget(t *testing.T) {
	srv := guardedServer(t, false)

	created := createSlackChannel(t, srv, "ops", "https://93.184.216.34/services/T000/B000/x")

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		channelBody("ops", "slack", `{"url":"https://93.184.216.34/services/T000/B000/y"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestChannelTargetAllowPrivateTargets is the self-hosting case. An SMTP relay
// or a webhook receiver on the internal network is a legitimate setup, and the
// save side must permit exactly what the delivery side permits — otherwise the
// flag half-works and the operator cannot configure what they can deliver to.
func TestChannelTargetAllowPrivateTargets(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		config string
	}{
		{"webhook on the internal network", "webhook", `{"url":"http://10.0.0.5:9000/hook"}`},
		{"loopback smtp relay", "email",
			`{"host":"127.0.0.1","port":"25","from":"a@example.com","to":"b@example.com"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := guardedServer(t, true) // --allow-private-targets

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
				channelBody("internal", tc.typ, tc.config))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 — --allow-private-targets did not reach the save path: %s",
					rec.Code, rec.Body.String())
			}
		})
	}
}

// TestChannelTargetWithoutGuard keeps the API constructible without a delivery
// pipeline. A nil guard means no save-time policy, not a blanket refusal.
func TestChannelTargetWithoutGuard(t *testing.T) {
	srv, _ := testServerWithDB(t) // no WithTargetGuard

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("no guard", "webhook", `{"url":"http://169.254.169.254/latest/meta-data/"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 without a guard: %s", rec.Code, rec.Body.String())
	}
}

// TestChannelTargetPassesBareHost pins what the guard is handed. Passing the
// whole URL, or the host with its port still attached, would make every
// lookup fail — which the rule below turns into a silent pass, so the check
// would quietly stop working with no test failing anywhere else.
func TestChannelTargetPassesBareHost(t *testing.T) {
	g := &recordingGuard{}
	srv, _ := testServerWithDB(t)
	srv = srv.WithTargetGuard(g)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("hook", "webhook", `{"url":"https://hooks.example.com:8443/services/x"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(g.hosts) != 1 {
		t.Fatalf("guard was consulted %d times, want 1", len(g.hosts))
	}
	if g.hosts[0] != "hooks.example.com" {
		t.Errorf("guard was given %q, want the bare hostname %q", g.hosts[0], "hooks.example.com")
	}
}

// TestChannelTargetLookupFailureIsNotARefusal covers the rule that a resolver
// problem must not block a save. An internal relay whose name the API process
// cannot resolve is an ordinary configuration, and refusing it would turn a
// convenience layer into a new way for a flaky resolver to stop work. Anything
// genuinely unreachable still fails visibly at delivery.
func TestChannelTargetLookupFailureIsNotARefusal(t *testing.T) {
	g := &recordingGuard{err: errors.New("resolve relay.internal: server misbehaving")}
	srv, _ := testServerWithDB(t)
	srv = srv.WithTargetGuard(g)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("relay", "email",
			`{"host":"relay.internal","port":"587","from":"a@example.com","to":"b@example.com"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — a lookup failure was treated as a refusal: %s",
			rec.Code, rec.Body.String())
	}
}

// TestChannelTargetRefusalIsRecognisedByError proves the refusal is matched on
// checker.ErrPrivateTarget rather than on the text of the message, so the two
// outcomes above cannot be confused by a reworded error.
func TestChannelTargetRefusalIsRecognisedByError(t *testing.T) {
	g := &recordingGuard{err: checker.ErrPrivateTarget}
	srv, _ := testServerWithDB(t)
	srv = srv.WithTargetGuard(g)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("relay", "webhook", `{"url":"https://rebinding.example/hook"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestChannelTargetFieldForType records the per-type decision in an executable
// form, so changing it means changing this table and thinking about why.
func TestChannelTargetFieldForType(t *testing.T) {
	tests := []struct {
		typ       string
		config    map[string]string
		wantField string
		wantHost  string
	}{
		{"webhook", map[string]string{"url": "https://a.example/x"}, "config.url", "a.example"},
		{"discord", map[string]string{"url": "https://b.example/x"}, "config.url", "b.example"},
		{"slack", map[string]string{"url": "https://c.example/x"}, "config.url", "c.example"},
		{"email", map[string]string{"host": "relay.example"}, "config.host", "relay.example"},
		// Telegram talks to a constant; there is no user-supplied
		// destination to check.
		{"telegram", map[string]string{"bot_token": "1:x", "chat_id": "2"}, "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			field, host := channelTarget(channelRequest{Type: tc.typ, Config: tc.config})
			if field != tc.wantField || host != tc.wantHost {
				t.Errorf("channelTarget = (%q, %q), want (%q, %q)",
					field, host, tc.wantField, tc.wantHost)
			}
		})
	}
}
