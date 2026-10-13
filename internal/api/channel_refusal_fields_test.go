package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// A channel refusal names the request field it is about, so the channel form
// can put the sentence under the input to change and move focus there. Each
// sentence already starts with the key; these tests pin that the field says
// the same thing on the wire, rule by rule, because a refusal that loses its
// field still reads correctly and only lands in the wrong place on the form.

func smsTwilio(extra string) string {
	return `{"provider":"twilio","account_sid":"` + testTwilioSID + `","auth_token":"t",` +
		`"from":"+14155550100","numbers":"+31612345678"` + extra + `}`
}

func TestCreateChannelRefusalNamesTheField(t *testing.T) {
	tests := []struct {
		name, typ, config string
		field, want       string
	}{
		{"unknown type", "fax", `{"url":"https://example.com"}`, "type", "type must be one of"},
		{"missing required key", "email", `{"host":"smtp.example.com"}`, "config.to", "config.to is required"},
		{"url without a scheme", "slack", `{"url":"hooks.slack.com/services/x"}`, "config.url", "must use http or https"},
		{"url without a host", "discord", `{"url":"https://"}`, "config.url", "must include a host"},
		{"url that does not parse", "slack", `{"url":"https://exa mple.com/%zz"}`, "config.url", "is not a valid URL"},
		{"over-long value", "slack", `{"url":"https://example.com/` + strings.Repeat("a", 2100) + `"}`,
			"config.url", "values 2048 or fewer"},

		{"ntfy topic", "ntfy", `{"topic":"has space"}`, "config.topic", "may contain only letters"},
		{"ntfy server with a path", "ntfy", `{"topic":"ops","url":"https://ntfy.example.com/ops"}`,
			"config.url", "must name the ntfy server"},
		{"ntfy blank token", "ntfy", `{"topic":"ops","token":"  "}`, "config.token", "only whitespace"},
		{"ntfy token and login", "ntfy", `{"topic":"ops","token":"tk_x","username":"u","password":"p"}`,
			"config.token", "cannot be combined"},
		{"ntfy username alone", "ntfy", `{"topic":"ops","username":"u"}`, "config.password", "must be set together"},
		{"ntfy password alone", "ntfy", `{"topic":"ops","password":"p"}`, "config.username", "must be set together"},

		{"gotify token", "gotify", `{"url":"https://gotify.example.com"}`, "config.token", "config.token is required"},
		{"gotify priority", "gotify", `{"url":"https://gotify.example.com","token":"t","priority_up":"11"}`,
			"config.priority_up", "whole number from 0 to 10"},

		{"sms number", "sms", `{"provider":"twilio","account_sid":"` + testTwilioSID + `","auth_token":"t",` +
			`"from":"+14155550100","numbers":"0612345678"}`, "config.numbers", "config.numbers"},
		{"sms sender", "sms", smsTwilio(`,"from":"12345"`), "config.from", "config.from must be"},
		{"sms provider", "sms", `{"provider":"bird","numbers":"+31612345678"}`, "config.provider", "is not supported"},
		{"sms gateway login", "sms", `{"provider":"android-gateway","url":"http://192.0.2.50:8080",` +
			`"username":"sms","numbers":"+31612345678"}`, "config.password", "username and password are required"},
		{"sms limit", "sms", smsTwilio(`,"hourly_limit":"0"`), "config.hourly_limit", "config.hourly_limit"},
		{"sms zone", "sms", smsTwilio(`,"timezone":"Mars/Olympus"`), "config.timezone", "not an IANA time zone"},

		// The webhook's own settings carried a field before the rest did;
		// they keep it.
		{"webhook method", "webhook", `{"url":"https://hook.example.com","method":"PATCH"}`,
			"config.method", "method must be POST or PUT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", channelBody("ops", tc.typ, tc.config))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
		})
	}
}

// TestChannelRefusalOfARequestNamesNoField keeps the other direction: a rule
// that is about the request as a whole must not point the form at an input
// that is not wrong.
func TestChannelRefusalOfARequestNamesNoField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	cfg := `{"url":"https://example.com/hook","` + strings.Repeat("k", 65) + `":"v"}`
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", channelBody("ops", "slack", cfg))
	wantStatusProblem(t, rec.Code, rec.Body.String(), "", "config keys must be 64 characters or fewer")
}

func TestUpdateChannelRefusalNamesTheField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createSlackChannel(t, srv, "ops", "https://hooks.slack.com/services/T000/B000/x")
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		channelBody("ops", "slack", `{"url":"hooks.slack.com/services/T000/B000/x"}`))
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.url", "must use http or https")
}

// TestBlockedChannelTargetNamesTheField covers the SSRF guard's save-time
// refusal on all three ways in: it names the field in its sentence and now in
// the field as well.
func TestBlockedChannelTargetNamesTheField(t *testing.T) {
	const blocked = "http://10.0.0.5/hook"
	srv := guardedServer(t, false)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		channelBody("ops", "email", `{"host":"127.0.0.1","port":"25","from":"a@example.com","to":"b@example.com"}`))
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.host", "is not an allowed destination")

	created := createSlackChannel(t, srv, "ops", "https://93.184.216.34/services/T000/B000/x")
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		channelBody("ops", "slack", `{"url":"`+blocked+`"}`))
	wantStatusProblem(t, rec.Code, rec.Body.String(), "config.url", "allow-private-targets")

	code, _, body := importYAML(t, srv, "version: 1\nchannels:\n  - key: hook\n    name: Hook\n    type: webhook\n"+
		"    config:\n      url: "+blocked+"\n", false)
	if code != http.StatusBadRequest {
		t.Fatalf("import = %d, want 400: %s", code, body)
	}
	var e errorResponse
	must(t, json.Unmarshal([]byte(body), &e))
	if e.Field != "channels[0].config.url" || !strings.Contains(e.Error, "is not an allowed destination") {
		t.Errorf("import refusal = %q at %q, want the guard's sentence at channels[0].config.url", e.Error, e.Field)
	}
}
