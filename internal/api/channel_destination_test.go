package api

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// A stored credential is never readable through the API, so a client sends it
// back as its mask and the server restores it. These tests hold that restore
// to the destination the credential was saved for: an edit that points a
// channel somewhere else, while keeping a credential it was never shown, would
// hand that credential to a server of the editor's choosing.

// seedChannel stores a channel directly, so its config holds real values.
func seedChannel(t *testing.T, db *store.DB, typ string, cfg map[string]string) store.Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), store.Channel{Name: "Ops", Type: typ, Enabled: true, Config: cfg})
	must(t, err)
	return ch
}

// putChannel replaces a channel as the seeded admin.
func putChannel(t *testing.T, srv *Server, id int64, typ string, cfg map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": "Ops", "type": typ, "config": cfg})
	must(t, err)
	return doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(id, 10), string(body))
}

func TestChannelEditRefusesAStoredCredentialForANewDestination(t *testing.T) {
	email := map[string]string{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com",
		"port": "587", "username": "sg", "password": "smtp-password-secret"}
	webhook := map[string]string{"url": "https://hooks.example.com/in",
		"headers": `{"X-Api-Key":"header-secret"}`}
	ntfy := map[string]string{"topic": "alerts-topic-secret"}
	gotify := map[string]string{"url": "https://gotify.example.com", "token": "gotify-token-secret"}
	sms := map[string]string{"provider": "android-gateway", "url": "http://phone.example:8080",
		"username": "sms", "password": "phone-pass", "numbers": "+31612345678"}
	telegram := map[string]string{"bot_token": "123456:telegram-bot-secret", "chat_id": "42"}

	cases := []struct {
		name    string
		typ     string
		stored  map[string]string
		newType string
		change  map[string]string
		field   string
		why     string
	}{
		{"email host", store.ChannelEmail, email, store.ChannelEmail,
			map[string]string{"host": "smtp.elsewhere.example"}, "config.password", "config.host changed"},
		{"email port", store.ChannelEmail, email, store.ChannelEmail,
			map[string]string{"port": "2525"}, "config.password", "config.port changed"},
		{"webhook url", store.ChannelWebhook, webhook, store.ChannelWebhook,
			map[string]string{"url": "https://elsewhere.example/in"}, "config.headers", "config.url changed"},
		{"ntfy server", store.ChannelNtfy, ntfy, store.ChannelNtfy,
			map[string]string{"url": "https://ntfy.elsewhere.example"}, "config.topic", "config.url changed"},
		{"gotify server", store.ChannelGotify, gotify, store.ChannelGotify,
			map[string]string{"url": "https://gotify.elsewhere.example"}, "config.token", "config.url changed"},
		{"sms gateway", store.ChannelSMS, sms, store.ChannelSMS,
			map[string]string{"url": "http://elsewhere.example:8080"}, "config.password", "config.url changed"},
		{"telegram to webhook", store.ChannelTelegram, telegram, store.ChannelWebhook,
			map[string]string{"url": "https://elsewhere.example/in", "chat_id": ""}, "config.bot_token", "the type changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			ch := seedChannel(t, db, tc.typ, tc.stored)

			// What a client that read the channel sends back: the masked
			// view, with only the destination changed.
			seen := maskConfig(tc.typ, tc.stored, true)
			for k, v := range tc.change {
				if v == "" {
					delete(seen, k)
					continue
				}
				seen[k] = v
			}
			rec := putChannel(t, srv, ch.ID, tc.newType, seen)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.why)

			got, err := db.GetChannel(context.Background(), ch.ID)
			must(t, err)
			if got.Type != tc.typ || !maps.Equal(got.Config, tc.stored) {
				t.Errorf("a refused edit changed the channel: %s %v", got.Type, got.Config)
			}
		})
	}
}

