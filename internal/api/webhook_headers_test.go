package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// legacyWebhookHeaders are header lines a webhook channel could be saved with
// before they were held to the rules the transport applies: a name that is
// not an HTTP token, and one header set on two lines.
const legacyWebhookHeaders = "X Api: k\nX-Dup: 1\nx-dup: 2"

// TestAWebhookSavedWithOldHeadersCanStillBeEdited: a channel whose stored
// header lines predate the rules keeps them through an edit that sends them
// back unchanged, as its masked value, so it can still be renamed or
// switched off. An edit that changes them is held to the rules, and so is
// an import of its own export, which resolves the placeholder to the stored
// lines.
func TestAWebhookSavedWithOldHeadersCanStillBeEdited(t *testing.T) {
	srv, db := testServerWithDB(t)
	ch := seedChannel(t, db, store.ChannelWebhook,
		map[string]string{"url": "https://hooks.example.com/in", "headers": legacyWebhookHeaders})
	path := "/api/v1/channels/" + strconv.FormatInt(ch.ID, 10)
	seen := maskConfig(store.ChannelWebhook, ch.Config, true)

	put := func(name string, cfg map[string]string, enabled bool) (int, string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"name": name, "type": store.ChannelWebhook, "config": cfg, "enabled": enabled})
		must(t, err)
		rec := doJSON(t, srv, http.MethodPut, path, string(body))
		return rec.Code, rec.Body.String()
	}

	if code, body := put("Ops renamed", seen, false); code != http.StatusOK {
		t.Fatalf("rename and switch off with the stored headers = %d: %s", code, body)
	}
	got, err := db.GetChannel(t.Context(), ch.ID)
	must(t, err)
	if got.Name != "Ops renamed" || got.Enabled || got.Config["headers"] != legacyWebhookHeaders {
		t.Errorf("after the edit: name %q, enabled %v, headers %q", got.Name, got.Enabled, got.Config["headers"])
	}

	exported := exportYAML(t, srv)
	if code, _, body := importYAML(t, srv, exported, false); code != http.StatusOK {
		t.Errorf("import of its own export = %d: %s", code, body)
	}

	changed := map[string]string{"url": seen["url"], "headers": legacyWebhookHeaders + "\nX-New: v"}
	code, body := put("Ops renamed", changed, false)
	wantStatusProblem(t, code, body, "config.headers", "headers line 1 has no valid header name")
	if strings.Contains(body, "X-Dup") {
		t.Errorf("refusal quotes a header that is not the one refused: %s", body)
	}
	got, err = db.GetChannel(t.Context(), ch.ID)
	must(t, err)
	if got.Config["headers"] != legacyWebhookHeaders {
		t.Errorf("a refused edit changed the stored headers to %q", got.Config["headers"])
	}
}
