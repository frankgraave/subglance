package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The whole point of this issue: nothing that matters is reachable without
// credentials. If this test ever passes trivially, the middleware is gone.
func TestProtectedRoutesRejectAnonymous(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := srv.Handler() // no credentials attached

	protected := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/monitors"},
		{http.MethodPost, "/api/v1/monitors"},
		{http.MethodGet, "/api/v1/monitors/1"},
		{http.MethodDelete, "/api/v1/monitors/1"},
		{http.MethodPost, "/api/v1/monitors/1/pause"},
		{http.MethodPost, "/api/v1/monitors/1/resume"},
		{http.MethodGet, "/api/v1/monitors/1/heartbeats"},
		{http.MethodGet, "/api/v1/monitors/1/incidents"},
		{http.MethodGet, "/api/v1/incidents"},
		{http.MethodPost, "/api/v1/incidents/1/ack"},
		{http.MethodGet, "/api/v1/tokens"},
		{http.MethodPost, "/api/v1/tokens"},
		{http.MethodDelete, "/api/v1/tokens/1"},
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodDelete, "/api/v1/users/1"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/password"},
	}

	for _, tc := range protected {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 — this route is reachable without credentials", rec.Code)
			}
		})
	}
}

func TestPublicRoutesStayOpen(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := srv.Handler()

	for _, path := range []string{"/health", "/api/v1/health", "/api/v1/ready", "/api/v1/setup"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s requires authentication but must be public", path)
			}
		})
	}
}

func TestSetupCreatesFirstAdminThenCloses(t *testing.T) {
	db := openEmptyDB(t)
	srv := New(testLogger(), db)
	h := srv.Handler()

	// Before setup the status endpoint must say so, or the UI cannot know
	// which screen to show.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/setup", nil))
	var status map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !status["setup_required"] {
		t.Fatal("setup_required = false on an empty instance")
	}

	body := `{"email":"frank@example.com","password":"correct-horse-battery-staple"}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup", body))

	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("setup did not set a session cookie; the user would have to log in again immediately")
	}

	// A second call must be refused: an open setup endpoint on a live instance
	// is a way in for anyone who finds it.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup",
		`{"email":"attacker@example.com","password":"correct-horse-battery-staple"}`))

	if rec.Code != http.StatusConflict {
		t.Errorf("second setup returned %d, want 409 — setup must close permanently", rec.Code)
	}
}

func TestSetupRejectsWeakPassword(t *testing.T) {
	db := openEmptyDB(t)
	srv := New(testLogger(), db)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup",
		`{"email":"frank@example.com","password":"short"}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a weak password", rec.Code)
	}
}

