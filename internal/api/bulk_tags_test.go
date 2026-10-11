package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func tagRequest(s *Server, path, body, etag string) *httptest.ResponseRecorder {
	r := jsonRequest(http.MethodPost, "/api/v1/monitors/tags"+path, body)
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	authedHandler(s).ServeHTTP(w, r)
	return w
}

func previewTags(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := tagRequest(s, "/preview", body, "")
	if w.Code != 200 {
		t.Fatalf("preview status = %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == "" {
		t.Fatal("preview must carry its snapshot ETag")
	}
	return w
}

func assertTagCounts(t *testing.T, w *httptest.ResponseRecorder, total, changed, collisions int) {
	t.Helper()
	var got struct{ Total, Changed, Unchanged, Collisions int }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != total || got.Changed != changed || got.Unchanged != total-changed || got.Collisions != collisions {
		t.Fatalf("counts = %+v; want total=%d changed=%d unchanged=%d collisions=%d", got, total, changed, total-changed, collisions)
	}
}

func TestBulkTagsRemoveOnlyExactSelectedPair(t *testing.T) {
	s, db := testServerWithDB(t)
	ids := []int64{}
	for _, value := range []string{"prod", "staging", "prod"} {
		m := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": value, "team": "ops"}})
		ids = append(ids, m.ID)
	}
	body := `{"action":"remove","monitor_ids":[` + itoa(ids[0]) + `,` + itoa(ids[1]) + `],"key":"env","value":"prod"}`
	preview := previewTags(t, s, body)
	assertTagCounts(t, preview, 2, 1, 0)
	w := tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 200 {
		t.Fatalf("remove = %d: %s", w.Code, w.Body.String())
	}
	assertTagCounts(t, w, 2, 1, 0)
	for i, id := range ids {
		m, _ := db.GetMonitor(t.Context(), id)
		want := []string{"", "staging", "prod"}[i]
		if m.Tags["env"] != want || m.Tags["team"] != "ops" {
			t.Fatalf("remove monitor %d tags=%v", id, m.Tags)
		}
	}
}

func TestBulkTagsRenameValueOnlyMatchingKeyAndValue(t *testing.T) {
	s, db := testServerWithDB(t)
	fixtures := []map[string]string{{"env": "old", "team": "old"}, {"env": "old"}, {"env": "other"}, {"team": "old"}}
	ids := []int64{}
	for _, tags := range fixtures {
		m := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: tags})
		ids = append(ids, m.ID)
	}
	body := `{"action":"rename_value","key":"env","value":"old","new_value":"New:Value"}`
	preview := previewTags(t, s, body)
	assertTagCounts(t, preview, 2, 2, 0)
	w := tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 200 {
		t.Fatalf("rename value = %d: %s", w.Code, w.Body.String())
	}
	assertTagCounts(t, w, 2, 2, 0)
	for i, id := range ids {
		m, _ := db.GetMonitor(t.Context(), id)
		want := fixtures[i]
		if i < 2 {
			want["env"] = "New:Value"
		}
		if !reflect.DeepEqual(m.Tags, want) {
			t.Fatalf("value rename monitor %d tags=%v want=%v", id, m.Tags, want)
		}
	}
}

func TestBulkTagsRenameKeyMergesEveryMatch(t *testing.T) {
	s, db := testServerWithDB(t)
	ids := []int64{}
	for i := 0; i < 251; i++ {
		tags := map[string]string{"evn": "prod", "team": "ops"}
		if i%2 == 0 {
			tags["env"] = "staging"
		}
		m := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: tags})
		ids = append(ids, m.ID)
	}
	untouched := seedMonitor(t, db, store.Monitor{Name: "untouched", Type: "tcp", Target: "localhost:80", Tags: map[string]string{"env": "local"}})
	body := `{"action":"rename_key","key":"evn","new_key":"env"}`
	preview := previewTags(t, s, body)
	assertTagCounts(t, preview, 251, 251, 126)
	w := tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 200 {
		t.Fatalf("rename status = %d: %s", w.Code, w.Body.String())
	}
	assertTagCounts(t, w, 251, 251, 126)
	for i, id := range ids {
		m, err := db.GetMonitor(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		want := "prod"
		if i%2 == 0 {
			want = "staging"
		}
		if len(m.Tags) != 2 || m.Tags["env"] != want || m.Tags["team"] != "ops" {
			t.Fatalf("rename monitor %d tags = %v, want env=%s team=ops", id, m.Tags, want)
		}
	}
	after, _ := db.GetMonitor(t.Context(), untouched.ID)
	if !reflect.DeepEqual(after.Tags, untouched.Tags) || !after.UpdatedAt.Equal(untouched.UpdatedAt) {
		t.Fatal("rename touched a nonmatching monitor")
	}
	empty := previewTags(t, s, body)
	assertTagCounts(t, empty, 0, 0, 0)
}

