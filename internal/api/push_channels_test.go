package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/notifier"
)

// The two self-hosted push channels. Their API surface is the same as every
// other channel's; what these tests pin is what is specific to them: which
// field is required, what is masked, and that the typical self-hosted server
// on a private address gets an error that names the way out.

func TestCreatePushChannels(t *testing.T) {
	srv, _ := testServerWithDB(t)

	tests := []struct {
		name, typ, config string
		masked, public    []string
	}{
		// The topic is masked: on a server without access control, the
		// topic name is the only thing standing between a stranger and
		// the operator's lock screen. ntfy's own docs call it a password.
		{"ntfy on the public server", "ntfy", `{"topic":"homelab-alerts","token":"tk_abcdefgh"}`,
			[]string{"token", "topic"}, nil},
		{"ntfy self-hosted with basic auth", "ntfy",
			`{"url":"https://ntfy.home.example","topic":"alerts","username":"me","password":"hunter22"}`,
			[]string{"password", "url", "topic"}, []string{"username"}},
		{"gotify", "gotify",
			`{"url":"https://gotify.home.example","token":"AppTok3nXYZ","priority_down":"9","priority_up":"2"}`,
			[]string{"token", "url"}, []string{"priority_down", "priority_up"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", channelBody("push", tc.typ, tc.config))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			var got channelResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var sent map[string]string
			if err := json.Unmarshal([]byte(tc.config), &sent); err != nil {
				t.Fatal(err)
			}
			for _, k := range tc.masked {
				if got.Config[k] == sent[k] || !strings.HasPrefix(got.Config[k], "****") {
					t.Errorf("config.%s = %q, want it masked", k, got.Config[k])
				}
			}
			for _, k := range tc.public {
				if got.Config[k] != sent[k] {
					t.Errorf("config.%s = %q, want %q in full", k, got.Config[k], sent[k])
				}
			}
		})
	}
}

func TestCreatePushChannelRejectsBadInput(t *testing.T) {
	srv, _ := testServerWithDB(t)

	cases := []struct{ name, body, want string }{
		{"ntfy without topic", channelBody("x", "ntfy", `{"url":"https://ntfy.example"}`), "config.topic"},
		{"ntfy topic with a slash", channelBody("x", "ntfy", `{"topic":"a/b"}`), "config.topic"},
		{"ntfy non-http server", channelBody("x", "ntfy", `{"topic":"t","url":"ftp://ntfy.example"}`), "config.url"},
		{"ntfy token and password", channelBody("x", "ntfy",
			`{"topic":"t","token":"a","username":"u","password":"p"}`), "config.token"},
		{"ntfy user without password", channelBody("x", "ntfy", `{"topic":"t","username":"u"}`), "config.username"},
		{"gotify without url", channelBody("x", "gotify", `{"token":"t"}`), "config.url"},
		{"gotify without token", channelBody("x", "gotify", `{"url":"https://gotify.example"}`), "config.token"},
		{"gotify priority out of range", channelBody("x", "gotify",
			`{"url":"https://gotify.example","token":"t","priority_down":"11"}`), "config.priority_down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("error does not name %s: %s", tc.want, rec.Body.String())
			}
		})
	}
}

// TestPushChannelOnPrivateAddressIsRefusedWithTheWayOut is the save-time half
// of the private-address decision: no separate switch, the existing
// --allow-private-targets covers these channels too, and the refusal says so
// by flag and by environment variable.
func TestPushChannelOnPrivateAddressIsRefusedWithTheWayOut(t *testing.T) {
	for _, tc := range []struct{ typ, config string }{
		{"gotify", `{"url":"http://192.168.1.20:8080","token":"t"}`},
		{"ntfy", `{"url":"http://10.0.0.5","topic":"t"}`},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			rec := doJSON(t, guardedServer(t, false), http.MethodPost, "/api/v1/channels",
				channelBody("lan", tc.typ, tc.config))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{"config.url", "private address", "--allow-private-targets",
				"SUBGLANCE_ALLOW_PRIVATE_TARGETS"} {
				if !strings.Contains(body, want) {
					t.Errorf("error does not mention %q: %s", want, body)
				}
			}

			rec = doJSON(t, guardedServer(t, true), http.MethodPost, "/api/v1/channels",
				channelBody("lan", tc.typ, tc.config))
			if rec.Code != http.StatusCreated {
				t.Fatalf("with --allow-private-targets: status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNtfyWithoutServerIsNotGuarded: an ntfy channel with no server posts to
// the public default, a constant, so the guard has nothing to look at.
func TestNtfyWithoutServerIsNotGuarded(t *testing.T) {
	field, host := channelTarget(channelRequest{Type: "ntfy", Config: map[string]string{"topic": "t"}})
	if field != "" || host != "" {
		t.Errorf("channelTarget = (%q, %q), want nothing to check", field, host)
	}
	field, host = channelTarget(channelRequest{Type: "gotify",
		Config: map[string]string{"url": "https://gotify.example:8443/sub", "token": "t"}})
	if field != "config.url" || host != "gotify.example" {
		t.Errorf("gotify channelTarget = (%q, %q), want (config.url, gotify.example)", field, host)
	}
}

// TestPushChannelTestButtonOnPrivateAddressNamesTheWayOut is the test-button
// half. A channel saved while --allow-private-targets was on and tested after
// it was turned off reaches the delivery guard, and the 502 the button shows
// has to carry the same pointer as the save-time refusal.
func TestPushChannelTestButtonOnPrivateAddressNamesTheWayOut(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	srv, _ := testServerWithDB(t)
	srv = srv.WithChannelTester(notifier.New(notifier.Options{Guard: checker.NewGuard(false)}))

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("lan", "gotify", `{"url":"`+target.URL+`","token":"t"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, srv, http.MethodPost,
		"/api/v1/channels/"+strconv.FormatInt(created.ID, 10)+"/test", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("test: status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"loopback address 127.0.0.1", "--allow-private-targets",
		"SUBGLANCE_ALLOW_PRIVATE_TARGETS"} {
		if !strings.Contains(body, want) {
			t.Errorf("test error does not mention %q: %s", want, body)
		}
	}
}
