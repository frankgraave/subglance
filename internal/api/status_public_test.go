package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Strings a monitor carries that no visitor may ever see (design §1.2).
// Each is distinctive enough not to occur in a page by chance.
const (
	pubSecretName   = "internal-billing-db-7f3a"
	pubSecretTarget = "https://billing.internal.example/health?key=zz9plural"
	pubSecretHost   = "billing.internal"
	pubSecretHeader = "X-Secret-Header-q81"
	pubSecretBody   = "stacktrace at billing.go:42 qx7"
	pubSecretError  = "dial tcp: connection refused by db-primary-3"
	pubSecretTagKey = "tenant-zq4"
	pubSecretTagVal = "acme-corp-hidden"
	pubSecretCause  = "cause-db-primary-failover"
	pubSecretPush   = "nightly-export-job-k2"
	pubDisplay      = "Billing"
	pubPushDisplay  = "Exports"
	pubTitle        = "Acme services"
)

// Source addresses for the rate-limit tests.
var (
	addrA     = strings.Join([]string{"203", "0", "113", "9"}, ".")
	addrB     = strings.Join([]string{"198", "51", "100", "20"}, ".")
	addrProxy = strings.Join([]string{"192", "0", "2", "1"}, ".")
)

type pubRecovery map[int64]bool

func (f pubRecovery) Recovery(id int64) (int, int, bool) { return 1, 2, f[id] }

type publicFixture struct {
	srv      *Server
	db       *store.DB
	h        http.Handler
	page     store.StatusPage
	monitor  store.Monitor
	push     store.Monitor
	now      time.Time
	nextAddr int
}

