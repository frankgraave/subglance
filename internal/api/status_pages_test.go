package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func decodeStatusPageBody(t *testing.T, rec *httptest.ResponseRecorder) statusPageResponse {
	t.Helper()
	var got statusPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status page: %v: %s", err, rec.Body.String())
	}
	return got
}

func mustStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status = %d, want %d: %s", what, rec.Code, want, rec.Body.String())
	}
}

func TestStatusPagesCanBeCreatedChangedAndDeleted(t *testing.T) {
	srv, db := testServerWithDB(t)
	api := seedMonitor(t, db, store.Monitor{Name: "api-prod-eu", Type: "http", Target: "https://api.example.com"})
	web := seedMonitor(t, db, store.Monitor{Name: "web-prod-eu", Type: "http", Target: "https://www.example.com"})

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":" Acme ","title":" Acme services ","selection":"monitors"}`)
	mustStatus(t, rec, http.StatusCreated, "create")
	page := decodeStatusPageBody(t, rec)
	if page.Slug != "acme" || page.Title != "Acme services" || page.Timezone != "UTC" {
		t.Errorf("created = %+v, want the slug lowercased, the title trimmed and UTC", page)
	}
	// Saving a page must publish nothing until it is switched on.
	if page.Enabled || page.Indexable {
		t.Errorf("enabled, indexable = %v, %v; want a new page off and unindexed", page.Enabled, page.Indexable)
	}
	if page.Entries == nil || page.UnnamedMonitorIDs == nil {
		t.Error("entries and unnamed_monitor_ids must be [] rather than null")
	}

	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
		`{"entries":[{"monitor_id":`+itoa(api.ID)+`,"display_name":" API "},{"monitor_id":`+itoa(web.ID)+`,"display_name":"Website"}]}`)
	mustStatus(t, rec, http.StatusOK, "set entries")
	withEntries := decodeStatusPageBody(t, rec)
	if len(withEntries.Entries) != 2 || withEntries.Entries[0].DisplayName != "API" || withEntries.Entries[1].MonitorID != web.ID {
		t.Fatalf("entries = %+v, want API then Website in the order sent", withEntries.Entries)
	}
	keys := map[int64]string{}
	for _, e := range withEntries.Entries {
		if len(e.PublicKey) != 16 {
			t.Errorf("public key %q is not 16 hex characters", e.PublicKey)
		}
		keys[e.MonitorID] = e.PublicKey
	}

	// Reordering and renaming keeps each monitor's public key.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/ACME/entries",
		`{"entries":[{"monitor_id":`+itoa(web.ID)+`,"display_name":"Site"},{"monitor_id":`+itoa(api.ID)+`,"display_name":"API"}]}`)
	mustStatus(t, rec, http.StatusOK, "reorder entries")
	for _, e := range decodeStatusPageBody(t, rec).Entries {
		if keys[e.MonitorID] != e.PublicKey {
			t.Errorf("monitor %d: public key changed from %q to %q on a reorder", e.MonitorID, keys[e.MonitorID], e.PublicKey)
		}
	}

	// PUT replaces the settings, the slug included; the entries stay.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme-status","title":"Acme","timezone":"Europe/Amsterdam","selection":"monitors","enabled":true}`)
	mustStatus(t, rec, http.StatusOK, "update")
	updated := decodeStatusPageBody(t, rec)
	if updated.ID != page.ID || updated.Slug != "acme-status" || !updated.Enabled || updated.Timezone != "Europe/Amsterdam" {
		t.Errorf("updated = %+v", updated)
	}
	if len(updated.Entries) != 2 {
		t.Errorf("an update of the settings dropped the entries: %+v", updated.Entries)
	}
	mustStatus(t, doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"A","selection":"monitors"}`), http.StatusNotFound, "update under the old slug")

	rec = doJSON(t, srv, http.MethodGet, "/api/v1/status-pages", "")
	mustStatus(t, rec, http.StatusOK, "list")
	var list struct {
		Pages []statusPageResponse `json:"pages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Pages) != 1 || list.Pages[0].Slug != "acme-status" || len(list.Pages[0].Entries) != 2 {
		t.Fatalf("list = %+v", list.Pages)
	}

	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/status-pages/acme-status", "")
	mustStatus(t, rec, http.StatusNoContent, "delete")
	mustStatus(t, doJSON(t, srv, http.MethodDelete, "/api/v1/status-pages/acme-status", ""), http.StatusNotFound, "second delete")
	if pages, err := db.ListStatusPages(t.Context()); err != nil || len(pages) != 0 {
		t.Fatalf("pages after delete = %v, %v", pages, err)
	}
}

