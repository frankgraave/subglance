package notifier

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A webhook's header lines are typed into a textarea, and delivery does not
// validate them. A line net/http will not send fails every alert routed to
// the channel in the transport, before a connection is made, and the failure
// is retried as if the endpoint were down. So a save is held to the rules
// the transport applies.

// TestWebhookHeaderRulesMatchTheTransport holds the save rule to what a send
// does: a set of header lines is accepted exactly when an alert sent with
// it reaches the server.
func TestWebhookHeaderRulesMatchTheTransport(t *testing.T) {
	var reached atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	for _, headers := range []string{
		"X-Api-Key: k",
		"X-Api-Key: k\nAuthorization: Bearer t",
		"X-Api-Key: k\r\nX-Other: v\r\n",
		"X-Tab: a\tb",
		"X-Empty:",
		"!#$%&'*+-.^_`|~: v",
		"X-Utf8: café",
		"X-Next-Line: a\u0085b",
		"  X-Padded  :  v  ",
		"",
		"X Api: k",
		": k",
		"Bad\x7fName: v",
		"Ünïcode: v",
		"X-Api-Key: a\x00b",
		"X-Api-Key: a\x1bb",
		"X-Api-Key: a\x7fb",
		"X-Api-Key: a\rb",
		"X-Ok: v\nX Bad: v",
	} {
		reached.Store(false)
		cfg := map[string]string{"url": srv.URL, "headers": headers}
		accepted := ValidateConfig(store.ChannelWebhook, cfg) == nil
		sendErr := NewWebhookSender(nil).Send(context.Background(), cfg, Alert{MonitorName: "api"})
		if accepted != reached.Load() {
			t.Errorf("%q: accepted on save = %v, but a send reached the server = %v (%v)",
				headers, accepted, reached.Load(), sendErr)
		}
	}
}

// TestWebhookHeaderRefusalsNameTheLineNotTheValue: every refusal is about
// config.headers and says which line, and none of them repeats a value. The
// setting is masked as a credential, and a refusal is also the error a
// channel test logs.
func TestWebhookHeaderRefusalsNameTheLineNotTheValue(t *testing.T) {
	const secret = "s3cr3t-value"
	cases := map[string]struct {
		headers string
		want    string
	}{
		"no colon":          {"X-Api-Key: k\nBearer " + secret, "headers line 2 is not in Name: value form"},
		"space in the name": {"X Api: " + secret, "headers line 1 has no valid header name before its colon"},
		"empty name":        {": " + secret, "headers line 1 has no valid header name before its colon"},
		"secret as name":    {"Bearer " + secret + ": x", "headers line 1 has no valid header name before its colon"},
		"NUL in the value":  {"\nX-Api-Key: " + secret + "\x00", "headers line 2 has a control character in the value of X-Api-Key"},
		"ESC in the value":  {"X-Api-Key: " + secret + "\x1b", "headers line 1 has a control character in the value of X-Api-Key"},
		"one header twice":  {"X-Api-Key: " + secret + "\nx-api-key: other", "headers lines 1 and 2 both set x-api-key"},
		"content type twice": {"Content-Type: text/plain\nX-A: b\ncontent-type: application/json",
			"headers lines 1 and 3 both set content-type"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateConfig(store.ChannelWebhook, map[string]string{"url": "https://hooks.example.com/in", "headers": tc.headers})
			if err == nil {
				t.Fatalf("accepted %q", tc.headers)
			}
			if got := ConfigKey(err); got != "headers" {
				t.Errorf("refusal names key %q, want headers", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("refusal %q repeats the header value", err)
			}
		})
	}
}

