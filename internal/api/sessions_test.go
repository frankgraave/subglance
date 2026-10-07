package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/store"
)

// signIn logs an account in with the given User-Agent and returns its cookie.
func signInFrom(t *testing.T, h http.Handler, email, userAgent string) *http.Cookie {
	t.Helper()
	req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"correct-horse-battery-staple"}`)
	req.Header.Set("User-Agent", userAgent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body)
	}
	return rec.Result().Cookies()[0]
}

// withCookie sends a same-origin request with a session cookie, the way the
// UI does.
func withCookie(h http.Handler, cookie *http.Cookie, method, path string) *httptest.ResponseRecorder {
	req := jsonRequest(method, path, "")
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func listSessionsWith(t *testing.T, h http.Handler, cookie *http.Cookie, path string) []sessionResponse {
	t.Helper()
	rec := withCookie(h, cookie, http.MethodGet, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	var body struct {
		Sessions []sessionResponse `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Sessions
}

func stillSignedIn(h http.Handler, cookie *http.Cookie) bool {
	return withCookie(h, cookie, http.MethodGet, "/api/v1/auth/me").Code == http.StatusOK
}

const (
	firefoxMac   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:131.0) Gecko/20100101 Firefox/131.0"
	safariIPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

func TestListOwnSessions(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	laptop := signInFrom(t, h, "user@example.com", firefoxMac)
	phone := signInFrom(t, h, "user@example.com", safariIPhone)
	// Someone else's session must not appear.
	signInFrom(t, h, "admin@example.com", firefoxMac)

	rec := withCookie(h, phone, http.MethodGet, "/api/v1/sessions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
	// Neither credential may be in the listing, in any form.
	for _, secret := range []string{laptop.Value, phone.Value, auth.HashToken(laptop.Value), auth.HashToken(phone.Value)} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("the session listing contains a session token or its hash")
		}
	}

	sessions := listSessionsWith(t, h, phone, "/api/v1/sessions")
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want the account's 2: %+v", len(sessions), sessions)
	}
	current := 0
	for _, s := range sessions {
		if s.Current {
			current++
			if s.Browser != "Safari" || s.Platform != "iPhone" {
				t.Errorf("current session = %s on %s, want Safari on iPhone", s.Browser, s.Platform)
			}
		}
		if s.IP == "" || s.UserAgent == "" || s.LastSeenAt.IsZero() || s.CreatedAt.IsZero() {
			t.Errorf("session missing what identifies it: %+v", s)
		}
		if !store.ValidSessionID(s.ID) {
			t.Errorf("id %q is not a session id", s.ID)
		}
	}
	if current != 1 {
		t.Errorf("%d sessions marked current, want exactly 1", current)
	}
}

func TestEndOwnSessionWorksOnTheNextRequest(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	lost := signInFrom(t, h, "user@example.com", firefoxMac)
	here := signInFrom(t, h, "user@example.com", safariIPhone)

	var lostID string
	for _, s := range listSessionsWith(t, h, here, "/api/v1/sessions") {
		if !s.Current {
			lostID = s.ID
		}
	}
	rec := withCookie(h, here, http.MethodDelete, "/api/v1/sessions/"+lostID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("ending another session cleared this browser's cookie")
	}
	if stillSignedIn(h, lost) {
		t.Error("the ended session still authenticates")
	}
	if !stillSignedIn(h, here) {
		t.Error("ending another session signed this one out")
	}
	if rec := withCookie(h, here, http.MethodDelete, "/api/v1/sessions/"+lostID); rec.Code != http.StatusNotFound {
		t.Errorf("ending it again: %d, want 404", rec.Code)
	}
}

func TestEndingTheCurrentSessionSignsOut(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	here := signInFrom(t, h, "user@example.com", firefoxMac)
	sessions := listSessionsWith(t, h, here, "/api/v1/sessions")

	rec := withCookie(h, here, http.MethodDelete, "/api/v1/sessions/"+sessions[0].ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == here.Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("ending the current session left its cookie in the browser")
	}
	if stillSignedIn(h, here) {
		t.Error("the current session still authenticates after ending it")
	}
}

// Knowing another account's session id must not be enough to end it, and the
// answer must not confirm that the id exists.
func TestCannotEndAnotherAccountsSession(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	for _, email := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := db.CreateUser(t.Context(), email, "correct-horse-battery-staple", store.RoleEditor); err != nil {
			t.Fatal(err)
		}
	}
	alice := signInFrom(t, h, "alice@example.com", firefoxMac)
	bob := signInFrom(t, h, "bob@example.com", firefoxMac)
	bobID := listSessionsWith(t, h, bob, "/api/v1/sessions")[0].ID

	rec := withCookie(h, alice, http.MethodDelete, "/api/v1/sessions/"+bobID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE another account's session: %d, want 404", rec.Code)
	}
	unknown := withCookie(h, alice, http.MethodDelete, "/api/v1/sessions/"+strings.Repeat("0", 32))
	if unknown.Body.String() != rec.Body.String() {
		t.Errorf("another account's id answers %q, an unknown one %q: the difference confirms it exists", rec.Body, unknown.Body)
	}
	if !stillSignedIn(h, bob) {
		t.Error("bob was signed out through alice's request")
	}
	if rec := withCookie(h, alice, http.MethodDelete, "/api/v1/sessions/not-an-id"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id: %d, want 400", rec.Code)
	}
}

