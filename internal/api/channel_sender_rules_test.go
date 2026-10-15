package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

// A channel the API saves has to be one its sender can deliver through.
// Delivery does not validate: an email channel saved without a host dials
// ":587", one with port 99999 fails its dial, and both are retried as if the
// server were down until the alert is given up, so every outage routed to
// the channel is lost and the first sign of it is the channel-failing notice.
// These are the settings the API used to save and the sender refused.
var senderRefusedChannels = []struct {
	name, typ string
	cfg       map[string]string
	field     string
	want      string
}{
	{"email without a host", store.ChannelEmail,
		map[string]string{"to": "ops@example.com", "from": "sg@example.com"}, "config.host", "config.host is required"},
	{"email without a sender", store.ChannelEmail,
		map[string]string{"to": "ops@example.com", "host": "smtp.example.com"}, "config.from", "config.from is required"},
	{"email sender that is a name", store.ChannelEmail,
		map[string]string{"to": "ops@example.com", "from": "SubGlance", "host": "smtp.example.com"},
		"config.from", "config.from is not an e-mail address"},
	{"email recipient without an at sign", store.ChannelEmail,
		map[string]string{"to": "ops", "from": "sg@example.com", "host": "smtp.example.com"},
		"config.to", "config.to is not an e-mail address"},
	{"email recipients separated by semicolons", store.ChannelEmail,
		map[string]string{"to": "ops@example.com; oncall@example.com", "from": "sg@example.com", "host": "smtp.example.com"},
		"config.to", "config.to separates addresses with commas, not semicolons or line breaks"},
	{"email port that is a word", store.ChannelEmail,
		map[string]string{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com", "port": "abc"},
		"config.port", "config.port must be a number between 1 and 65535"},
	{"email port out of range", store.ChannelEmail,
		map[string]string{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com", "port": "99999"},
		"config.port", "config.port must be a number between 1 and 65535"},
	{"telegram without a chat", store.ChannelTelegram,
		map[string]string{"bot_token": "123456:abc"}, "config.chat_id", "config.chat_id is required"},
	{"telegram token that is a bot name", store.ChannelTelegram,
		map[string]string{"bot_token": "@ops_bot", "chat_id": "42"}, "config.bot_token", "does not look like a bot token"},
	{"discord url that is not discord", store.ChannelDiscord,
		map[string]string{"url": "https://hooks.example.com/x"}, "config.url", "does not look like a Discord webhook"},
	// A URL that parses and that every delivery refuses before it leaves
	// the process: the dial rejects the port, the resolver the hostname.
	{"webhook port out of range", store.ChannelWebhook,
		map[string]string{"url": "https://hooks.example.com:99999/in"}, "config.url", "port 99999 is out of range"},
	{"slack port zero", store.ChannelSlack,
		map[string]string{"url": "https://hooks.slack.com:0/services/x"}, "config.url", "port 0 is out of range"},
	{"gotify hostname with a bad character", store.ChannelGotify,
		map[string]string{"url": "https://got!fy.example.com", "token": "t"}, "config.url", `contains "!"`},
	{"ntfy hostname with a leading hyphen", store.ChannelNtfy,
		map[string]string{"url": "https://-ntfy.example.com", "topic": "alerts"}, "config.url", "starts with a hyphen"},
	{"discord port out of range", store.ChannelDiscord,
		map[string]string{"url": "https://discord.com:65536/api/webhooks/1/x"}, "config.url", "port 65536 is out of range"},
	{"sms gateway hostname of digits", store.ChannelSMS,
		map[string]string{"provider": "android-gateway", "url": "http://192.168.1:8080", "username": "u", "password": "p", "numbers": "+31612345678"},
		"config.url", "not a valid IP address"},
}

func TestChannelSaveRefusesSettingsItsSenderRefuses(t *testing.T) {
	for _, tc := range senderRefusedChannels {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			body, err := json.Marshal(map[string]any{"name": "Ops", "type": tc.typ, "config": tc.cfg})
			must(t, err)

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", string(body))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)

			// An edit is held to the same rule: a channel saved before it
			// existed must not keep a setting its sender refuses because
			// it is being renamed.
			ch := seedChannel(t, db, store.ChannelSlack, map[string]string{"url": "https://hooks.slack.com/services/x"})
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(ch.ID, 10), string(body))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)

			// And so is a configuration file, at the setting's own path.
			var doc strings.Builder
			doc.WriteString("version: 1\nchannels:\n  - key: ops\n    name: Ops\n    type: " + tc.typ + "\n    config:\n")
			for k, v := range tc.cfg {
				doc.WriteString("      " + k + ": " + strconv.Quote(v) + "\n")
			}
			code, _, resp := importYAML(t, srv, doc.String(), true)
			if code != http.StatusBadRequest {
				t.Fatalf("import = %d, want 400: %s", code, resp)
			}
			var e errorResponse
			must(t, json.Unmarshal([]byte(resp), &e))
			if e.Field != "channels[0]."+tc.field || !strings.Contains(e.Error, tc.want[len("config."):]) {
				t.Errorf("import refusal = %q at %q, want %q at channels[0].%s", e.Error, e.Field, tc.want, tc.field)
			}
		})
	}
}

