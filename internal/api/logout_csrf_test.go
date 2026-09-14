package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// TestCrossSiteLogoutIsRejected reproduces the forced logout.
//
// Logout is registered as a public route, so the CSRF check inside
// authenticate() never runs for it. Any page on the internet could then end a
// visitor's session, which is not data loss but is an operator locked out of a
// dashboard during an incident.
func TestCrossSiteLogoutIsRejected(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "victim@example.com",
		"correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"victim@example.com","password":"correct-horse-battery-staple"}`))
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusNoContent {
		t.Error("a cross-site POST to logout was accepted")
	}

	// And the session must still work afterwards.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("the victim's session was ended by a cross-site request (status %d)", rec.Code)
	}
}
