package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/statuspage"
	"github.com/frankgraave/subglance/internal/store"
	"github.com/frankgraave/subglance/internal/webui"
)

// The public status page: the read path a visitor without an account uses
// (docs/design/status-page.md). Two formats of one answer, the HTML page at
// /status/{slug} and the JSON at /api/v1/status-pages/{slug}, both built by
// statuspage.Builder from the public types only.
//
// What a visitor can learn from this file's responses is limited three ways:
// the builder copies nothing but the allowlisted fields, an unknown and a
// disabled slug get the same bytes, and every failure past that is a fixed
// 503 whose cause goes to the log.

// statusPageCacheFor is how long one rendered answer is reused, here and, by
// its Cache-Control header, by any proxy in front (design §3.3). It is also
// how long switching a page off can take to show, which the docs state.
const statusPageCacheFor = 30 * time.Second

// Rate limits on the public status page routes (design §3.2), checked before
// the page is looked up. They guard the database, so a request answered from
// the cache does not spend them; see servePublicStatusPage.
const (
	statusPageIPRate     = 5.0
	statusPageIPBurst    = 20.0
	statusPageFloodRate  = 50.0
	statusPageFloodBurst = 100.0
)

// statusPageNotFoundHTML is the one answer for a slug that names no page
// and for a page that is switched off. It must not vary with either, or it
// would tell a prober which slugs exist (design §3.1).
const statusPageNotFoundHTML = "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">" +
	"<title>Not found</title></head><body><p>There is no status page at this address.</p></body></html>\n"

// statusPageUnavailableHTML is the fixed body of a 503 on the HTML route.
// The cause is logged, never shown (design §3.5).
const statusPageUnavailableHTML = "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">" +
	"<title>Unavailable</title></head><body><p>This status page cannot be shown right now. " +
	"Try again in a minute.</p></body></html>\n"

// requestedSlug accepts what the slug column can match: the design's rule,
// with capitals because the column compares without case. Anything else is
// answered with the ordinary 404 without asking the database.
var requestedSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)

// publicFormat is which of the two renderings a request wants. The cache
// keys on it, so a JSON request can never be answered with cached HTML.
type publicFormat int

const (
	formatHTML publicFormat = iota
	formatJSON
)

type publicPageKey struct {
	slug   string
	format publicFormat
}

// publicPageAnswer is one rendered answer and everything its headers need.
type publicPageAnswer struct {
	body      []byte
	etag      string
	indexable bool
	expires   time.Time
	// csp is the HTML document's own policy, which depends on the page:
	// an accent adds its stylesheet's hash. Empty for JSON.
	csp string
}

// publicPageSlot holds one page's cached answer. Its mutex is held while the
// answer is rebuilt, so a crowd arriving when it expires waits for one render
// instead of each running its own.
type publicPageSlot struct {
	mu     sync.Mutex
	answer *publicPageAnswer
}

// publicPages is the state of the public routes: their limits and their
// cache. The zero value is ready; now is overridden by tests.
type publicPages struct {
	floodByIP ipBuckets
	flood     tokenBucket

	mu    sync.Mutex
	slots map[publicPageKey]*publicPageSlot

	// logos caches the logo files the pages link to (status_page_logo.go).
	logos publicLogos

	now func() time.Time
}

func (pp *publicPages) clock() time.Time {
	if pp.now != nil {
		return pp.now()
	}
	return time.Now()
}

// cached returns the answer for key while it is fresh.
func (pp *publicPages) cached(key publicPageKey, now time.Time) *publicPageAnswer {
	pp.mu.Lock()
	slot := pp.slots[key]
	pp.mu.Unlock()
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.answer != nil && now.Before(slot.answer.expires) {
		return slot.answer
	}
	return nil
}

// slot returns the slot for key, creating it. Only called once the slug is
// known to name an enabled page, so the map holds at most two slots per
// page, never one per slug a prober invents.
func (pp *publicPages) slot(key publicPageKey) *publicPageSlot {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if pp.slots == nil {
		pp.slots = make(map[publicPageKey]*publicPageSlot)
	}
	s := pp.slots[key]
	if s == nil {
		s = &publicPageSlot{}
		pp.slots[key] = s
	}
	return s
}

