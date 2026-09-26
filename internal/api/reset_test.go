package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// signIn logs an account in and returns its session cookie. The reset deletes
// API tokens, so a test that wants to keep using the instance afterwards has
// to hold a session, exactly as the browser does.
func signIn(t *testing.T, h http.Handler, email string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"correct-horse-battery-staple"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()[0]
}

func sessionRequest(method, path, body string, cookie *http.Cookie) *http.Request {
	r := jsonRequest(method, path, body)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return r
}

func TestResetInstance(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	cookie := signIn(t, h, "admin@example.com")

	if _, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "api", Type: "http", Target: "https://example.com"}); err != nil {
		t.Fatal(err)
	}

	t.Run("a wrong phrase deletes nothing", func(t *testing.T) {
		for _, body := range []string{
			`{"confirm":"delete all data"}`,
			`{"confirm":"DELETE ALL DATA "}`,
			`{"confirm":""}`,
			`{}`,
		} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/v1/instance/reset", body, cookie))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400: %s", body, rec.Code, rec.Body.String())
			}
			var e errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&e); err != nil || e.Field != "confirm" {
				t.Errorf("%s: error = %+v (%v), want field confirm", body, e, err)
			}
		}
		monitors, err := db.ListMonitors(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(monitors) != 1 {
			t.Fatalf("%d monitors after refused resets, want 1", len(monitors))
		}
	})

	t.Run("the exact phrase empties the instance and keeps the session", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/v1/instance/reset", `{"confirm":"DELETE ALL DATA"}`, cookie))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var got resetResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		// One monitor, and the admin's seeded test token.
		if got.Monitors != 1 || got.APITokens != 1 {
			t.Errorf("response = %+v, want 1 monitor and 1 token", got)
		}

		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/v1/monitors", "", cookie))
		if rec.Code != http.StatusOK {
			t.Fatalf("list after reset: status = %d, want 200 — the operator must stay signed in", rec.Code)
		}
		monitors, err := db.ListMonitors(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(monitors) != 0 {
			t.Errorf("%d monitors after a reset, want 0", len(monitors))
		}
	})
}

// A reset is not scoped to the caller: it empties the instance for everyone,
// so an editor, who may otherwise delete monitors one at a time, is refused.
func TestResetInstanceIsAdminOnly(t *testing.T) {
	srv, db := testServerWithDB(t)
	editor := seedUser(t, srv, db, "editor@example.com", store.RoleEditor)
	if _, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "api", Type: "http", Target: "https://example.com"}); err != nil {
		t.Fatal(err)
	}

	req := jsonRequest(http.MethodPost, "/api/v1/instance/reset", `{"confirm":"DELETE ALL DATA"}`)
	req.Header.Set("Authorization", "Bearer "+editor)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	monitors, err := db.ListMonitors(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 1 {
		t.Errorf("%d monitors after a refused reset, want 1", len(monitors))
	}
}