func TestBulkTagsApplyPreviewCommitPreservesSettings(t *testing.T) {
	s, db := testServerWithDB(t)
	a := seedMonitor(t, db, store.Monitor{Name: "alpha", Type: "http", Target: "https://example.com", Tags: map[string]string{"team": "ops"}, Headers: map[string]string{"X-Keep": "yes"}, Body: "keep", RepeatAfterS: 600})
	b := seedMonitor(t, db, store.Monitor{Name: "beta", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	body := `{"action":"apply","monitor_ids":[` + itoa(a.ID) + `,` + itoa(b.ID) + `],"key":" Env ","value":" prod "}`
	preview := previewTags(t, s, body)
	assertTagCounts(t, preview, 2, 1, 0)
	before, _ := db.GetMonitor(t.Context(), a.ID)
	if !reflect.DeepEqual(before.Tags, a.Tags) {
		t.Fatal("preview changed tags")
	}
	w := tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 200 {
		t.Fatalf("commit = %d: %s", w.Code, w.Body.String())
	}
	assertTagCounts(t, w, 2, 1, 0)
	after, err := db.GetMonitor(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Tags["env"] != "prod" || after.Tags["team"] != "ops" {
		t.Fatalf("apply tags = %v", after.Tags)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatal("changed monitor revision did not advance")
	}
	after.Tags = before.Tags
	after.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("apply changed unrelated settings: %+v", after)
	}
	unchanged, _ := db.GetMonitor(t.Context(), b.ID)
	if !unchanged.UpdatedAt.Equal(b.UpdatedAt) {
		t.Fatal("no-op apply changed revision")
	}
	stale := patchWithHeaders(t, s, monitorPath(a.ID), `{"name":"stale"}`, map[string]string{"If-Match": monitorETag(before)})
	if stale.Code != 412 {
		t.Fatalf("old monitor edit = %d, want 412", stale.Code)
	}
}

func TestBulkTagsRenameReportsAndMovesRoutingRules(t *testing.T) {
	s, db := testServerWithDB(t)
	seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	rule, err := db.CreateRoutingRule(t.Context(), store.RoutingRule{TagKey: "env", TagValue: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"action":"rename_value","key":"env","value":"prod","new_value":"production"}`
	preview := previewTags(t, s, body)
	var counts struct {
		RoutingRules       *int `json:"routing_rules"`
		MaintenanceWindows *int `json:"maintenance_windows"`
		StatusPages        *int `json:"status_pages"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts.RoutingRules == nil || *counts.RoutingRules != 1 || counts.MaintenanceWindows == nil || *counts.MaintenanceWindows != 0 || counts.StatusPages == nil || *counts.StatusPages != 0 {
		t.Fatalf("preview follower counts = %s; want routing_rules 1 and the other two present at 0", preview.Body.String())
	}
	if w := tagRequest(s, "", body, preview.Header().Get("ETag")); w.Code != 200 {
		t.Fatalf("rename = %d: %s", w.Code, w.Body.String())
	}
	got, err := db.GetRoutingRule(t.Context(), rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TagValue != "production" {
		t.Fatalf("rule still names %s=%s after the rename", got.TagKey, got.TagValue)
	}
}

func TestBulkTagsRenameOntoAnotherRulesPairIs409(t *testing.T) {
	s, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	for _, value := range []string{"prod", "production"} {
		if _, err := db.CreateRoutingRule(t.Context(), store.RoutingRule{TagKey: "env", TagValue: value}); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"action":"rename_value","key":"env","value":"prod","new_value":"production"}`
	w := tagRequest(s, "/preview", body, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("preview = %d: %s; want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "env=production") {
		t.Fatalf("409 does not name the pair already taken: %s", w.Body.String())
	}
	after, _ := db.GetMonitor(t.Context(), m.ID)
	if after.Tags["env"] != "prod" {
		t.Fatalf("refused rename changed the monitor: %v", after.Tags)
	}
}
