package api

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The session cookie is Secure, and named with the __Host- prefix, whenever
// the browser's page is HTTPS. Behind a TLS-terminating proxy that sends no
// X-Forwarded-Proto (nginx's default), the page's Origin is the only thing
// in the request that says so; without it, an HTTPS dashboard was handed a
// cookie the browser also sends over plain HTTP.

const sessionHost = "status.example.com"

// pageSchemes are the ways a request can arrive, and whether each one is
// from an HTTPS page.
var pageSchemes = map[string]struct {
	arrive func(*http.Request)
	secure bool
}{
	"plain HTTP, no Origin (curl)": {func(*http.Request) {}, false},
	"plain HTTP page":              {func(r *http.Request) { r.Header.Set("Origin", "http://"+sessionHost) }, false},
	"opaque Origin":                {func(r *http.Request) { r.Header.Set("Origin", "null") }, false},
	"TLS":                          {func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, true},
	"X-Forwarded-Proto https":      {func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, true},
	"X-Forwarded-Proto HTTPS":      {func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "HTTPS") }, true},
	"HTTPS page behind a proxy that sends no X-Forwarded-Proto": {
		func(r *http.Request) { r.Header.Set("Origin", "https://"+sessionHost) }, true,
	},
}

// sessionCookieOf returns the cookie in rec that carries a session, failing
// when there is not exactly one.
func sessionCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Value == "" {
			continue
		}
		if found != nil {
			t.Fatalf("two session cookies set: %s and %s", found.Name, c.Name)
		}
		found = c
	}
	if found == nil {
		t.Fatalf("no session cookie set (status %d)", rec.Code)
	}
	return found
}

// expired reports the cookie names rec tells the browser to drop.
func expired(rec *httptest.ResponseRecorder) map[string]bool {
	out := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			out[c.Name] = true
		}
	}
	return out
}

func checkSessionCookie(t *testing.T, rec *httptest.ResponseRecorder, wantSecure bool) {
	t.Helper()
	c := sessionCookieOf(t, rec)
	wantName := insecureSessionCookieName
	if wantSecure {
		wantName = sessionCookieName
	}
	if c.Name != wantName || c.Secure != wantSecure {
		t.Errorf("cookie %s Secure=%v, want %s Secure=%v", c.Name, c.Secure, wantName, wantSecure)
	}
	// Over HTTPS the plain cookie a browser may hold from before is dropped
	// in the same response; over HTTP nothing else is touched.
	gone := expired(rec)
	if wantSecure != gone[insecureSessionCookieName] {
		t.Errorf("plain cookie expired = %v, want %v", gone[insecureSessionCookieName], wantSecure)
	}
	if gone[sessionCookieName] {
		t.Error("the response expired the cookie it issued")
	}
}

func TestSignInCookieIsSecureOnAnHTTPSPage(t *testing.T) {
	for name, tc := range pageSchemes {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			if _, err := db.CreateUser(t.Context(), "user@example.com", strongPassword, store.RoleEditor); err != nil {
				t.Fatalf("create user: %v", err)
			}
			req := jsonRequest(http.MethodPost, "/api/v1/auth/login",
				`{"email":"user@example.com","password":"`+strongPassword+`"}`)
			req.Host = sessionHost
			tc.arrive(req)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
			}
			checkSessionCookie(t, rec, tc.secure)
		})
	}
}

func TestSetupCookieIsSecureOnAnHTTPSPage(t *testing.T) {
	for name, tc := range pageSchemes {
		t.Run(name, func(t *testing.T) {
			srv := New(testLogger(), openEmptyDB(t))
			req := jsonRequest(http.MethodPost, "/api/v1/setup",
				`{"email":"frank@example.com","password":"`+strongPassword+`"}`)
			req.Host = sessionHost
			tc.arrive(req)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
				t.Fatalf("setup status = %d: %s", rec.Code, rec.Body.String())
			}
			checkSessionCookie(t, rec, tc.secure)
		})
	}
}

func TestPasswordChangeCookieIsSecureOnAnHTTPSPage(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", strongPassword, store.RoleEditor); err != nil {
		t.Fatalf("create user: %v", err)
	}
	login := jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"`+strongPassword+`"}`)
	login.Host = sessionHost
	login.Header.Set("Origin", "https://"+sessionHost)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, login)
	cookie := sessionCookieOf(t, rec)

	req := jsonRequest(http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"`+strongPassword+`","new_password":"another-long-passphrase-42"}`)
	req.Host = sessionHost
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://"+sessionHost)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d: %s", rec.Code, rec.Body.String())
	}
	checkSessionCookie(t, rec, true)
}

// Signing out over HTTPS drops both names: a browser that signed in before
// its page's scheme was recognised holds the plain one.
func TestSignOutExpiresBothCookieNamesOverHTTPS(t *testing.T) {
	for name, tc := range map[string]struct {
		arrive func(*http.Request)
		want   []string
	}{
		"HTTPS page": {
			func(r *http.Request) { r.Header.Set("Origin", "https://"+sessionHost) },
			[]string{sessionCookieName, insecureSessionCookieName},
		},
		"plain HTTP page": {
			func(r *http.Request) { r.Header.Set("Origin", "http://"+sessionHost) },
			[]string{insecureSessionCookieName},
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			h := srv.Handler()
			if _, err := db.CreateUser(t.Context(), "user@example.com", strongPassword, store.RoleEditor); err != nil {
				t.Fatalf("create user: %v", err)
			}
			// The pre-fix cookie: issued over what looked like plain HTTP.
			login := jsonRequest(http.MethodPost, "/api/v1/auth/login",
				`{"email":"user@example.com","password":"`+strongPassword+`"}`)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, login)
			old := sessionCookieOf(t, rec)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			req.Host = sessionHost
			req.AddCookie(old)
			tc.arrive(req)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("logout status = %d: %s", rec.Code, rec.Body.String())
			}
			gone := expired(rec)
			if len(gone) != len(tc.want) {
				t.Errorf("expired %v, want %v", gone, tc.want)
			}
			for _, n := range tc.want {
				if !gone[n] {
					t.Errorf("logout left %s in the browser (expired %v)", n, gone)
				}
			}
			for _, c := range rec.Result().Cookies() {
				if c.Name == sessionCookieName && !c.Secure {
					t.Error("a __Host- cookie expired without Secure is refused by the browser")
				}
			}
			if stillSignedIn(h, old) {
				t.Error("the session still authenticates after signing out")
			}
		})
	}
}

// A push URL built from the request takes its scheme from the same rule, so
// a monitor created from an HTTPS dashboard behind such a proxy does not
// hand its job an http:// address.
func TestPushURLSchemeFollowsTheHTTPSPage(t *testing.T) {
	for name, tc := range pageSchemes {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			req := jsonRequest(http.MethodPost, "/api/v1/monitors", pushMonitorBody)
			req.Host = sessionHost
			tc.arrive(req)
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
			}
			var got monitorResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			want := "http://"
			if tc.secure {
				want = "https://"
			}
			if !strings.HasPrefix(got.PushURL, want+sessionHost+pushPath) {
				t.Errorf("push_url = %q, want it to start %s%s%s", got.PushURL, want, sessionHost, pushPath)
			}
		})
	}
}