func TestChannelEditKeepsAStoredCredentialForTheSameDestination(t *testing.T) {
	srv, db := testServerWithDB(t)
	stored := map[string]string{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com",
		"port": "", "username": "sg", "password": "smtp-password-secret"}
	ch := seedChannel(t, db, store.ChannelEmail, stored)

	// A new recipient is not a new destination: the password still goes to
	// the same SMTP server. An empty port and the port it defaults to are
	// one server, and a stray space does not make another.
	seen := maskConfig(store.ChannelEmail, stored, true)
	seen["to"] = "oncall@example.com"
	seen["port"] = "587"
	seen["host"] = " smtp.example.com "
	rec := putChannel(t, srv, ch.ID, store.ChannelEmail, seen)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit keeping the destination: %d %s", rec.Code, rec.Body.String())
	}
	got, err := db.GetChannel(context.Background(), ch.ID)
	must(t, err)
	if got.Config["password"] != "smtp-password-secret" || got.Config["to"] != "oncall@example.com" {
		t.Errorf("stored config = %v", got.Config)
	}

	// The same for ntfy's default server written out.
	ntfy := seedChannel(t, db, store.ChannelNtfy, map[string]string{"topic": "alerts-topic-secret"})
	seen = maskConfig(store.ChannelNtfy, ntfy.Config, true)
	seen["url"] = "https://ntfy.sh"
	if rec := putChannel(t, srv, ntfy.ID, store.ChannelNtfy, seen); rec.Code != http.StatusOK {
		t.Fatalf("ntfy default server written out: %d %s", rec.Code, rec.Body.String())
	}
}

func TestChannelEditMovesWithCredentialsTypedAgain(t *testing.T) {
	srv, db := testServerWithDB(t)
	ch := seedChannel(t, db, store.ChannelEmail, map[string]string{"to": "ops@example.com",
		"from": "sg@example.com", "host": "smtp.example.com", "port": "587", "username": "sg",
		"password": "smtp-password-secret"})
	rec := putChannel(t, srv, ch.ID, store.ChannelEmail, map[string]string{"to": "ops@example.com",
		"from": "sg@example.com", "host": "smtp.elsewhere.example", "port": "587", "username": "sg",
		"password": "a-new-password"})
	if rec.Code != http.StatusOK {
		t.Fatalf("move with the password typed again: %d %s", rec.Code, rec.Body.String())
	}
	got, err := db.GetChannel(context.Background(), ch.ID)
	must(t, err)
	if got.Config["host"] != "smtp.elsewhere.example" || got.Config["password"] != "a-new-password" {
		t.Errorf("stored config = %v", got.Config)
	}

	// A Slack URL is its own credential: a new one is a new value, not a
	// kept one, and is accepted.
	slack := seedChannel(t, db, store.ChannelSlack, map[string]string{"url": "https://hooks.slack.com/services/T/B/old"})
	if rec := putChannel(t, srv, slack.ID, store.ChannelSlack,
		map[string]string{"url": "https://hooks.slack.com/services/T/B/new"}); rec.Code != http.StatusOK {
		t.Fatalf("new Slack URL: %d %s", rec.Code, rec.Body.String())
	}
}

// TestChannelEditRefusesMaskedNumbersForANewGateway: an editor reads an SMS
// channel's numbers masked, and those follow the gateway's address like a
// credential does.
func TestChannelEditRefusesMaskedNumbersForANewGateway(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", smsChannelBody(`06 1234 5678`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created channelResponse
	must(t, json.Unmarshal(rec.Body.Bytes(), &created))

	editor := seedUser(t, srv, db, "editor@example.com", store.RoleEditor)
	seen := getChannelAs(t, srv, editor, created.ID)
	seen.Config["url"] = "http://elsewhere.example:8080"
	seen.Config["password"] = "typed-again"
	body, _ := json.Marshal(map[string]any{"name": seen.Name, "type": seen.Type, "config": seen.Config})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+editor)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.numbers", "config.url changed")
}

func TestImportDoesNotCarryAStoredCredentialToANewDestination(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	exported := exportYAML(t, srv)
	if !strings.Contains(exported, "host: smtp.example.com") {
		t.Fatalf("export has no SMTP host to move:\n%s", exported)
	}
	moved := strings.Replace(exported, "host: smtp.example.com", "host: smtp.elsewhere.example", 1)

	for _, dryRun := range []bool{true, false} {
		code, rep, body := importYAML(t, srv, moved, dryRun)
		if code != http.StatusOK {
			t.Fatalf("import (dry run %v) = %d: %s", dryRun, code, body)
		}
		var needs []string
		for _, it := range rep.Channels {
			if it.Key == "mail" {
				needs = it.NeedsSecrets
			}
		}
		if strings.Join(needs, ",") != "password" {
			t.Errorf("dry run %v: mail needs_secrets = %v, want [password]\n%s", dryRun, needs, body)
		}
	}

	chans, err := db.ListChannels(context.Background())
	must(t, err)
	for _, c := range chans {
		if c.Name != "Mail" {
			continue
		}
		if c.Config["host"] != "smtp.elsewhere.example" {
			t.Errorf("host = %q, want the file's", c.Config["host"])
		}
		if c.Config["password"] == secretSMTPPass {
			t.Error("the stored SMTP password was kept for a new host")
		}
		if c.Enabled {
			t.Error("a channel waiting for its password is enabled")
		}
	}
}

