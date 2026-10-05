package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// A webhook's body template and method are configuration a person writes and
// corrects by hand, so they are read back in full; its URL and headers are
// where credentials go, and stay masked.

func webhookBody(t *testing.T, cfg map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"name": "teams", "type": "webhook", "config": cfg})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWebhookBodyTemplateIsValidatedOnSave(t *testing.T) {
	srv, _ := testServerWithDB(t)
	cases := map[string]struct {
		cfg  map[string]string
		want string
	}{
		"unknown placeholder": {map[string]string{"body": `{"text": "{{monitor}}"}`},
			"config.body has an unknown placeholder {{monitor}} at line 1, column 11"},
		"not JSON": {map[string]string{"body": `{"text": {{summary}}}`},
			"outside a JSON string"},
		"unknown method": {map[string]string{"method": "DELETE"}, "config.method must be POST or PUT"},
		"bad header":     {map[string]string{"headers": "Authorization"}, "is not in Name: value form"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.cfg["url"] = "https://example.com/hook"
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", webhookBody(t, tc.cfg))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body = %s, want it to contain %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestWebhookBodyTemplateIsReadBackInFull(t *testing.T) {
	srv, _ := testServerWithDB(t)
	// Longer than the 2048 characters other settings may hold: an adaptive
	// card with a few facts is that size, and the body has its own limit.
	body := `{"type": "message", "text": "{{summary}}", "pad": "` + strings.Repeat("x", 3000) + `"}`
	cfg := map[string]string{"url": "https://example.com/hook/secretpath", "method": "PUT",
		"headers": "Authorization: Bearer topsecret", "body": body}
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", webhookBody(t, cfg))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var got channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Config["body"] != body || got.Config["method"] != "PUT" {
		t.Errorf("body/method not read back in full: %q / %q", got.Config["body"], got.Config["method"])
	}
	if strings.Contains(rec.Body.String(), "secretpath") || strings.Contains(rec.Body.String(), "topsecret") {
		t.Errorf("the URL or a header leaked: %s", rec.Body.String())
	}

	// Writing back what was read keeps the URL and headers and stores an
	// edited template.
	got.Config["body"] = `{"text": "{{summary}} ({{status}})"}`
	put, _ := json.Marshal(map[string]any{"name": "teams", "type": "webhook", "config": got.Config})
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(got.ID, 10), string(put))
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", rec.Code, rec.Body.String())
	}
	stored, err := srv.db.GetChannel(t.Context(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config["url"] != cfg["url"] || stored.Config["headers"] != cfg["headers"] ||
		stored.Config["body"] != got.Config["body"] {
		t.Errorf("stored config after a round trip = %#v", stored.Config)
	}

	// The template's own ceiling still holds.
	cfg["body"] = `"` + strings.Repeat("x", 9000) + `"`
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/channels", webhookBody(t, cfg))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "8192 characters or fewer") {
		t.Errorf("a 9000-character body: %d %s", rec.Code, rec.Body.String())
	}
}

// TestWebhookBodyTemplateTravelsInAConfigFile: the template is exported as
// written, and the URL and headers as placeholders, so a file moves a Teams
// channel to another instance with only its credentials to fill in.
func TestWebhookBodyTemplateTravelsInAConfigFile(t *testing.T) {
	src, _ := testServerWithDB(t)
	body := "{\n  \"type\": \"message\",\n  \"text\": \"{{summary}}: {{last_error}}\"\n}"
	cfg := map[string]string{"url": "https://example.com/hook/secretpath", "method": "PUT",
		"headers": "Authorization: Bearer topsecret", "body": body}
	if rec := doJSON(t, src, http.MethodPost, "/api/v1/channels", webhookBody(t, cfg)); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	exported := exportYAML(t, src)
	if strings.Contains(exported, "secretpath") || strings.Contains(exported, "topsecret") {
		t.Fatalf("the export carries a credential:\n%s", exported)
	}

	dst, dstDB := testServerWithDB(t)
	code, rep, out := importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, out)
	}
	if len(rep.Channels) != 1 || strings.Join(rep.Channels[0].NeedsSecrets, ",") != "headers,url" {
		t.Errorf("import report = %+v, want the url and headers to fill in", rep.Channels)
	}
	chans, err := dstDB.ListChannels(t.Context())
	must(t, err)
	if len(chans) != 1 || chans[0].Config["body"] != body || chans[0].Config["method"] != "PUT" {
		t.Errorf("imported channel config = %#v", chans)
	}
}