func TestEndOtherSessionsKeepsThisBrowser(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	others := []*http.Cookie{signInFrom(t, h, "user@example.com", firefoxMac), signInFrom(t, h, "user@example.com", "curl/8.9")}
	here := signInFrom(t, h, "user@example.com", safariIPhone)

	rec := withCookie(h, here, http.MethodPost, "/api/v1/sessions/end-others")
	if rec.Code != http.StatusOK {
		t.Fatalf("end-others: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Ended int `json:"ended"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Ended != 2 {
		t.Errorf("ended = %d (%v), want 2", body.Ended, err)
	}
	if !stillSignedIn(h, here) {
		t.Fatal("signing out everywhere else signed this browser out")
	}
	for i, c := range others {
		if stillSignedIn(h, c) {
			t.Errorf("other session %d still authenticates", i)
		}
	}
}

func TestAdminSeesAndEndsAnotherAccountsSessions(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	user, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	userCookies := []*http.Cookie{signInFrom(t, h, "user@example.com", firefoxMac), signInFrom(t, h, "user@example.com", safariIPhone)}
	admin := signInFrom(t, h, "admin@example.com", firefoxMac)
	path := "/api/v1/users/" + itoa(user.ID) + "/sessions"

	sessions := listSessionsWith(t, h, admin, path)
	if len(sessions) != 2 {
		t.Fatalf("admin sees %d of the account's sessions, want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.IP == "" {
			t.Error("an administrator is not shown the session's IP")
		}
		if s.Current {
			t.Error("another account's session is marked as the admin's current one")
		}
	}

	rec := withCookie(h, admin, http.MethodDelete, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE %s: %d %s", path, rec.Code, rec.Body)
	}
	for i, c := range userCookies {
		if stillSignedIn(h, c) {
			t.Errorf("session %d of the account survived", i)
		}
	}
	if !stillSignedIn(h, admin) {
		t.Error("ending an account's sessions signed the administrator out")
	}
	// The account and its tokens stay: this is a sign-out, not a removal.
	if _, err := db.GetUser(t.Context(), user.ID); err != nil {
		t.Errorf("the account is gone: %v", err)
	}

	if rec := withCookie(h, admin, http.MethodGet, "/api/v1/users/99999/sessions"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown account: %d, want 404", rec.Code)
	}
	if rec := withCookie(h, admin, http.MethodDelete, "/api/v1/users/99999/sessions"); rec.Code != http.StatusNotFound {
		t.Errorf("ending an unknown account's sessions: %d, want 404", rec.Code)
	}
	if rec := withCookie(h, admin, http.MethodGet, "/api/v1/users/x/sessions"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed account id: %d, want 400", rec.Code)
	}
}

func TestOnlyAnAdminSeesAnotherAccountsSessions(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	target, err := db.CreateUser(t.Context(), "target@example.com", "correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	targetCookie := signInFrom(t, h, "target@example.com", firefoxMac)
	path := "/api/v1/users/" + itoa(target.ID) + "/sessions"

	for _, role := range []store.Role{store.RoleEditor, store.RoleViewer} {
		email := string(role) + "@example.com"
		if _, err := db.CreateUser(t.Context(), email, "correct-horse-battery-staple", role); err != nil {
			t.Fatal(err)
		}
		cookie := signInFrom(t, h, email, firefoxMac)
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			if rec := withCookie(h, cookie, method, path); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d, want 403", method, path, role, rec.Code)
			}
		}
	}
	if !stillSignedIn(h, targetCookie) {
		t.Error("a non-administrator ended another account's sessions")
	}
}

// A request made with an API token holds no session, so none is current and
// "the others" are all of them.
func TestSessionsThroughAnAPIToken(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	user, err := db.CreateUser(t.Context(), "user@example.com", "correct-horse-battery-staple", store.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := db.CreateAPIToken(t.Context(), user.ID, "script", nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := signInFrom(t, h, "user@example.com", firefoxMac)

	req := jsonRequest(http.MethodGet, "/api/v1/sessions", "")
	req.Header.Set("Authorization", "Bearer "+token)
	// A cookie beside the bearer token did not authenticate this request.
	req.AddCookie(browser)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct {
		Sessions []sessionResponse `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Sessions) != 1 {
		t.Fatalf("list via token: %d %s", rec.Code, rec.Body)
	}
	if body.Sessions[0].Current {
		t.Error("a session is marked current on a request that used an API token")
	}

	req = jsonRequest(http.MethodPost, "/api/v1/sessions/end-others", "")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("end-others via token: %d %s", rec.Code, rec.Body)
	}
	if stillSignedIn(h, browser) {
		t.Error("the browser session survived end-others from a token")
	}
}

func TestDescribeUserAgent(t *testing.T) {
	for _, tc := range []struct{ ua, browser, platform string }{
		{firefoxMac, "Firefox", "macOS"},
		{safariIPhone, "Safari", "iPhone"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", "Chrome", "Windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0", "Edge", "Windows"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36", "Chrome", "Android"},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 OPR/114.0.0.0", "Opera", "Linux"},
		{"Mozilla/5.0 (iPad; CPU OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/129.0 Mobile/15E148 Safari/604.1", "Chrome", "iPad"},
		{"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", "Chrome", "ChromeOS"},
		{"curl/8.9.1", "curl", ""},
		{"", "", ""},
		{"something-else/1.0", "", ""},
	} {
		browser, platform := describeUserAgent(tc.ua)
		if browser != tc.browser || platform != tc.platform {
			t.Errorf("describeUserAgent(%q) = %q, %q; want %q, %q", tc.ua, browser, platform, tc.browser, tc.platform)
		}
	}
}
