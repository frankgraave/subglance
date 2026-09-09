package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func TestUserManagement(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	// Create.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users",
		`{"email":"editor@example.com","password":"correct-horse-battery-staple","role":"editor"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var created userResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Role != "editor" {
		t.Errorf("role = %q, want editor", created.Role)
	}

	// List.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}

	var list struct {
		Users []userResponse `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Users) != 2 {
		t.Errorf("got %d users, want 2", len(list.Users))
	}

	// Delete.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodDelete, "/api/v1/users/"+itoa(created.ID), ""))
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

// Omitting the role must give the least privilege, not the most.
func TestCreateUserDefaultsToViewer(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users",
		`{"email":"nobody@example.com","password":"correct-horse-battery-staple"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created userResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Role != string(store.RoleViewer) {
		t.Errorf("default role = %q, want viewer — an omitted role must not grant power", created.Role)
	}
}

func TestCreateUserValidation(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	tests := []struct {
		name string
		body string
	}{
		{"no email", `{"password":"correct-horse-battery-staple"}`},
		{"malformed email", `{"email":"not-an-email","password":"correct-horse-battery-staple"}`},
		{"email is just @", `{"email":"@","password":"correct-horse-battery-staple"}`},
		{"weak password", `{"email":"a@example.com","password":"short"}`},
		{"unknown role", `{"email":"a@example.com","password":"correct-horse-battery-staple","role":"god"}`},
		{"malformed json", `{"email":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Deleting your own account is the other way to lock everyone out.
func TestCannotDeleteSelf(t *testing.T) {
	srv, db := testServerWithDB(t)

	users, err := db.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		jsonRequest(http.MethodDelete, "/api/v1/users/"+itoa(users[0].ID), ""))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDeleteUnknownUserIs404(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodDelete, "/api/v1/users/99999", ""))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestTokenValidation(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	tests := []struct {
		name string
		body string
	}{
		{"no name", `{}`},
		{"empty name", `{"name":""}`},
		{"malformed duration", `{"name":"x","expires_in":"tomorrow"}`},
		{"negative duration", `{"name":"x","expires_in":"-1h"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/tokens", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTokenWithExpiry(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		jsonRequest(http.MethodPost, "/api/v1/tokens", `{"name":"temporary","expires_in":"24h"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Details apiTokenResponse `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Details.ExpiresAt == nil {
		t.Error("expires_at was not set despite expires_in being given")
	}
}

func TestRevokeUnknownTokenIs404(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodDelete, "/api/v1/tokens/99999", ""))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestClientIPExtraction(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"remote addr", nil, "192.0.2.1:1234", "192.0.2.1"},
		{"x-forwarded-for", map[string]string{"X-Forwarded-For": "203.0.113.5"}, "10.0.0.1:1234", "203.0.113.5"},
		{"x-forwarded-for chain", map[string]string{"X-Forwarded-For": "203.0.113.5, 10.0.0.1"}, "10.0.0.1:1234", "203.0.113.5"},
		{"x-real-ip", map[string]string{"X-Real-Ip": "198.51.100.7"}, "10.0.0.1:1234", "198.51.100.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSameOriginDetection(t *testing.T) {
	tests := []struct {
		origin string
		host   string
		want   bool
	}{
		{"https://example.com", "example.com", true},
		{"http://example.com", "example.com", true},
		{"https://evil.com", "example.com", false},
		{"https://example.com.evil.com", "example.com", false},
		{"", "example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.Host = tt.host
			if got := sameOrigin(tt.origin, r); got != tt.want {
				t.Errorf("sameOrigin(%q, host=%q) = %v, want %v", tt.origin, tt.host, got, tt.want)
			}
		})
	}
}

// A non-browser client sends neither Sec-Fetch-Site nor Origin. Blocking it
// would break every curl and CI caller for no security gain: a cross-site
// attack needs a browser to carry the cookie in the first place.
func TestCSRFAllowsNonBrowserClients(t *testing.T) {
	srv, _ := testServerWithDB(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", nil)
	if !srv.checkCSRF(r) {
		t.Error("a request with no Fetch metadata and no Origin was blocked")
	}
}

func TestCSRFHonoursOriginWhenFetchMetadataIsAbsent(t *testing.T) {
	srv, _ := testServerWithDB(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", nil)
	r.Host = "subglance.example.com"
	r.Header.Set("Origin", "https://evil.example.com")

	if srv.checkCSRF(r) {
		t.Error("a request from a foreign Origin was allowed")
	}

	r.Header.Set("Origin", "https://subglance.example.com")
	if !srv.checkCSRF(r) {
		t.Error("a request from the matching Origin was blocked")
	}
}

func TestSetupStatusAfterSetup(t *testing.T) {
	srv, _ := testServerWithDB(t) // seeds an admin

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/setup", nil))

	var status map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status["setup_required"] {
		t.Error("setup_required = true although an account exists")
	}
}

func TestSetupRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no email", `{"password":"correct-horse-battery-staple"}`},
		{"malformed email", `{"email":"nope","password":"correct-horse-battery-staple"}`},
		{"malformed json", `{"email":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openEmptyDB(t)
			srv := New(testLogger(), db)

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestChangePasswordValidation(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))
	cookie := rec.Result().Cookies()[0]

	// A new password below the policy must be refused even with the correct
	// current one.
	req := jsonRequest(http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"correct-horse-battery-staple","new_password":"short"}`)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a weak new password", rec.Code)
	}
}

func TestLogoutWithoutSessionIsHarmless(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}
