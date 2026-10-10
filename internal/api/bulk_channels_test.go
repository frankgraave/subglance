package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func channelOpRequest(s *Server, path, body, etag string) *httptest.ResponseRecorder {
	r := jsonRequest(http.MethodPost, "/api/v1/monitors/channels"+path, body)
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	authedHandler(s).ServeHTTP(w, r)
	return w
}

func bulkChannel(t *testing.T, db *store.DB, name string, enabled bool) store.Channel {
	t.Helper()
	c, err := db.CreateChannel(t.Context(), store.Channel{
		Name: name, Type: store.ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/" + name}, Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func linkedIDs(t *testing.T, db *store.DB, monitorID int64) []int64 {
	t.Helper()
	channels, err := db.ListMonitorChannels(t.Context(), monitorID)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, c := range channels {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestBulkChannelsPreviewThenCommitAddsToTheSelection(t *testing.T) {
	s, db := testServerWithDB(t)
	ops, mail := bulkChannel(t, db, "ops", true), bulkChannel(t, db, "mail", true)
	a := seedMonitor(t, db, store.Monitor{Name: "a", Type: "http", Target: "https://example.com"})
	b := seedMonitor(t, db, store.Monitor{Name: "b", Type: "http", Target: "https://example.com"})
	if err := db.SetMonitorChannels(t.Context(), b.ID, []int64{mail.ID}); err != nil {
		t.Fatal(err)
	}
	body := `{"action":"add","monitor_ids":[` + itoa(a.ID) + `,` + itoa(b.ID) + `],"channel_id":` + itoa(ops.ID) + `}`

	preview := channelOpRequest(s, "/preview", body, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", preview.Code, preview.Body.String())
	}
	etag := preview.Header().Get("ETag")
	if !store.ValidChannelPreviewETag(etag) {
		t.Fatalf("preview ETag %q", etag)
	}
	var counts store.ChannelOperationResult
	if err := json.Unmarshal(preview.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts != (store.ChannelOperationResult{Total: 2, Changed: 2, ChannelEnabled: true}) {
		t.Fatalf("preview counts = %+v", counts)
	}
	if got := linkedIDs(t, db, a.ID); len(got) != 0 {
		t.Fatalf("preview wrote links: %v", got)
	}

	w := channelOpRequest(s, "", body, etag)
	if w.Code != http.StatusOK {
		t.Fatalf("commit = %d: %s", w.Code, w.Body.String())
	}
	if got := linkedIDs(t, db, a.ID); !slices.Equal(got, []int64{ops.ID}) {
		t.Fatalf("a links = %v", got)
	}
	if got := linkedIDs(t, db, b.ID); !slices.Equal(got, []int64{ops.ID, mail.ID}) {
		t.Fatalf("b links = %v", got)
	}
	if w.Header().Get("ETag") != "" {
		t.Fatal("a commit handed out a validator for a blind retry")
	}
}

func TestBulkChannelsRefusals(t *testing.T) {
	s, db := testServerWithDB(t)
	ops := bulkChannel(t, db, "ops", true)
	m := seedMonitor(t, db, store.Monitor{Name: "a", Type: "http", Target: "https://example.com"})
	id, ch := itoa(m.ID), itoa(ops.ID)
	valid := `{"action":"add","monitor_ids":[` + id + `],"channel_id":` + ch + `}`
	for name, tc := range map[string]struct {
		path, body, etag string
		want             int
	}{
		"unknown action":  {"/preview", `{"action":"replace","monitor_ids":[` + id + `],"channel_id":` + ch + `}`, "", 400},
		"replacement set": {"/preview", `{"action":"add","monitor_ids":[` + id + `],"channel_ids":[` + ch + `]}`, "", 400},
		"unknown channel": {"/preview", `{"action":"add","monitor_ids":[` + id + `],"channel_id":` + itoa(ops.ID+99) + `}`, "", 400},
		"missing monitor": {"/preview", `{"action":"add","monitor_ids":[` + itoa(m.ID+99) + `],"channel_id":` + ch + `}`, "", 404},
		"two objects":     {"/preview", valid + valid, "", 400},
		"no precondition": {"", valid, "", 428},
		"weak validator":  {"", valid, `W/"channels-` + strings.Repeat("0", 64) + `"`, 400},
		"a tag validator": {"", valid, `"tags-` + strings.Repeat("0", 64) + `"`, 400},
		"stale validator": {"", valid, `"channels-` + strings.Repeat("0", 64) + `"`, 412},
	} {
		t.Run(name, func(t *testing.T) {
			w := channelOpRequest(s, tc.path, tc.body, tc.etag)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if got := linkedIDs(t, db, m.ID); len(got) != 0 {
		t.Fatalf("a refused request wrote links: %v", got)
	}
}

func TestBulkChannelsPermissions(t *testing.T) {
	s, db := testServerWithDB(t)
	for _, path := range []string{"/api/v1/monitors/channels", "/api/v1/monitors/channels/preview"} {
		for _, role := range []store.Role{"", store.RoleViewer, store.RoleEditor, store.RoleAdmin} {
			t.Run(path+string(role), func(t *testing.T) {
				r := jsonRequest(http.MethodPost, path, `{"action":"add","monitor_ids":[1],"channel_id":1}`)
				r.Header.Set("If-Match", `"channels-`+strings.Repeat("0", 64)+`"`)
				if role != "" {
					token := seedUser(t, s, db, strings.ReplaceAll(path, "/", "")+string(role)+"@example.com", role)
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				switch {
				case role == "" && w.Code != http.StatusUnauthorized,
					role == store.RoleViewer && w.Code != http.StatusForbidden,
					(role == store.RoleEditor || role == store.RoleAdmin) && (w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden):
					t.Fatalf("role %q = %d: %s", role, w.Code, w.Body.String())
				}
			})
		}
	}
}
