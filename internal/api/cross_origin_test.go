package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// signInEditor creates an editor and returns the session cookie a browser holds.
func signInEditor(t *testing.T, h http.Handler, db *store.DB) *http.Cookie {
	t.Helper()
	if _, err := db.CreateUser(t.Context(), "editor@example.com",
		"correct-horse-battery-staple", store.RoleEditor); err != nil {
		t.Fatalf("create user: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"editor@example.com","password":"correct-horse-battery-staple"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()[0]
}

// proxiedWrite is the dashboard creating a monitor over plain HTTP through a
// reverse proxy that rewrites Host to its upstream address (nginx without
// proxy_set_header Host). The browser sends no Sec-Fetch-Site to a
// plain-HTTP address that is not loopback, so Origin is all there is.
func proxiedWrite(cookie *http.Cookie, origin string) *http.Request {
	r := jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"api","type":"http","target":"https://example.com"}`)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", origin)
	r.AddCookie(cookie)
	return r
}

// Behind a Host-rewriting proxy, every save from the dashboard used to be
// refused as cross-origin, with the 401 that signs the dashboard out. The
// address --base-url declares is the browser's own, so its origin counts.
func TestWriteFromBaseURLOriginBehindHostRewritingProxy(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithBaseURL("http://monitor.lan")
	h := srv.Handler()
	cookie := signInEditor(t, h, db)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedWrite(cookie, "http://monitor.lan"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a write from the --base-url origin: %s",
			rec.Code, rec.Body.String())
	}

	// The declared origin is the only extra one: another page is still
	// refused, Host-rewriting proxy or not.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedWrite(cookie, "http://evil.example"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a write from a foreign origin", rec.Code)
	}
}

// Without --base-url the write is still refused, but as what it is: a 403
// that names the fix, not the 401 that tells the dashboard the session is
// gone. The session must survive the refusal.
func TestCrossOriginRefusalKeepsTheSession(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	cookie := signInEditor(t, h, db)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedWrite(cookie, "http://monitor.lan"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: a valid session answered 401 reads as signed out", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "--base-url") {
		t.Errorf("refusal does not name --base-url: %s", body)
	}

	me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	me.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, me)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /auth/me after the refusal = %d, want 200", rec.Code)
	}
}

// A --base-url with a path prefix still has the host's origin: the browser
// leaves the path out of Origin.
func TestWriteFromBaseURLOriginWithPathPrefix(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithBaseURL("https://example.com/subglance")
	h := srv.Handler()
	cookie := signInEditor(t, h, db)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedWrite(cookie, "https://example.com"))
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// A refused write from another site is 403 for a bearer-less request and
// changes nothing for a bearer token, which a browser never attaches.
func TestBearerTokenIgnoresForeignOrigin(t *testing.T) {
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

	r := jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"from-ci","type":"http","target":"https://example.com"}`)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestOriginOf(t *testing.T) {
	for base, want := range map[string]string{
		"":                              "",
		"http://monitor.lan":            "http://monitor.lan",
		"https://Status.Example.COM":    "https://status.example.com",
		"https://example.com/subglance": "https://example.com",
		"https://example.com:443":       "https://example.com",
		"http://example.com:80":         "http://example.com",
		"http://example.com:8080":       "http://example.com:8080",
		"https://example.com:80":        "https://example.com:80",
		"http://[2001:DB8::1]:8080":     "http://[2001:db8::1]:8080",
		// IPv6 as a browser serialises it (WHATWG URL; checked against
		// Node's URL): leading zeroes dropped, the first longest zero run
		// cut, a single zero piece kept, IPv4-mapped written in hex.
		"http://[2001:0db8::1]:8080":    "http://[2001:db8::1]:8080",
		"http://[2001:0DB8:0000::0001]": "http://[2001:db8::1]",
		"http://[0:0:0:0:0:0:0:1]":      "http://[::1]",
		"http://[1:0:0:0:0:0:0:0]":      "http://[1::]",
		"http://[2001:db8:0:0:1:0:0:1]": "http://[2001:db8::1:0:0:1]",
		"http://[2001:db8:0:1:1:1:1:1]": "http://[2001:db8:0:1:1:1:1:1]",
		"http://[::ffff:192.0.2.10]":    "http://[::ffff:c000:20a]",
		"https://[2001:db8::]:443":      "https://[2001:db8::]",
		"http://192.0.2.10":             "http://192.0.2.10",
	} {
		if got := originOf(base); got != want {
			t.Errorf("originOf(%q) = %q, want %q", base, got, want)
		}
	}
}
