package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func TestMonitorDetailReadsCheckSettings(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "empty and zero"}[empty], func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "configured", Type: "http", Target: "https://example.com/health", Enabled: true,
				Method: "POST", ExpectedStatus: "201-204", Keyword: "healthy", KeywordMode: "must_not_contain", FollowRedirects: false,
				Headers: map[string]string{"Authorization": "Bearer private", "X-Empty": ""}, Body: "{\"token\":\"private\"}", Retries: 4, SSLWarnDays: 30,
				CaptureResponse: true, RepeatAfterS: 600})
			if empty {
				// Explicit empty/zero settings must survive the read unchanged.
				m.Method = ""
				m.ExpectedStatus = ""
				m.Keyword = ""
				m.Headers = map[string]string{}
				m.Body = ""
				m.Retries = 0
				m.CaptureResponse = false
				m.RepeatAfterS = 0
				var err error
				m, err = db.UpdateMonitor(t.Context(), m)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Compare the read contract to storage, including its canonical empty maps.
			var err error
			m, err = db.GetMonitor(t.Context(), m.ID)
			if err != nil {
				t.Fatal(err)
			}
			rec := getMonitorRaw(t, srv, m.ID)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET status %d: %s", rec.Code, rec.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			headers := map[string]any{}
			for k, v := range m.Headers {
				headers[k] = v
			}
			want := map[string]any{"type": m.Type, "target": m.Target, "method": m.Method, "expected_status": m.ExpectedStatus,
				"keyword": m.Keyword, "keyword_mode": m.KeywordMode, "follow_redirects": m.FollowRedirects,
				"headers": headers, "body": m.Body, "retries": float64(m.Retries), "ssl_warn_days": float64(m.SSLWarnDays),
				"capture_response": m.CaptureResponse, "repeat_after_s": float64(m.RepeatAfterS)}
			for key, value := range want {
				if !reflect.DeepEqual(got[key], value) {
					t.Errorf("detail %s = %#v, want raw stored %#v", key, got[key], value)
				}
			}
			if rec.Header().Get("ETag") != monitorETag(m) {
				t.Fatal("settings must be paired with their own ETag")
			}
			patched := patchWithHeaders(t, srv, monitorPath(m.ID), `{"name":"renamed"}`, map[string]string{"If-Match": rec.Header().Get("ETag")})
			if patched.Code != http.StatusOK {
				t.Fatalf("conditional rename: %d %s", patched.Code, patched.Body.String())
			}
			after, err := db.GetMonitor(t.Context(), m.ID)
			if err != nil {
				t.Fatal(err)
			}
			after.Name = m.Name
			after.UpdatedAt = m.UpdatedAt
			if !reflect.DeepEqual(after, m) {
				t.Fatalf("rename changed unseen configuration: got %+v want %+v", after, m)
			}
			stale := patchWithHeaders(t, srv, monitorPath(m.ID), `{"body":"overwritten"}`, map[string]string{"If-Match": rec.Header().Get("ETag")})
			if stale.Code != http.StatusPreconditionFailed {
				t.Fatalf("stale settings ETag: %d, want 412", stale.Code)
			}
		})
	}
}

func TestMonitorDetailRequestSecretsRequireWriteRole(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "private", Type: "http", Target: "https://example.com", Headers: map[string]string{"Authorization": "private-header"}, Body: "private-body"})
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		t.Run(string(role), func(t *testing.T) {
			token := seedUser(t, srv, db, string(role)+"@example.com", role)
			req := httptest.NewRequest(http.MethodGet, monitorPath(m.ID), nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("read status: %d", rec.Code)
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"headers", "body"} {
				_, present := got[key]
				if present != role.CanWrite() {
					t.Errorf("%s: %s presence = %v, want %v", role, key, present, role.CanWrite())
				}
			}
			if got["method"] != m.Method {
				t.Errorf("viewer-safe settings missing: method = %v", got["method"])
			}
			if rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Error("sensitive detail must not be cached")
			}
		})
	}
}

func TestMonitorSettingsStayOffBulkResponses(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "private", Type: "http", Target: "https://example.com", Headers: map[string]string{"Authorization": "secret-header"}, Body: "secret-body"})
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	var got struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 1 {
		t.Fatalf("monitors = %d", len(got.Monitors))
	}
	for _, key := range []string{"headers", "body"} {
		if _, ok := got.Monitors[0][key]; ok {
			t.Errorf("bulk response leaks %s", key)
		}
	}
	unauth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, monitorPath(m.ID), nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated settings status = %d", unauth.Code)
	}
}