// Every refusal names the field it is about, so the editor can place the
// message beside the input instead of matching on its wording.
func TestStatusPageRefusalsNameTheField(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "m", Type: "http", Target: "https://example.com"})
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"taken","title":"T","selection":"monitors"}`), http.StatusCreated, "seed page")

	for name, tc := range map[string]struct {
		method, path, body string
		status             int
		field              string
	}{
		"bad slug":           {http.MethodPost, "/api/v1/status-pages", `{"slug":"my page","title":"T","selection":"monitors"}`, http.StatusBadRequest, "slug"},
		"reserved slug":      {http.MethodPost, "/api/v1/status-pages", `{"slug":"api","title":"T","selection":"monitors"}`, http.StatusBadRequest, "slug"},
		"slug taken":         {http.MethodPost, "/api/v1/status-pages", `{"slug":"TAKEN","title":"T","selection":"monitors"}`, http.StatusConflict, "slug"},
		"no title":           {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":" ","selection":"monitors"}`, http.StatusBadRequest, "title"},
		"long description":   {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"monitors","description":"` + strings.Repeat("d", 501) + `"}`, http.StatusBadRequest, "description"},
		"unknown zone":       {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"monitors","timezone":"Mars/Olympus"}`, http.StatusBadRequest, "timezone"},
		"unknown selection":  {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"all"}`, http.StatusBadRequest, "selection"},
		"bad tag key":        {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"tag","tag_key":"has space","tag_value":"v"}`, http.StatusBadRequest, "tag_key"},
		"tag without value":  {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"tag","tag_key":"env"}`, http.StatusBadRequest, "tag_value"},
		"tag on monitors":    {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"monitors","tag_key":"env","tag_value":"prod"}`, http.StatusBadRequest, "tag_key"},
		"unknown field":      {http.MethodPost, "/api/v1/status-pages", `{"slug":"x","title":"T","selection":"monitors","password":"p"}`, http.StatusBadRequest, ""},
		"unknown monitor":    {http.MethodPut, "/api/v1/status-pages/taken/entries", `{"entries":[{"monitor_id":4242,"display_name":"X"}]}`, http.StatusBadRequest, "entries"},
		"unnamed entry":      {http.MethodPut, "/api/v1/status-pages/taken/entries", `{"entries":[{"monitor_id":` + itoa(m.ID) + `,"display_name":""}]}`, http.StatusBadRequest, "entries"},
		"entries omitted":    {http.MethodPut, "/api/v1/status-pages/taken/entries", `{}`, http.StatusBadRequest, "entries"},
		"entries of nothing": {http.MethodPut, "/api/v1/status-pages/nope/entries", `{"entries":[]}`, http.StatusNotFound, ""},
		"update of nothing":  {http.MethodPut, "/api/v1/status-pages/nope", `{"slug":"nope","title":"T","selection":"monitors"}`, http.StatusNotFound, ""},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, srv, tc.method, tc.path, tc.body)
			mustStatus(t, rec, tc.status, name)
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error == "" || body.Field != tc.field {
				t.Errorf("error = %q, field = %q; want a message and field %q", body.Error, body.Field, tc.field)
			}
		})
	}

	// A refused entry list changes nothing.
	pages, err := db.ListStatusPages(t.Context())
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages = %v, %v; want only the seeded one", pages, err)
	}
	if entries, _ := db.ListStatusPageEntries(t.Context(), pages[0].ID); len(entries) != 0 {
		t.Errorf("a refused entry list stored %d entries", len(entries))
	}
}

// A tag page lists the tagged monitors that still need a public name, and an
// entry for a monitor that lost the tag is kept but reported by id only.
func TestTagStatusPageListsMonitorsThatNeedAName(t *testing.T) {
	srv, db := testServerWithDB(t)
	named := seedMonitor(t, db, store.Monitor{Name: "named", Type: "http", Target: "https://a.example", Tags: map[string]string{"customer": "Acme"}})
	unnamed := seedMonitor(t, db, store.Monitor{Name: "unnamed", Type: "http", Target: "https://b.example", Tags: map[string]string{"customer": "Acme"}})
	seedMonitor(t, db, store.Monitor{Name: "other", Type: "http", Target: "https://c.example", Tags: map[string]string{"customer": "Globex"}})

	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"acme","title":"Acme","selection":"tag","tag_key":" Customer ","tag_value":"Acme"}`), http.StatusCreated, "create")
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
		`{"entries":[{"monitor_id":`+itoa(named.ID)+`,"display_name":"Portal"}]}`)
	mustStatus(t, rec, http.StatusOK, "set entries")
	page := decodeStatusPageBody(t, rec)
	if page.TagKey != "customer" || page.TagValue != "Acme" {
		t.Errorf("tag = %q=%q, want customer=Acme", page.TagKey, page.TagValue)
	}
	if len(page.UnnamedMonitorIDs) != 1 || page.UnnamedMonitorIDs[0] != unnamed.ID {
		t.Errorf("unnamed_monitor_ids = %v, want [%d]", page.UnnamedMonitorIDs, unnamed.ID)
	}
}

// Managing status pages is an administrator's job: an editor may change
// monitors, but not what the instance publishes about them.
func TestStatusPagesAreAdminOnly(t *testing.T) {
	srv, db := testServerWithDB(t)
	editor := seedUser(t, srv, db, "editor@example.com", store.RoleEditor)
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)
	routes := [][2]string{
		{http.MethodGet, "/api/v1/status-pages"},
		{http.MethodPost, "/api/v1/status-pages"},
		{http.MethodPut, "/api/v1/status-pages/acme"},
		{http.MethodDelete, "/api/v1/status-pages/acme"},
		{http.MethodPut, "/api/v1/status-pages/acme/entries"},
	}
	do := func(token, method, path string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	for _, rt := range routes {
		if code := do("", rt[0], rt[1]); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: status = %d, want 401", rt[0], rt[1], code)
		}
		for role, token := range map[string]string{"editor": editor, "viewer": viewer} {
			if code := do(token, rt[0], rt[1]); code != http.StatusForbidden {
				t.Errorf("%s %s %s: status = %d, want 403", role, rt[0], rt[1], code)
			}
		}
	}
}