// seedPublicPage builds an enabled page showing one HTTP monitor that
// carries every secret above and one push monitor whose token is a secret
// too, both under public names, with a passing check each.
func seedPublicPage(t *testing.T) *publicFixture {
	t.Helper()
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	f := &publicFixture{srv: srv, db: db, now: time.Now().UTC().Truncate(time.Second)}
	srv.publicPages.now = func() time.Time { return f.now }

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: pubSecretName, Type: "http", Target: pubSecretTarget, Enabled: true,
		Headers: map[string]string{pubSecretHeader: "v"},
		Tags:    map[string]string{pubSecretTagKey: pubSecretTagVal},
	})
	if err != nil {
		t.Fatal(err)
	}
	push, err := db.CreateMonitor(ctx, store.Monitor{
		Name: pubSecretPush, Type: "push", Enabled: true, PushIntervalS: 3600, PushGraceS: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if push.PushToken == "" {
		t.Fatal("push monitor has no token to look for")
	}
	p, err := db.CreateStatusPage(ctx, store.StatusPage{
		Slug: "acme", Title: pubTitle, Selection: store.StatusPageSelectMonitors, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetStatusPageEntries(ctx, p.ID, []store.StatusPageEntryInput{
		{MonitorID: m.ID, DisplayName: pubDisplay}, {MonitorID: push.ID, DisplayName: pubPushDisplay},
	}); err != nil {
		t.Fatal(err)
	}
	f.page, f.monitor, f.push = p, m, push
	f.beat(t, m.ID, f.now.Add(-2*time.Minute), true)
	f.beat(t, push.ID, f.now.Add(-2*time.Minute), true)
	f.h = srv.Handler()
	return f
}

func (f *publicFixture) beat(t *testing.T, id int64, at time.Time, ok bool) {
	t.Helper()
	hb := store.Heartbeat{MonitorID: id, TS: at, OK: ok, Assessment: "up", StatusCode: 200}
	if !ok {
		hb.Assessment, hb.StatusCode, hb.Error = "down", 503, pubSecretError
		hb.Response = &store.ResponseSnapshot{Body: pubSecretBody, Headers: map[string]string{pubSecretHeader: pubSecretBody}}
	}
	if err := f.db.RecordHeartbeat(t.Context(), hb); err != nil {
		t.Fatal(err)
	}
}

// get sends a GET from a fresh address each time unless one is given, so
// tests that are not about rate limits never run into them.
func (f *publicFixture) get(t *testing.T, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	f.nextAddr++
	return f.getFrom(t, "2001:db8::"+strconv.FormatInt(int64(f.nextAddr), 16), target, hdr...)
}

func (f *publicFixture) getFrom(t *testing.T, addr, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = addr + ":40000"
	if strings.Contains(addr, ":") {
		req.RemoteAddr = "[" + addr + "]:40000"
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

// Criterion 3: nothing internal in the HTML, the JSON or the headers, for
// up, down and degraded.
func TestPublicStatusPageLeaksNothingInternal(t *testing.T) {
	f := seedPublicPage(t)
	ctx := t.Context()
	secrets := []string{pubSecretName, pubSecretTarget, pubSecretHost, pubSecretHeader, pubSecretBody,
		pubSecretError, pubSecretTagKey, pubSecretTagVal, pubSecretCause, pubSecretPush, f.push.PushToken,
		f.push.PushTokenPrefix}

	stages := []struct {
		name    string
		prepare func()
		want    string
	}{
		{name: "up", want: `"status":"up"`},
		{name: "down", prepare: func() {
			f.beat(t, f.monitor.ID, f.now.Add(-time.Minute), false)
			if _, err := f.db.OpenIncident(ctx, f.monitor.ID, f.now.Add(-90*time.Second), pubSecretCause, pubSecretError); err != nil {
				t.Fatal(err)
			}
			if err := f.db.ConfirmIncident(ctx, f.monitor.ID, f.now.Add(-time.Minute), pubSecretCause, pubSecretError); err != nil {
				t.Fatal(err)
			}
		}, want: `"status":"down"`},
		{name: "degraded", prepare: func() {
			f.srv.statusPageRecovery = pubRecovery{f.monitor.ID: true}
		}, want: `"status":"degraded"`},
	}
	for _, st := range stages {
		t.Run(st.name, func(t *testing.T) {
			if st.prepare != nil {
				st.prepare()
			}
			f.now = f.now.Add(statusPageCacheFor + time.Second) // past the cache
			for _, target := range []string{"/status/acme", "/api/v1/status-pages/acme"} {
				rec := f.get(t, target)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s: status %d: %s", target, rec.Code, rec.Body.String())
				}
				body := rec.Body.String()
				var headers strings.Builder
				for k, vs := range rec.Header() {
					headers.WriteString(k + ": " + strings.Join(vs, ", ") + "\n")
				}
				for _, s := range secrets {
					if strings.Contains(body, s) {
						t.Errorf("GET %s: body contains %q", target, s)
					}
					if strings.Contains(headers.String(), s) {
						t.Errorf("GET %s: headers contain %q:\n%s", target, s, headers.String())
					}
				}
				if !strings.Contains(body, pubDisplay) || !strings.Contains(body, pubTitle) {
					t.Errorf("GET %s: the public name or title is missing", target)
				}
				if strings.HasPrefix(target, "/api/") && !strings.Contains(body, st.want) {
					t.Errorf("GET %s: want %s in %s", target, st.want, body)
				}
			}
		})
	}
}

// Criterion 4: an unknown slug, a malformed one and a page that is switched
// off get the same bytes and the same headers.
func TestPublicStatusPageNotFoundIsIdentical(t *testing.T) {
	f := seedPublicPage(t)
	if _, err := f.db.CreateStatusPage(t.Context(), store.StatusPage{
		Slug: "draft", Title: "Draft", Selection: store.StatusPageSelectMonitors,
	}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"/status/", "/api/v1/status-pages/"} {
		want := f.get(t, prefix+"nosuchpage")
		if want.Code != http.StatusNotFound {
			t.Fatalf("%snosuchpage: status %d, want 404", prefix, want.Code)
		}
		for _, slug := range []string{"draft", "DRAFT", "bad_slug", strings.Repeat("a", 64)} {
			got := f.get(t, prefix+slug)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Errorf("%s%s: %d %q, want %d %q", prefix, slug, got.Code, got.Body.String(), want.Code, want.Body.String())
			}
			if g, w := headerDump(got.Header()), headerDump(want.Header()); g != w {
				t.Errorf("%s%s: headers differ:\n%s\nwant:\n%s", prefix, slug, g, w)
			}
		}
	}
}

func headerDump(h http.Header) string {
	var b strings.Builder
	for _, k := range []string{"Content-Type", "Cache-Control", "X-Robots-Tag", "Content-Security-Policy", "Etag", "Set-Cookie", "Retry-After"} {
		b.WriteString(k + "=" + strings.Join(h.Values(k), ",") + "\n")
	}
	return b.String()
}

// Criterion 5: the per-address limit refuses with 429 and Retry-After, and
// runs before the page is looked up.
func TestPublicStatusPagePerAddressLimitPrecedesTheLookup(t *testing.T) {
	f := seedPublicPage(t)
	for i := range int(statusPageIPBurst) {
		if rec := f.getFrom(t, addrA, "/status/nosuch"+strconv.Itoa(i)); rec.Code != http.StatusNotFound {
			t.Fatalf("request %d inside the burst: status %d, want 404", i, rec.Code)
		}
	}
	// With the database gone, a request that reached the lookup would be a
	// 503. A 429 proves the limit answered first.
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	rec := f.getFrom(t, addrA, "/api/v1/status-pages/nosuch")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("past the burst: status %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a refused request does not say when to try again")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("429 Cache-Control = %q, want no-store so a proxy does not keep it", cc)
	}
	// Another address has its own allowance; it reaches the (closed)
	// database, and gets the fixed 503, with no cause in the body.
	rec = f.getFrom(t, addrB, "/status/nosuch")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("another address: status %d, want 503", rec.Code)
	}
	if b := strings.ToLower(rec.Body.String()); strings.Contains(b, "closed") || strings.Contains(b, "sql") {
		t.Errorf("503 body names the cause: %s", b)
	}
}

// A page already rendered is served from the cache without spending the
// limits: a page shared during an outage, perhaps with a whole office behind
// one address, keeps answering instead of turning into 429s.
func TestPublicStatusPageCacheHitsDoNotSpendTheLimits(t *testing.T) {
	f := seedPublicPage(t)
	if rec := f.getFrom(t, addrA, "/status/acme"); rec.Code != http.StatusOK {
		t.Fatalf("first request: %d", rec.Code)
	}
	for i := range 3 * int(statusPageIPBurst) {
		if rec := f.getFrom(t, addrA, "/status/acme"); rec.Code != http.StatusOK {
			t.Fatalf("cached request %d from one address: %d, want 200", i, rec.Code)
		}
	}
	// The allowance is still whole for anything that would read the store.
	for i := range int(statusPageIPBurst) - 1 {
		if rec := f.getFrom(t, addrA, "/status/nosuch"); rec.Code != http.StatusNotFound {
			t.Fatalf("uncached request %d: %d, want 404", i, rec.Code)
		}
	}
}

// Criterion 5: the global limit holds whatever the address.
func TestPublicStatusPageGlobalLimit(t *testing.T) {
	f := seedPublicPage(t)
	now := f.now
	for range int(statusPageFloodBurst) {
		f.srv.publicPages.flood.allow(now, statusPageFloodRate, statusPageFloodBurst)
	}
	rec := f.get(t, "/status/nosuch")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q; want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Errorf("the HTML route answered a limit with a non-HTML body: %s", rec.Body.String())
	}
}

