package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestWrongMethodOnAKnownAPIPathIs405: a path the API serves, reached with a
// verb it does not serve, is a 405 naming the verbs it does — not the 404 "no
// such endpoint" that sends a client looking for a typo in a correct path.
func TestWrongMethodOnAKnownAPIPathIs405(t *testing.T) {
	srv := testServer()

	cases := []struct {
		method, path, allow string
	}{
		{http.MethodDelete, "/api/v1/monitors", "GET, HEAD, POST"},
		{http.MethodPut, "/api/v1/monitors", "GET, HEAD, POST"},
		{http.MethodOptions, "/api/v1/monitors", "GET, HEAD, POST"},
		{http.MethodPatch, "/api/v1/channels", "GET, HEAD, POST"},
		// An item path, matched through its {id} wildcard.
		{http.MethodPut, "/api/v1/monitors/7/check", "POST"},
		{http.MethodGet, "/api/v1/maintenance/3", "DELETE"},
		// HEAD is a GET to net/http, so a POST-only path refuses it too.
		// (Not /monitors/preview: GET /monitors/{id} matches that path, so
		// a HEAD there is a monitor lookup, not a wrong verb.)
		{http.MethodHead, "/api/v1/auth/login", "POST"},
		// Two wildcards.
		{http.MethodPost, "/api/v1/routing-rules/4/exclusions/9", "DELETE, PUT"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", c.method, c.path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("%s %s: Content-Type = %q, want JSON", c.method, c.path, got)
		}
		if c.method == http.MethodHead {
			continue
		}
		var body struct {
			Error string `json:"error"`
			Field string `json:"field"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body is not the API's error object: %v (%q)", c.method, c.path, err, rec.Body)
		}
		want := c.method + " is not allowed here; this endpoint takes " + c.allow
		if body.Error != want || body.Field != "" {
			t.Errorf("%s %s: error = %q field %q, want %q and no field", c.method, c.path, body.Error, body.Field, want)
		}
	}
}

// TestUnknownAPIPathKeepsItsJSON404: the 405 is for a path the table serves.
// A path it does not serve is still unknown, whatever the method, and carries
// no Allow header — there is nothing to allow.
func TestUnknownAPIPathKeepsItsJSON404(t *testing.T) {
	srv := testServer()

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/nope"},
		{http.MethodDelete, "/api/v1/nope"},
		// A trailing slash is a different path, not a different verb.
		{http.MethodGet, "/api/v1/monitors/"},
		{http.MethodDelete, "/api/v1/monitors/1/"},
		{http.MethodPut, "/api/v2/monitors"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", c.method, c.path, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "" {
			t.Errorf("%s %s: Allow = %q on an unknown path", c.method, c.path, got)
		}
		if !strings.Contains(rec.Body.String(), "no such endpoint") {
			t.Errorf("%s %s: body = %q, want the API's not-found error", c.method, c.path, rec.Body)
		}
	}
}

// TestWrongMethodOutsideTheAPINamesTheRealMethods: the paths outside /api
// that the table serves — a probe, a status page, a mute link — answer a
// wrong verb with a plain-text 405 like the dashboard's pages, but with the
// verbs that path takes. The mute link takes a POST, which the dashboard's
// "GET, HEAD" used to deny.
func TestWrongMethodOutsideTheAPINamesTheRealMethods(t *testing.T) {
	srv := testServer()

	for _, c := range []struct{ method, path, allow string }{
		{http.MethodPost, "/health", "GET, HEAD"},
		{http.MethodDelete, "/status/main", "GET, HEAD"},
		{http.MethodPut, "/mute/abc", "GET, HEAD, POST"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", c.method, c.path, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
		if got := rec.Header().Get("Content-Type"); strings.HasPrefix(got, "application/json") {
			t.Errorf("%s %s: Content-Type = %q; outside /api the refusal is not JSON", c.method, c.path, got)
		}
	}
}

// TestEveryRouteIsInTheAllowIndex walks the route table itself, so a route
// added tomorrow is held to the same answer without anyone extending a list:
// a verb no route uses, sent to any route's path, is a 405 whose Allow names
// that route's own method.
func TestEveryRouteIsInTheAllowIndex(t *testing.T) {
	srv := testServer()
	h := srv.Handler()

	for _, rt := range srv.routes() {
		if rt.Method == anyMethod {
			continue
		}
		path := concretePath(rt.Pattern)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("PROPFIND", path, nil))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("PROPFIND %s (route %s %s): status = %d, want 405", path, rt.Method, rt.Pattern, rec.Code)
			continue
		}
		allow := strings.Split(rec.Header().Get("Allow"), ", ")
		if !slices.Contains(allow, rt.Method) {
			t.Errorf("PROPFIND %s: Allow = %q, missing the route's own %s", path, allow, rt.Method)
		}
	}
}

// concretePath fills every wildcard of a route pattern with "1" and drops the
// end-of-path marker, giving a path the pattern matches.
func concretePath(pattern string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(pattern, '{')
		if i < 0 {
			b.WriteString(pattern)
			return b.String()
		}
		b.WriteString(pattern[:i])
		j := strings.IndexByte(pattern[i:], '}')
		if pattern[i:i+j+1] != "{$}" {
			b.WriteString("1")
		}
		pattern = pattern[i+j+1:]
	}
}
