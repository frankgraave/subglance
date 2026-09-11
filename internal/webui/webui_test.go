package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// themeScript stands in for the pre-paint theme script in web/index.html: an
// inline script that cannot move into the bundle because it has to run before
// the first paint.
const themeScript = `document.documentElement.setAttribute("data-theme","dark");`

const indexHTML = `<!doctype html>
<html lang="en"><head>
<script>` + themeScript + `</script>
<script type="module" src="/assets/index-abc123.js"></script>
</head><body><div id="root"></div></body></html>`

// builtFS is what an embedded frontend looks like after a real build.
func builtFS() fs.FS {
	return fstest.MapFS{
		"index.html":              {Data: []byte(indexHTML)},
		"assets/index-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc123.css": {Data: []byte(":root{}")},
		"favicon.svg":             {Data: []byte("<svg/>")},
		"nested/page/index.html":  {Data: []byte("nested")},
	}
}

// apiNotFound is the JSON 404 the API hands to the UI handler.
func apiNotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such endpoint"}`))
	})
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// TestUnknownPagePathServesShell is the single-page contract: a URL the user
// bookmarked or reloaded is a client-side route, not a missing file.
func TestUnknownPagePathServesShell(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, target := range []string{"/", "/monitors", "/monitors/42/incidents", "/settings?tab=users"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", target, rec.Code)
			continue
		}
		if got := rec.Body.String(); !strings.Contains(got, `id="root"`) {
			t.Errorf("GET %s did not serve the SPA shell, got %q", target, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type = %q, want text/html", target, got)
		}
	}
}

// TestAPIPathsFallThroughAsJSON is the other half of that contract, and the
// reason the handler cannot simply serve index.html for everything: a JSON
// client that receives HTML fails far away from the typo that caused it.
func TestAPIPathsFallThroughAsJSON(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, target := range []string{"/api", "/api/", "/api/v1/nope", "/api/v2/monitors"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("GET %s: Content-Type = %q, want application/json", target, got)
		}
	}
}

// TestAPILookalikePathsAreStillPages guards the boundary from the other side:
// "/apiary" is not under the API, and a prefix test that forgot the slash
// would wrongly 404 it.
func TestAPILookalikePathsAreStillPages(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, target := range []string{"/apiary", "/api-docs", "/apis"} {
		rec := get(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want the SPA shell (200)", target, rec.Code)
		}
	}
}

// TestHashedAssetsAreCachedForever and its sibling below encode the only cache
// policy that is safe for a hashed bundle: the content-addressed files may be
// kept forever, but the document naming them may never be, or a browser will
// happily pin itself to a build the server no longer has.
func TestHashedAssetsAreCachedForever(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	rec := get(t, h, http.MethodGet, "/assets/index-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != immutableCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, immutableCacheControl)
	}
	if got := rec.Body.String(); got != "console.log(1)" {
		t.Errorf("body = %q, want the asset itself", got)
	}
}

func TestShellAndUnhashedFilesAreNeverCached(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, target := range []string{"/", "/favicon.svg"} {
		rec := get(t, h, http.MethodGet, target)
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control = %q, want no-cache", target, got)
		}
	}
}

// TestMissingAssetDoesNotServeHTML is the trap a naive SPA fallback walks into:
// a renamed bundle would answer 200 with an HTML document, the browser would
// try to execute it as a module, and the console error would point at the
// bundle rather than at the stale document that asked for it.
func TestMissingAssetDoesNotServeHTML(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	rec := get(t, h, http.MethodGet, "/assets/index-gone.js")
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `id="root"`) {
		t.Error("a missing hashed asset was answered with the SPA shell")
	}
}

// TestUnbuiltBinaryExplainsItself covers `go build` without Node: the binary
// must still start and still serve the API, and the UI must say why it is not
// there rather than returning a blank page.
func TestUnbuiltBinaryExplainsItself(t *testing.T) {
	h := handlerFor(fstest.MapFS{}, apiNotFound())

	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "web-build") {
		t.Errorf("the unbuilt page does not say how to build the dashboard: %q", body)
	}

	// The API must keep working in that build.
	if rec := get(t, h, http.MethodGet, "/api/v1/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("API path in an unbuilt binary: status = %d, want 404", rec.Code)
	}
}

// TestHeadRequestSendsNoBody: HEAD is registered alongside GET, and a HEAD
// that ships a body is a protocol violation clients cache wrongly.
func TestHeadRequestSendsNoBody(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	rec := get(t, h, http.MethodHead, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD / returned %d bytes of body", rec.Body.Len())
	}
}

// TestCSPAllowsTheInlineScriptByHash is the security claim this package makes:
// the pre-paint theme script runs because its exact bytes are hashed into the
// policy, not because inline script is allowed in general. If a future edit
// reaches for 'unsafe-inline' to make the page work, this fails.
func TestCSPAllowsTheInlineScriptByHash(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	csp := get(t, h, http.MethodGet, "/").Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy on the shell")
	}
	if strings.Contains(csp, "unsafe-inline") && strings.Contains(csp, "script-src") {
		// style-src may carry it; script-src may not.
		for _, dir := range strings.Split(csp, ";") {
			dir = strings.TrimSpace(dir)
			if strings.HasPrefix(dir, "script-src") && strings.Contains(dir, "unsafe-inline") {
				t.Error("script-src allows 'unsafe-inline'; the whole point of the hash is that it does not")
			}
		}
	}

	sum := sha256.Sum256([]byte(themeScript))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(csp, want) {
		t.Errorf("CSP does not allow the inline theme script by hash.\n got: %s\nwant it to contain: %s", csp, want)
	}
}

// TestCSPDoesNotHashExternalScripts: the module tag has a src, so its contents
// are empty and hashing it would put a meaningless hash in the header.
func TestCSPDoesNotHashExternalScripts(t *testing.T) {
	csp := contentSecurityPolicy([]byte(indexHTML))

	if n := strings.Count(csp, "'sha256-"); n != 1 {
		t.Errorf("CSP contains %d script hashes, want exactly 1 (the inline theme script): %s", n, csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP dropped frame-ancestors: %s", csp)
	}
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP must allow the dashboard to reach its own API and stream: %s", csp)
	}
}

// TestDirectoryRequestServesShell: a request for a directory must not produce
// a file listing, and must not 500 either.
func TestDirectoryRequestServesShell(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	rec := get(t, h, http.MethodGet, "/assets")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `id="root"`) {
		t.Errorf("a directory path did not serve the shell: %q", body)
	}
}

// TestTraversalCannotEscapeTheBundle: path.Clean collapses "..", so a crafted
// path can only ever name something inside the embedded file system.
func TestTraversalCannotEscapeTheBundle(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, target := range []string{"/../go.mod", "/assets/../../go.mod", "/%2e%2e/go.mod"} {
		rec := get(t, h, http.MethodGet, target)
		if strings.Contains(rec.Body.String(), "module github.com") {
			t.Errorf("GET %s escaped the bundle", target)
		}
	}
}

// TestNonReadMethodsOnPagesAreRejected: this handler is the catch-all, so a
// POST to a GET-only endpoint lands here. Serving the SPA shell with a 200
// would tell the caller their write succeeded.
func TestNonReadMethodsOnPagesAreRejected(t *testing.T) {
	h := handlerFor(builtFS(), apiNotFound())

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := get(t, h, method, "/monitors")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /monitors: status = %d, want 405", method, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s /monitors: Allow = %q, want \"GET, HEAD\"", method, got)
		}
	}
}