// TestASavedChannelIsOneItsSenderAccepts holds the invariant itself over a
// corpus of settings, accepted and refused, for every type: whatever the API
// saves, the type's sender accepts. A rule added to a sender and not to the
// save path fails here before it fails an outage.
func TestASavedChannelIsOneItsSenderAccepts(t *testing.T) {
	corpus := map[string][]map[string]string{
		store.ChannelEmail: {
			{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com"},
			{"to": "ops@example.com, oncall@example.com", "from": "sg@example.com", "host": "smtp.example.com", "port": "465"},
			{"to": "ops@example.com"},
			{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com", "port": "-1"},
		},
		store.ChannelTelegram: {
			{"bot_token": "123456:abc", "chat_id": "-1001234567890"},
			{"bot_token": "123456:abc", "chat_id": " "},
			{"bot_token": "abc", "chat_id": "42"},
		},
		store.ChannelDiscord: {
			{"url": "https://discord.com/api/webhooks/1/x"},
			{"url": "https://discordapp.com/api/webhooks/1/x"},
			{"url": "https://chat.example.com/hooks/1"},
		},
		store.ChannelSlack: {
			{"url": "https://hooks.slack.com/services/T0/B0/x"},
			{"url": "mailto:ops@example.com"},
		},
		store.ChannelWebhook: {
			{"url": "https://hooks.example.com/in"},
			{"url": "https://hooks.example.com/in", "method": "PUT", "headers": "X-Key: v"},
			{"url": "https://hooks.example.com/in", "headers": "nonsense"},
		},
		store.ChannelNtfy: {
			{"topic": "alerts"},
			{"topic": "alerts", "url": "https://ntfy.example.com"},
			{"topic": "alerts", "url": "https://ntfy.example.com/alerts"},
		},
		store.ChannelGotify: {
			{"url": "https://gotify.example.com", "token": "t"},
			{"url": "https://gotify.example.com", "token": "t", "priority_down": "12"},
		},
		store.ChannelSMS: {
			{"provider": "twilio", "account_sid": testTwilioSID, "auth_token": "t", "from": "SubGlance", "numbers": "+31612345678"},
			{"provider": "twilio", "account_sid": "AC1", "auth_token": "t", "from": "SubGlance", "numbers": "+31612345678"},
		},
	}
	for _, tc := range senderRefusedChannels {
		corpus[tc.typ] = append(corpus[tc.typ], tc.cfg)
	}

	srv, _ := testServerWithDB(t)
	for typ, cfgs := range corpus {
		for _, cfg := range cfgs {
			body, err := json.Marshal(map[string]any{"name": "Ops", "type": typ, "config": cfg})
			must(t, err)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", string(body))
			senderErr := notifier.ValidateConfig(typ, cfg)
			if rec.Code == http.StatusCreated && senderErr != nil {
				t.Errorf("%s %v: saved, but its sender refuses it: %v", typ, cfg, senderErr)
			}
			if rec.Code != http.StatusCreated && rec.Code != http.StatusBadRequest {
				t.Errorf("%s %v: status %d: %s", typ, cfg, rec.Code, rec.Body.String())
			}
		}
	}
}
