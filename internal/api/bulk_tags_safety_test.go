package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func TestBulkTagsRejectInvalidRequestsAtomically(t *testing.T) {
	s, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "safe", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	base := `{"action":"apply","monitor_ids":[` + itoa(m.ID) + `],"key":"env","value":"new"}`
	tooMany := make([]int64, 10001)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	manyJSON, _ := json.Marshal(tooMany)
	tests := []struct {
		name, body string
		status     int
	}{
		{"empty", "", 400}, {"null", "null", 400}, {"unknown action", strings.Replace(base, "apply", "explode", 1), 400},
		{"unknown field", strings.TrimSuffix(base, "}") + `,"typo":true}`, 400},
		{"trailing JSON", base + ` {}`, 400}, {"trailing garbage", base + ` !`, 400},
		{"oversized", base + strings.Repeat(" ", 256<<10), 400},
		{"empty selection", strings.Replace(base, `[`+itoa(m.ID)+`]`, `[]`, 1), 400},
		{"duplicate ID", strings.Replace(base, `[`+itoa(m.ID)+`]`, `[`+itoa(m.ID)+`,`+itoa(m.ID)+`]`, 1), 400},
		{"negative ID", strings.Replace(base, `[`+itoa(m.ID)+`]`, `[-1]`, 1), 400},
		{"fractional ID", strings.Replace(base, `[`+itoa(m.ID)+`]`, `[1.5]`, 1), 400},
		{"unknown ID", strings.Replace(base, `[`+itoa(m.ID)+`]`, `[`+itoa(m.ID)+`,99999]`, 1), 404},
		{"too many IDs", strings.Replace(base, `[`+itoa(m.ID)+`]`, string(manyJSON), 1), 400},
		{"invalid key", strings.Replace(base, `"key":"env"`, `"key":"bad key"`, 1), 400},
		{"empty value", strings.Replace(base, `"value":"new"`, `"value":""`, 1), 400},
		{"long value", strings.Replace(base, `"value":"new"`, `"value":"`+strings.Repeat("v", 65)+`"`, 1), 400},
		{"unexpected destination", strings.TrimSuffix(base, "}") + `,"new_key":"other"}`, 400},
		{"global selection", `{"action":"rename_key","monitor_ids":[1],"key":"env","new_key":"environment"}`, 400},
		{"empty global selection", `{"action":"rename_key","monitor_ids":[],"key":"env","new_key":"environment"}`, 400},
		{"null global selection", `{"action":"rename_key","monitor_ids":null,"key":"env","new_key":"environment"}`, 400},
		{"empty unexpected field", strings.TrimSuffix(base, "}") + `,"new_value":""}`, 400},
		{"same key", `{"action":"rename_key","key":" Env ","new_key":"env"}`, 400},
		{"bad new key", `{"action":"rename_key","key":"env","new_key":"bad key"}`, 400},
		{"same value", `{"action":"rename_value","key":"env","value":"prod","new_value":"prod"}`, 400},
		{"bad new value", `{"action":"rename_value","key":"env","value":"prod","new_value":"a\tb"}`, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := tagRequest(s, "/preview", tc.body, "")
			if w.Code != tc.status {
				t.Fatalf("preview = %d: %s, want %d", w.Code, w.Body.String(), tc.status)
			}
			after, _ := db.GetMonitor(t.Context(), m.ID)
			if !reflect.DeepEqual(after.Tags, m.Tags) || !after.UpdatedAt.Equal(m.UpdatedAt) {
				t.Fatal("invalid request changed tags or revision")
			}
		})
	}
}

