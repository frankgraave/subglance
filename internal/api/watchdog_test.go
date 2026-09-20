package api

import (
	"encoding/json"
	"github.com/frankgraave/subglance/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWatchdogReadAccessAndDisabledState(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithWatchdog(nil)
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor, store.RoleAdmin} {
		token := seedUser(t, srv, db, string(role)+"@watchdog.test", role)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/watchdog", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s cannot read: %d", role, rec.Code)
		}
		var data map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data) != 11 || data["configured"] != false || data["last_success_at"] != nil || data["last_result"] != nil || data["last_status_code"] != nil {
			t.Fatalf("wrong disabled shape: %s", rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("cacheable diagnostic")
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/watchdog", nil))
	if rec.Code != 401 || strings.Contains(rec.Body.String(), "configured") {
		t.Fatalf("public diagnostic: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/watchdog", nil))
	if rec.Code < 400 {
		t.Fatalf("read-only diagnostic accepted POST: %d", rec.Code)
	}
}

func TestWatchdogUnknownSourceIsNotDisabled(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/watchdog", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired source = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("diagnostics cache policy = %q", rec.Header().Get("Cache-Control"))
	}
}