// Criterion 5: behind a trusted proxy each visitor has their own bucket,
// and an untrusted caller cannot pick one by forging X-Forwarded-For.
func TestPublicStatusPageLimitUsesTheTrustedProxyAddress(t *testing.T) {
	f := seedPublicPage(t)
	if _, err := f.srv.WithTrustedProxies(addrProxy); err != nil {
		t.Fatal(err)
	}
	f.h = f.srv.Handler()
	for i := 0; i <= int(statusPageIPBurst); i++ {
		f.getFrom(t, addrProxy, "/status/nosuch", "X-Forwarded-For", addrA)
	}
	if rec := f.getFrom(t, addrProxy, "/status/nosuch", "X-Forwarded-For", addrA); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("visitor A behind the proxy past the burst: %d, want 429", rec.Code)
	}
	if rec := f.getFrom(t, addrProxy, "/status/nosuch", "X-Forwarded-For", addrB); rec.Code != http.StatusNotFound {
		t.Fatalf("visitor B behind the same proxy: %d, want 404 (own bucket)", rec.Code)
	}

	// An untrusted caller naming a new address on every request still
	// spends one bucket: its own.
	var last int
	for i := 0; i <= int(statusPageIPBurst); i++ {
		last = f.getFrom(t, addrB, "/status/nosuch", "X-Forwarded-For", "2001:db8:1::"+strconv.Itoa(i+1)).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("forged X-Forwarded-For past the burst: %d, want 429", last)
	}
}