// TestWebhookHeadersStoredUnderTheOldRuleSurviveAnEdit: header lines a
// channel was saved with before these rules are not refused by an edit that
// sends them back unchanged, so a rename or switching off a failing channel
// still works. Changed lines, a new channel and another stored type are
// held to every rule; the Name: value form applies to any edit that changes
// a setting.
func TestWebhookHeadersStoredUnderTheOldRuleSurviveAnEdit(t *testing.T) {
	const legacy = "X Api: k\nX-Dup: 1\nx-dup: 2"
	stored := &store.Channel{Type: store.ChannelWebhook,
		Config: map[string]string{"url": "https://hooks.example.com/in", "headers": legacy}}
	cfg := func(headers string) map[string]string {
		return map[string]string{"url": "https://hooks.example.com/in", "headers": headers}
	}

	if err := ValidateEditedConfig(store.ChannelWebhook, cfg(legacy), stored); err != nil {
		t.Errorf("unchanged stored headers refused: %v", err)
	}
	for name, tc := range map[string]struct {
		headers string
		stored  *store.Channel
	}{
		"changed lines":      {legacy + "\nX-New: v", stored},
		"a new channel":      {legacy, nil},
		"another type first": {legacy, &store.Channel{Type: store.ChannelSlack, Config: map[string]string{"headers": legacy}}},
	} {
		if err := ValidateEditedConfig(store.ChannelWebhook, cfg(tc.headers), tc.stored); ConfigKey(err) != "headers" {
			t.Errorf("%s: err = %v, want a refusal of headers", name, err)
		}
	}

	// A stored line without a colon fails every delivery, so the channel
	// is one an operator switches off: sent back with every other setting
	// as stored, it is not refused. Beside any changed setting the Name:
	// value form applies to it again.
	noColon := &store.Channel{Type: store.ChannelWebhook, Config: cfg("Authorization")}
	if err := ValidateEditedConfig(store.ChannelWebhook, cfg("Authorization"), noColon); err != nil {
		t.Errorf("a stored line without a colon, sent back unchanged: %v", err)
	}
	withMethod := cfg("Authorization")
	withMethod["method"] = "PUT"
	if err := ValidateEditedConfig(store.ChannelWebhook, withMethod, noColon); ConfigKey(err) != "headers" {
		t.Errorf("a stored line without a colon beside a new method: err = %v, want the Name: value refusal", err)
	}
	// The exemption is for the header lines only.
	other := cfg(legacy)
	other["method"] = "DELETE"
	if err := ValidateEditedConfig(store.ChannelWebhook, other, stored); ConfigKey(err) != "method" {
		t.Errorf("a bad method beside unchanged headers: err = %v, want a refusal of method", err)
	}
}

// TestAnUnchangedStoredConfigIsNotJudgedAgain: a channel whose settings
// break a sender rule added after it was saved (an email port of 99999, a
// webhook to port 0) is accepted by an edit that sends every setting back as
// stored, so it can be renamed and switched off. One changed setting, a new
// channel or another stored type is held to every rule.
func TestAnUnchangedStoredConfigIsNotJudgedAgain(t *testing.T) {
	email := map[string]string{"host": "smtp.example.com", "port": "99999", "from": "sg@example.com", "to": "ops@example.com"}
	stored := &store.Channel{Type: store.ChannelEmail, Config: email}
	if err := ValidateEditedConfig(store.ChannelEmail, maps.Clone(email), stored); err != nil {
		t.Errorf("the stored settings sent back: %v", err)
	}
	changed := maps.Clone(email)
	changed["to"] = "oncall@example.com"
	for name, tc := range map[string]struct {
		cfg    map[string]string
		stored *store.Channel
	}{
		"one setting changed": {changed, stored},
		"a setting added":     {map[string]string{"host": "smtp.example.com", "port": "99999", "from": "sg@example.com", "to": "ops@example.com", "username": "u"}, stored},
		"a new channel":       {maps.Clone(email), nil},
		"another stored type": {maps.Clone(email), &store.Channel{Type: store.ChannelWebhook, Config: email}},
	} {
		if err := ValidateEditedConfig(store.ChannelEmail, tc.cfg, tc.stored); ConfigKey(err) != "port" {
			t.Errorf("%s: err = %v, want a refusal of port", name, err)
		}
	}
}