// forget drops every cached answer. An administrator's change to a page then
// shows on this server at the next request; a proxy in front may still hold
// the old answer for up to statusPageCacheFor.
func (pp *publicPages) forget() {
	pp.mu.Lock()
	pp.slots = nil
	pp.mu.Unlock()
	pp.logos.forget()
}

// statusPageRenderer is built once, from the stylesheet embedded in this
// binary. A binary built without the frontend has no stylesheet; its pages
// are unstyled but still complete, which beats refusing to publish them.
var statusPageRenderer = sync.OnceValues(func() (*statuspage.Renderer, error) {
	return statuspage.NewRenderer(webui.StatusPageCSS())
})

// WithStatusPageRecovery tells the public status page which confirmed
// incidents are already seeing passing checks, so it can call them degraded
// rather than down (design §1.3). Without it a confirmed incident shows as
// down until it closes. The monitor runner satisfies it.
func (s *Server) WithStatusPageRecovery(r statuspage.RecoverySource) *Server {
	s.statusPageRecovery = r
	return s
}

func (s *Server) handlePublicStatusPageHTML(w http.ResponseWriter, r *http.Request) {
	s.servePublicStatusPage(w, r, formatHTML)
}

func (s *Server) handlePublicStatusPageJSON(w http.ResponseWriter, r *http.Request) {
	s.servePublicStatusPage(w, r, formatJSON)
}

// servePublicStatusPage answers one public request.
//
// The order is the point:
//
//  1. A fresh cached answer is served straight away. It costs no database
//     read, so it does not spend the rate limits either: during an outage a
//     page shared with many people, perhaps all behind one proxy address,
//     keeps answering instead of turning into 429s at the moment it matters.
//  2. Otherwise the per-address and then the global limit run, before any
//     lookup, so a flood of invented slugs cannot reach the reader pool the
//     dashboard needs (design §3.2).
//  3. Then the page is looked up; unknown and disabled get the same 404.
//  4. Then one render per page and format, which the cache keeps.
func (s *Server) servePublicStatusPage(w http.ResponseWriter, r *http.Request, format publicFormat) {
	pp := &s.publicPages
	now := pp.clock()
	requested := r.PathValue("slug")
	valid := requestedSlug.MatchString(requested)
	key := publicPageKey{slug: strings.ToLower(requested), format: format}

	if valid {
		if a := pp.cached(key, now); a != nil {
			writePublicAnswer(w, r, format, a, now)
			return
		}
	}

	if !pp.floodByIP.allow(s.clientIP(r), now, statusPageIPRate, statusPageIPBurst) ||
		!pp.flood.allow(now, statusPageFloodRate, statusPageFloodBurst) {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Cache-Control", "no-store")
		writePublicError(w, format, http.StatusTooManyRequests)
		return
	}

	if s.db == nil || !valid {
		writePublicError(w, format, http.StatusNotFound)
		return
	}
	page, err := s.db.GetStatusPageBySlug(r.Context(), requested)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writePublicError(w, format, http.StatusNotFound)
		return
	case err != nil:
		s.log.Error("public status page: look up page", "slug", key.slug, "error", err)
		writePublicError(w, format, http.StatusServiceUnavailable)
		return
	case !page.Enabled:
		writePublicError(w, format, http.StatusNotFound)
		return
	}

	// The slug column only folds ASCII case and the pattern above only lets
	// ASCII through, so the stored slug is the lowercased request.
	key.slug = page.Slug
	slot := pp.slot(key)
	slot.mu.Lock()
	a := slot.answer
	if a == nil || !now.Before(a.expires) {
		a, err = s.renderPublicStatusPage(r, page, format, now)
		if err != nil {
			slot.mu.Unlock()
			s.log.Error("public status page: build", "slug", page.Slug, "error", err)
			writePublicError(w, format, http.StatusServiceUnavailable)
			return
		}
		slot.answer = a
	}
	slot.mu.Unlock()
	writePublicAnswer(w, r, format, a, now)
}

