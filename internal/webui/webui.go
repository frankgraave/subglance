// Package webui embeds the built dashboard and hands it out as a single-page
// application.
//
// SubGlance ships as one binary with no loose parts, so the
// output of the frontend build lives inside the executable rather than next to
// it. The bundler writes straight into dist/ here; see web/vite.config.ts.
//
// The package is deliberately usable without that build having happened: a
// contributor working on the Go side must be able to `go build ./...` on a
// clean checkout with no Node installed. In that case Available reports false
// and the handler returns an honest explanation instead of a blank page.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

// dist holds the built frontend.
//
// The `all:` prefix matters twice over: it makes the committed dist/.gitkeep
// visible to embed, which is what keeps this directive compiling when the
// frontend has never been built, and it stops embed from silently dropping any
// future asset whose name begins with a dot or an underscore.
//
//go:embed all:dist
var dist embed.FS

// indexPath is the SPA shell, relative to the embedded root.
const indexPath = "index.html"

// assetPrefix is the directory the bundler writes content-hashed files into. A
// file under it carries its own content in its name, so it can be cached
// forever; anything outside it cannot.
const assetPrefix = "assets/"

// immutableCacheControl is the standard "this URL can never mean anything
// else" answer: one year, and no revalidation even on a forced reload.
const immutableCacheControl = "public, max-age=31536000, immutable"

// FS returns the embedded build output rooted at the bundle itself.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only reachable if the embed directive above stops matching, which
		// is a compile-time fact, not a runtime one.
		panic("webui: " + err.Error())
	}
	return sub
}

// Available reports whether a real frontend build is embedded.
//
// False means the binary was built without building the frontend first. That
// is a supported state for backend work, so callers should degrade rather than
// refuse to start.
func Available() bool {
	_, err := fs.Stat(FS(), indexPath)
	return err == nil
}

// shell is the SPA entry document: the bytes to send and the
// Content-Security-Policy that fits them. A nil body means no frontend was
// built into this binary.
type shell struct {
	body []byte
	csp  string
}

// loadShell reads index.html out of files once.
func loadShell(files fs.FS) func() shell {
	return sync.OnceValue(func() shell {
		b, err := fs.ReadFile(files, indexPath)
		if err != nil {
			return shell{}
		}
		return shell{body: b, csp: contentSecurityPolicy(b)}
	})
}

// scriptElement matches a <script> element, capturing its opening tag and its
// body. index.html carries exactly one inline script: the pre-paint theme
// script, which must run before React mounts and therefore cannot move into
// the bundle.
var scriptElement = regexp.MustCompile(`(?is)(<script(?:\s[^>]*)?>)(.*?)</script>`)

// contentSecurityPolicy builds a policy for the shell.
//
// Inline scripts are allowed by hash rather than by 'unsafe-inline'. The
// difference is the whole value of the header: 'unsafe-inline' would permit
// any script an injection manages to place in the page, while a hash permits
// exactly the bytes shipped in this binary and nothing else. Hashes are
// computed from the embedded file, so editing the theme script cannot silently
// invalidate the policy the way a hard-coded hash would.
func contentSecurityPolicy(indexHTML []byte) string {
	var hashes []string
	for _, m := range scriptElement.FindAllSubmatch(indexHTML, -1) {
		if strings.Contains(strings.ToLower(string(m[1])), " src=") {
			continue // external script, already covered by 'self'
		}
		sum := sha256.Sum256(m[2])
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}

	scriptSrc := "'self'"
	if len(hashes) > 0 {
		scriptSrc += " " + strings.Join(hashes, " ")
	}

	return strings.Join([]string{
		"default-src 'none'",
		"script-src " + scriptSrc,
		// The bundler extracts component CSS into a stylesheet, but React
		// still sets style attributes at runtime, and style-src governs those.
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		// The dashboard talks to this origin only: its own API and its own
		// event stream. Anything else would be exfiltration.
		"connect-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

// Handler returns the HTTP handler for the embedded dashboard.
//
// It is meant to be mounted as the catch-all route, which means it also sees
// every request that matched no API pattern. Those two cases need opposite
// answers: an unknown page path is client-side routing and gets the shell,
// while an unknown /api path is a caller mistake and must stay a JSON 404.
// Handing HTML to a JSON client produces a bug that is genuinely hard to find.
func Handler(apiNotFound http.Handler) http.Handler {
	return handlerFor(FS(), apiNotFound)
}

// handlerFor is Handler with the file system injected, so tests can describe
// both a built and an unbuilt binary without depending on whether the frontend
// happened to be compiled in.
func handlerFor(files fs.FS, apiNotFound http.Handler) http.Handler {
	index := loadShell(files)

	writeIndex := func(w http.ResponseWriter, r *http.Request) {
		sh := index()
		if sh.body == nil {
			writeUnbuilt(w)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// index.html is never cached: it names the hashed bundles, so a stale
		// copy pins a browser to a build that may no longer exist on the
		// other end.
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Security-Policy", sh.csp)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(sh.body)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			apiNotFound.ServeHTTP(w, r)
			return
		}

		// A page is something you fetch, not something you post to. Because
		// this is the catch-all, without this check a POST to any path the
		// API does not serve — including a real GET-only endpoint reached
		// with the wrong verb — would be answered with a cheerful 200 and an
		// HTML document.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			writeIndex(w, r)
			return
		}

		f, err := files.Open(name)
		if err != nil {
			if strings.HasPrefix(name, assetPrefix) {
				// A hashed asset that is not here is a stale document asking
				// for a bundle from an older build, not a page. Answering it
				// with the shell would make the browser try to execute HTML
				// as a module, and the console error would point at the
				// bundle instead of at the document that asked for it.
				http.NotFound(w, r)
				return
			}
			// Not a file we ship: treat the path as a client-side route.
			writeIndex(w, r)
			return
		}
		defer func() { _ = f.Close() }()

		info, err := f.Stat()
		if err != nil || info.IsDir() {
			writeIndex(w, r)
			return
		}

		rs, ok := f.(io.ReadSeeker)
		if !ok {
			writeIndex(w, r)
			return
		}

		if strings.HasPrefix(name, assetPrefix) {
			w.Header().Set("Cache-Control", immutableCacheControl)
		} else {
			// Unhashed name: the content behind it can change with the next
			// release, so it has to be revalidated every time.
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
	})
}

// writeUnbuilt explains an empty embed instead of returning a blank 200.
//
// This is what a backend contributor sees after `go build` without Node. A
// silent empty page reads as "the dashboard is broken"; the real answer is
// "the dashboard was never built into this binary", and only one of those
// tells them what to do next.
func writeUnbuilt(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprint(w, "The SubGlance dashboard is not part of this binary.\n\n"+
		"Build it with `make web-install && make web-build`, then rebuild with `make build`.\n"+
		"The API at /api/v1 is unaffected.\n")
}

// isAPIPath reports whether a path belongs to the JSON API.
//
// Exactly "/api" counts too: that is a caller aiming at the API, not a page.
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}