// TestImportKeepsStoredValuesOnlyWhereTheyWereSaved holds an import to the
// two rules a stored value lives by besides the destination: SMS numbers are
// kept only under the country code they were saved with, and a type change
// does not inherit the old type's settings by leaving config out.
func TestImportKeepsStoredValuesOnlyWhereTheyWereSaved(t *testing.T) {
	sms := map[string]string{"provider": "android-gateway", "url": "http://phone.example:8080",
		"username": "sms", "password": "phone-pass", "country_code": "+31", "numbers": "06 1234 5678"}
	smsFile := func(key, countryCode string) string {
		return "version: 1\nchannels:\n  - key: " + key + "\n    name: Ops\n    type: sms\n    config:\n" +
			"      provider: android-gateway\n      url: http://phone.example:8080\n      username: sms\n" +
			"      password: \"" + configfile.Placeholder + "\"\n      country_code: \"" + countryCode + "\"\n" +
			"      numbers: \"" + configfile.Placeholder + "\"\n"
	}
	cases := map[string]struct {
		typ       string
		cfg       map[string]string
		file      func(key string) string
		wantField string            // a refused file names this path
		wantNeeds string            // otherwise the channel waits for these
		wantKept  map[string]string // stored values the import must leave as they were
	}{
		"a new country code drops the stored numbers": {
			typ: store.ChannelSMS, cfg: sms,
			file:      func(key string) string { return smsFile(key, "+32") },
			wantNeeds: "numbers",
			wantKept:  map[string]string{"password": "phone-pass", "country_code": "+32", "numbers": ""},
		},
		"the same country code, spaced, keeps them": {
			typ: store.ChannelSMS, cfg: sms,
			file:     func(key string) string { return smsFile(key, " +31 ") },
			wantKept: map[string]string{"password": "phone-pass", "numbers": "06 1234 5678"},
		},
		"a type change without config is refused": {
			typ: store.ChannelSlack, cfg: map[string]string{"url": secretSlackURL},
			file: func(key string) string {
				return "version: 1\nchannels:\n  - key: " + key + "\n    name: Ops\n    type: webhook\n"
			},
			wantField: "channels[0].config",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			ch := seedChannel(t, db, tc.typ, maps.Clone(tc.cfg))
			doc, err := configfile.Parse([]byte(exportYAML(t, srv)))
			must(t, err)
			if len(doc.Channels) != 1 {
				t.Fatalf("export has %d channels, want 1", len(doc.Channels))
			}
			file := tc.file(doc.Channels[0].Key)

			for _, dryRun := range []bool{true, false} {
				code, rep, body := importYAML(t, srv, file, dryRun)
				if tc.wantField != "" {
					wantStatusProblem(t, code, body, tc.wantField, "type changes")
					continue
				}
				if code != http.StatusOK {
					t.Fatalf("import (dry run %v) = %d: %s", dryRun, code, body)
				}
				if len(rep.Channels) != 1 || strings.Join(rep.Channels[0].NeedsSecrets, ",") != tc.wantNeeds {
					t.Errorf("dry run %v: channels = %+v, want needs_secrets %q\n%s", dryRun, rep.Channels, tc.wantNeeds, body)
				}
			}

			stored, err := db.GetChannel(context.Background(), ch.ID)
			must(t, err)
			if tc.wantField != "" {
				if stored.Type != tc.typ || !maps.Equal(stored.Config, tc.cfg) {
					t.Errorf("a refused file changed the channel: %s %v", stored.Type, stored.Config)
				}
				return
			}
			for k, want := range tc.wantKept {
				if got := stored.Config[k]; got != want {
					t.Errorf("config.%s = %q, want %q", k, got, want)
				}
			}
			if stored.Enabled != (tc.wantNeeds == "") {
				t.Errorf("enabled = %v with needs_secrets %q", stored.Enabled, tc.wantNeeds)
			}
		})
	}
}