// renderPublicStatusPage builds and renders one answer.
func (s *Server) renderPublicStatusPage(r *http.Request, p store.StatusPage, format publicFormat, now time.Time) (*publicPageAnswer, error) {
	built, err := statuspage.Builder{DB: s.db, Recovery: s.statusPageRecovery}.Build(r.Context(), p, now)
	if err != nil {
		return nil, err
	}
	var body []byte
	var csp string
	switch format {
	case formatJSON:
		body, err = json.Marshal(built)
		body = append(body, '\n')
	default:
		var rnd *statuspage.Renderer
		if rnd, err = statusPageRenderer(); err == nil {
			body, err = rnd.Render(built)
			csp = rnd.ContentSecurityPolicyFor(built)
		}
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	return &publicPageAnswer{
		body: body,
		// Strong, because the tag names these exact bytes: a cached answer
		// is served byte for byte until it expires.
		etag:      `"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`,
		indexable: p.Indexable,
		expires:   now.Add(statusPageCacheFor),
		csp:       csp,
	}, nil
}

// writePublicAnswer writes a 200, or a 304 when the caller already holds
// these bytes. It never reads or sets a cookie: a shared cache must be able
// to hand the same answer to everyone (design §3.3).
func writePublicAnswer(w http.ResponseWriter, r *http.Request, format publicFormat, a *publicPageAnswer, now time.Time) {
	h := w.Header()
	h.Set("ETag", a.etag)
	h.Set("Cache-Control", "public, max-age="+maxAge(a.expires.Sub(now)))
	if !a.indexable {
		h.Set("X-Robots-Tag", "noindex")
	}
	if format == formatHTML && a.csp != "" {
		// The page's own policy: its inline stylesheets and theme script by
		// hash, fonts and logo from beside it, and no framing. It replaces
		// the API's default-src 'none', which would block the stylesheet.
		h.Set("Content-Security-Policy", a.csp)
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagListMatches(inm, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if format == formatJSON {
		h.Set("Content-Type", "application/json; charset=utf-8")
	} else {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	// The request only chose which page; the bytes are html/template's
	// escaped output or json.Marshal of the public types, built in
	// renderPublicStatusPage.
	_, _ = w.Write(a.body) //nolint:gosec // G705: escaped template or JSON output, not request data
}

// maxAge is the seconds left on a cached answer, rounded up and never zero,
// so a proxy stops reusing it when this server does.
func maxAge(left time.Duration) string {
	secs := int64((left + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}

// etagListMatches is If-None-Match's weak comparison against one tag.
func etagListMatches(header, etag string) bool {
	tags, star, ok := parseIfMatch(header)
	if !ok {
		return false
	}
	if star {
		return true
	}
	want := strings.Trim(etag, `"`)
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// writePublicError writes a fixed answer for status. The body depends on
// the status and the format only, never on the slug or the cause, so an
// unknown and a disabled page are byte-identical.
func writePublicError(w http.ResponseWriter, format publicFormat, status int) {
	h := w.Header()
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("X-Robots-Tag", "noindex")
	if format == formatJSON {
		msg := "status page not found"
		switch status {
		case http.StatusTooManyRequests:
			msg = "too many requests; try again in a moment"
		case http.StatusServiceUnavailable:
			msg = "this status page cannot be shown right now"
		}
		writeError(w, status, msg)
		return
	}
	body := statusPageNotFoundHTML
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		body = statusPageUnavailableHTML
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// handleStatusPageFont serves the page's typefaces beside it. The page asks
// for them relative to itself (statuspage.FontPath), so the same document
// works at /status/acme, at /status/acme/ and at the root of a subdomain
// proxied there. They are the same public files the dashboard serves at
// /fonts/, so they are not rate limited either.
func (s *Server) handleStatusPageFont(w http.ResponseWriter, r *http.Request) {
	webui.ServeFont(w, r, r.PathValue("file"))
}