func TestBulkTagsMixedValidInvalidRollbackAndDatabaseFailure(t *testing.T) {
	s, db := testServerWithDB(t)
	a := seedMonitor(t, db, store.Monitor{Name: "first", Type: "http", Target: "https://example.com"})
	b := seedMonitor(t, db, store.Monitor{Name: "second", Type: "http", Target: "https://example.com"})
	body := `{"action":"apply","monitor_ids":[` + itoa(a.ID) + `,` + itoa(b.ID) + `],"key":"new","value":"yes"}`
	preview := previewTags(t, s, body)
	b.Tags = map[string]string{}
	for i := 0; i < 20; i++ {
		b.Tags[fmt.Sprintf("key%d", i)] = "v"
	}
	var err error
	b, err = db.UpdateMonitor(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	w := tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 400 {
		t.Fatalf("tag limit commit = %d: %s", w.Code, w.Body.String())
	}
	after, _ := db.GetMonitor(t.Context(), a.ID)
	if len(after.Tags) != 0 || !after.UpdatedAt.Equal(a.UpdatedAt) {
		t.Fatal("valid first row was partially committed")
	}
	b.Tags = nil
	b, err = db.UpdateMonitor(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	preview = previewTags(t, s, body)
	// A real SQL write failure on the SECOND row must roll back the first row,
	// including the version bump. Validation alone does not prove transactions.
	_, err = db.Writer.ExecContext(t.Context(), fmt.Sprintf(`CREATE TRIGGER reject_tag BEFORE INSERT ON monitor_tags WHEN NEW.monitor_id=%d BEGIN SELECT RAISE(ABORT,'test failure'); END`, b.ID))
	if err != nil {
		t.Fatal(err)
	}
	w = tagRequest(s, "", body, preview.Header().Get("ETag"))
	if w.Code != 500 {
		t.Fatalf("DB commit = %d: %s", w.Code, w.Body.String())
	}
	for _, before := range []store.Monitor{a, b} {
		after, _ := db.GetMonitor(t.Context(), before.ID)
		if len(after.Tags) != 0 || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("DB failure partially changed monitor %d", before.ID)
		}
	}
	if err := db.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	if w := tagRequest(s, "/preview", body, ""); w.Code != 500 {
		t.Fatalf("DB read = %d, must not claim empty/success", w.Code)
	}
}

func TestBulkTagsPreconditionsProtectIntentAndCollisions(t *testing.T) {
	for _, change := range []string{"source", "destination", "membership", "request"} {
		t.Run(change, func(t *testing.T) {
			s, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: map[string]string{"evn": "prod"}})
			body := `{"action":"rename_key","key":"evn","new_key":"env"}`
			preview := previewTags(t, s, body)
			for _, header := range []string{"", "*", `W/"tags-invalid"`, `"other"`, `"tags-` + strings.Repeat("g", 64) + `"`} {
				w := tagRequest(s, "", body, header)
				want := 400
				if header == "" {
					want = 428
				}
				if w.Code != want {
					t.Fatalf("precondition %q = %d, want %d", header, w.Code, want)
				}
			}
			switch change {
			case "source":
				m.Tags["evn"] = "staging"
			case "destination":
				m.Tags["env"] = "staging"
			case "membership":
				seedMonitor(t, db, store.Monitor{Name: "new match", Type: "http", Target: "https://example.com", Tags: map[string]string{"evn": "prod"}})
			case "request":
				body = strings.Replace(body, `"new_key":"env"`, `"new_key":"environment"`, 1)
			}
			if change == "source" || change == "destination" {
				var err error
				m, err = db.UpdateMonitor(t.Context(), m)
				if err != nil {
					t.Fatal(err)
				}
			}
			w := tagRequest(s, "", body, preview.Header().Get("ETag"))
			if w.Code != 412 {
				t.Fatalf("stale preview = %d: %s", w.Code, w.Body.String())
			}
			after, _ := db.GetMonitor(t.Context(), m.ID)
			if !reflect.DeepEqual(after.Tags, m.Tags) {
				t.Fatal("conflict changed tags")
			}
			if w.Header().Get("ETag") != "" {
				t.Fatal("conflict handed out a fresh validator for blind retry")
			}
		})
	}
}

func TestBulkTagsPermissions(t *testing.T) {
	s, db := testServerWithDB(t)
	for _, path := range []string{"/api/v1/monitors/tags", "/api/v1/monitors/tags/preview"} {
		for _, role := range []store.Role{"", store.RoleViewer, store.RoleEditor, store.RoleAdmin} {
			t.Run(path+string(role), func(t *testing.T) {
				r := jsonRequest(http.MethodPost, path, `{"action":"rename_key","key":"old","new_key":"new"}`)
				r.Header.Set("If-Match", `"tags-`+strings.Repeat("0", 64)+`"`)
				if role != "" {
					token := seedUser(t, s, db, path+string(role)+"@example.com", role)
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				want := 200
				if role == "" {
					want = 401
				} else if role == store.RoleViewer {
					want = 403
				} else if path == "/api/v1/monitors/tags" {
					want = 412
				}
				if w.Code != want {
					t.Fatalf("role %s = %d: %s want %d", role, w.Code, w.Body.String(), want)
				}
			})
		}
	}
}