func TestLoginAndSessionCookie(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleEditor); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie was set")
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("session cookie is not HttpOnly: XSS would become session theft")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}

	// The cookie must now authenticate a request.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	var me userResponse
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.Email != "user@example.com" || me.Role != "editor" {
		t.Errorf("got %+v, want the editor account", me)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	srv, db := testServerWithDB(t)

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"wrong-password-entirely"}`))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// The error for an unknown account and a wrong password must be identical,
// or the endpoint becomes a way to enumerate who has an account here.
func TestLoginDoesNotRevealWhichAccountsExist(t *testing.T) {
	srv, db := testServerWithDB(t)

	if _, err := db.CreateUser(t.Context(), "real@example.com",
		"correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatalf("create user: %v", err)
	}
	h := srv.Handler()

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"real@example.com","password":"wrong-password-entirely"}`))

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"ghost@example.com","password":"wrong-password-entirely"}`))

	if rec1.Code != rec2.Code {
		t.Errorf("status differs: existing=%d missing=%d", rec1.Code, rec2.Code)
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("body differs:\n existing: %s\n missing:  %s", rec1.Body, rec2.Body)
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv, db := testServerWithDB(t)

	if _, err := db.CreateUser(t.Context(), "target@example.com",
		"correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatalf("create user: %v", err)
	}
	h := srv.Handler()

	body := `{"email":"target@example.com","password":"wrong-password-entirely"}`
	var lastCode int
	for range maxLoginAttempts + 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login", body))
		lastCode = rec.Code
	}

	if lastCode != http.StatusTooManyRequests {
		t.Errorf("after %d failures the status is %d, want 429", maxLoginAttempts+2, lastCode)
	}

	// The limit must survive the correct password too, otherwise it only slows
	// down an attacker who never guesses right.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"target@example.com","password":"correct-horse-battery-staple"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("correct password bypassed the rate limit (status %d)", rec.Code)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the session still works after logout (status %d)", rec.Code)
	}
}

func TestAPITokenAuthenticates(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "bot@example.com",
		"correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), user.ID, "ci", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokedTokenIsRejected(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "bot@example.com",
		"correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, meta, err := db.CreateAPIToken(t.Context(), user.ID, "ci", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := db.RevokeAPIToken(t.Context(), meta.ID, user.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token still works (status %d)", rec.Code)
	}
}

func TestGarbageTokenIsRejected(t *testing.T) {
	srv, _ := testServerWithDB(t)

	for _, header := range []string{
		"Bearer nonsense",
		"Bearer ",
		"nonsense",
		"Basic dXNlcjpwYXNz",
		"Bearer sgp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		t.Run(header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// A viewer must be able to look but not touch. Without this, the role column
// is decoration.
func TestViewerCannotWrite(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "viewer@example.com",
		"correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), user.ID, "readonly", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	h := srv.Handler()

	// Reading is fine.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer cannot read monitors (status %d)", rec.Code)
	}

	// Writing is not.
	writes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/monitors", `{"name":"x","type":"http","target":"https://a.com"}`},
		{http.MethodDelete, "/api/v1/monitors/1", ""},
		{http.MethodPost, "/api/v1/monitors/1/pause", ""},

		// Issuing a token is a write. A viewer's token could only read, but
		// minting one hands out a long-lived credential that outlives the
		// session and leaves the browser — a broader act than reading in the
		// UI, and the least-trusted role is where that distinction matters.
		{http.MethodPost, "/api/v1/tokens", `{"name":"ci"}`},
	}
	for _, tc := range writes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := jsonRequest(tc.method, tc.path, tc.body)
			r.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 — a viewer must not be able to write", rec.Code)
			}
		})
	}
}

// A viewer that cannot mint a token must still be able to see and shred the
// ones it already has. Locking issuance without leaving revocation open would
// strand a viewer with a live key it cannot kill.
func TestViewerCanStillListAndRevokeOwnTokens(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "viewer@example.com",
		"correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, meta, err := db.CreateAPIToken(t.Context(), user.ID, "readonly", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list own tokens: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/tokens/%d", meta.ID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("revoke own token: status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEditorCannotAdminister(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "editor@example.com",
		"correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), user.ID, "editor", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — an editor must not list users", rec.Code)
	}
}

// A cookie-authenticated write from another site must be refused, or every
// logged-in user is one malicious link away from having their monitors deleted.
func TestCSRFBlocksCrossSiteWrite(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))
	cookie := rec.Result().Cookies()[0]

	t.Run("cross-site is blocked", func(t *testing.T) {
		req := jsonRequest(http.MethodPost, "/api/v1/monitors",
			`{"name":"evil","type":"http","target":"https://evil.com"}`)
		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "cross-site")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 — a cross-site write was accepted", rec.Code)
		}
	})

	t.Run("same-origin is allowed", func(t *testing.T) {
		req := jsonRequest(http.MethodPost, "/api/v1/monitors",
			`{"name":"fine","type":"http","target":"https://example.com"}`)
		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
	})

	// Reads are safe by definition and must not be blocked, or the dashboard
	// breaks when embedded or linked from elsewhere.
	t.Run("cross-site read is allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "cross-site")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 for a cross-site read", rec.Code)
		}
	})
}

// A bearer token is not attached by a browser on a cross-site request, so it
// needs no CSRF check — and applying one would break every API client.
func TestBearerTokenSkipsCSRF(t *testing.T) {
	srv, db := testServerWithDB(t)

	user, err := db.CreateUser(t.Context(), "bot@example.com",
		"correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), user.ID, "ci", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"from-ci","type":"http","target":"https://example.com"}`)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordChangeInvalidatesOtherSessions(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}

	login := func() *http.Cookie {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
			`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("login failed: %d %s", rec.Code, rec.Body)
		}
		return rec.Result().Cookies()[0]
	}

	otherDevice := login()
	thisDevice := login()

	req := jsonRequest(http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"correct-horse-battery-staple","new_password":"a-completely-new-passphrase"}`)
	req.AddCookie(thisDevice)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// This is the point of changing a password after a suspected compromise.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(otherDevice)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the other device is still signed in after a password change (status %d)", rec.Code)
	}
}

func TestPasswordChangeRequiresCurrentPassword(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if _, err := db.CreateUser(t.Context(), "user@example.com",
		"correct-horse-battery-staple", store.RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"correct-horse-battery-staple"}`))
	cookie := rec.Result().Cookies()[0]

	req := jsonRequest(http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"not-the-right-one","new_password":"a-completely-new-passphrase"}`)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — an unattended browser must not be a takeover", rec.Code)
	}
}

func TestTokenLifecycle(t *testing.T) {
	srv, _ := testServerWithDB(t)
	h := authedHandler(srv)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/tokens", `{"name":"deploy"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var created struct {
		Token   string           `json:"token"`
		Warning string           `json:"warning"`
		Details apiTokenResponse `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(created.Token, "sgp_") {
		t.Errorf("token %q lacks the sgp_ prefix that secret scanners look for", created.Token)
	}
	if created.Warning == "" {
		t.Error("no warning that the token is shown only once")
	}

	// Listing must never return the plaintext again.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil))
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Error("the token list leaks the plaintext token")
	}

	// The new token works.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+created.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the new token does not authenticate (status %d)", rec.Code)
	}

	// And stops working once revoked.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodDelete,
		"/api/v1/tokens/"+itoa(created.Details.ID), ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+created.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the revoked token still authenticates (status %d)", rec.Code)
	}
}

// Losing every administrator locks everyone out of the instance permanently.
func TestCannotDeleteLastAdmin(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := authedHandler(srv)

	users, err := db.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var adminID int64
	for _, u := range users {
		if u.Role == store.RoleAdmin {
			adminID = u.ID
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodDelete, "/api/v1/users/"+itoa(adminID), ""))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — deleting the last admin must be refused", rec.Code)
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for h, v := range want {
		if got := rec.Header().Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("no Content-Security-Policy header")
	}
}