// Criterion 6: cache headers, an ETag, no cookie, and a second request
// inside the interval answered without reading the store.
func TestPublicStatusPageIsCached(t *testing.T) {
	f := seedPublicPage(t)
	first := f.get(t, "/api/v1/status-pages/acme")
	if first.Code != http.StatusOK {
		t.Fatalf("status %d", first.Code)
	}
	if cc := first.Header().Get("Cache-Control"); cc != "public, max-age=30" {
		t.Errorf("Cache-Control = %q, want public, max-age=30", cc)
	}
	etag := first.Header().Get("ETag")
	if etag == "" || strings.HasPrefix(etag, "W/") {
		t.Errorf("ETag = %q, want a strong tag", etag)
	}
	if first.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("X-Robots-Tag = %q on a page that is not indexable", first.Header().Get("X-Robots-Tag"))
	}

	// Change what the page shows behind the API's back, so the cache is
	// not told. Inside the interval the answer must not move.
	if _, err := f.db.SetStatusPageEntries(t.Context(), f.page.ID, []store.StatusPageEntryInput{
		{MonitorID: f.monitor.ID, DisplayName: "Renamed"},
	}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(20 * time.Second)
	second := f.get(t, "/api/v1/status-pages/ACME")
	if second.Body.String() != first.Body.String() || second.Header().Get("ETag") != etag {
		t.Fatal("a second request inside the interval was rendered again")
	}
	if cc := second.Header().Get("Cache-Control"); cc != "public, max-age=10" {
		t.Errorf("Cache-Control 20 s later = %q, want the 10 s that remain", cc)
	}
	if nm := f.get(t, "/api/v1/status-pages/acme", "If-None-Match", etag); nm.Code != http.StatusNotModified || nm.Body.Len() != 0 {
		t.Errorf("If-None-Match with the current tag: %d with %d bytes, want an empty 304", nm.Code, nm.Body.Len())
	}

	// The HTML has its own entry: it is rendered now, from current data,
	// and a JSON answer is never handed out for it.
	html := f.get(t, "/status/acme")
	if ct := html.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("HTML route Content-Type = %q", ct)
	}
	if !strings.Contains(html.Body.String(), "Renamed") {
		t.Error("the HTML route reused another format's cache entry")
	}

	f.now = f.now.Add(11 * time.Second)
	third := f.get(t, "/api/v1/status-pages/acme")
	if !strings.Contains(third.Body.String(), "Renamed") || third.Header().Get("ETag") == etag {
		t.Error("the answer was not rebuilt after the interval")
	}

	for _, rec := range []*httptest.ResponseRecorder{first, second, html, third} {
		if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
			t.Errorf("a public answer sets a cookie: %v", c)
		}
	}
}

// A visitor who happens to be signed in gets the public answer, byte for
// byte: the route never looks at credentials.
func TestPublicStatusPageIgnoresCredentials(t *testing.T) {
	f := seedPublicPage(t)
	anon := f.get(t, "/api/v1/status-pages/acme")
	f.srv.publicPages.forget()
	testCredentialsMu.Lock()
	token := testCredentials[f.srv]
	testCredentialsMu.Unlock()
	authed := f.get(t, "/api/v1/status-pages/acme", "Authorization", "Bearer "+token)
	if anon.Body.String() != authed.Body.String() {
		t.Error("a signed-in request got a different answer")
	}
}

// An administrator switching a page off is not kept waiting on this
// server's cache.
func TestPublicStatusPageFollowsAdminChanges(t *testing.T) {
	f := seedPublicPage(t)
	if rec := f.get(t, "/status/acme"); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := `{"slug":"acme","title":"Acme services","timezone":"UTC","selection":"monitors","enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/status-pages/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authedHandler(f.srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch off: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/status/acme"); rec.Code != http.StatusNotFound {
		t.Errorf("a page switched off still answers %d", rec.Code)
	}
}

// The HTML route: its own content policy, the indexable switch, the
// trailing-slash address a subdomain proxy uses, and fonts beside the page.
func TestPublicStatusPageHTMLRoute(t *testing.T) {
	f := seedPublicPage(t)
	rec := f.get(t, "/status/acme")
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sha256-") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want the page's hashed policy", csp)
	}
	slash := f.get(t, "/status/acme/")
	if slash.Code != http.StatusOK || slash.Body.String() != rec.Body.String() {
		t.Errorf("/status/acme/: %d, want the same page", slash.Code)
	}

	p := f.page
	p.Indexable = true
	if _, err := f.db.UpdateStatusPage(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	f.srv.publicPages.forget()
	if got := f.get(t, "/status/acme").Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("an indexable page sends X-Robots-Tag %q", got)
	}

	// The font routes answer as files: a name that is not shipped is a
	// plain 404, not the dashboard shell.
	for _, target := range []string{"/status/fonts/nosuch.woff2", "/status/acme/fonts/nosuch.woff2"} {
		got := f.get(t, target)
		if got.Code != http.StatusNotFound || strings.Contains(got.Body.String(), "<html") {
			t.Errorf("%s: %d %q, want a plain 404", target, got.Code, got.Body.String())
		}
	}
}

// The JSON route serves the public type and nothing else.
func TestPublicStatusPageJSONShape(t *testing.T) {
	f := seedPublicPage(t)
	rec := f.get(t, "/api/v1/status-pages/acme")
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	want := "description,entries,generated_at,maintenance,outages,summary,timezone,title"
	sort.Strings(keys)
	if g := strings.Join(keys, ","); g != want {
		t.Errorf("top-level fields = %s, want %s", g, want)
	}
}

// A server without a database answers the public routes with the ordinary
// 404 rather than crashing.
func TestPublicStatusPageWithoutADatabase(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status/acme", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}
