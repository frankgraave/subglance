package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 12, 6)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// putLogo uploads body as the logo of page slug, as an administrator.
func putLogo(t *testing.T, srv *Server, slug string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/status-pages/"+slug+"/logo", bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, r)
	return rec
}

// The type is decided by the bytes: each accepted format is recognised
// whatever the request claims, and its size is read from the image.
func TestLogoTypeIsReadFromTheBytes(t *testing.T) {
	webp, err := os.ReadFile("testdata/logo.webp")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, claimed, want string
		body                []byte
		w, h                int
	}{
		{"png", "image/jpeg", "image/png", pngBytes(t, 40, 20), 40, 20},
		{"jpeg", "text/plain", "image/jpeg", jpegBytes(t), 12, 6},
		{"webp", "", "image/webp", webp, 0, 0},
	} {
		got, err := checkLogo(c.body)
		if err != nil {
			t.Errorf("%s: refused: %v", c.name, err)
			continue
		}
		if got.ContentType != c.want || (c.w > 0 && (got.Width != c.w || got.Height != c.h)) {
			t.Errorf("%s: got %s %dx%d", c.name, got.ContentType, got.Width, got.Height)
		}
	}
}

// Criterion: an SVG, or a file that only pretends to be a PNG, is refused.
func TestLogoRefusesSVGAndImpostors(t *testing.T) {
	real := pngBytes(t, 4, 4)
	huge := pngBytes(t, maxLogoSide+1, 1)
	for name, body := range map[string][]byte{
		"svg":              []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"svg with bom":     append([]byte("\xef\xbb\xbf"), []byte(`<?xml version="1.0"?><svg/>`)...),
		"html named .png":  []byte("<!doctype html><script>alert(1)</script>"),
		"png signature":    []byte("\x89PNG\r\n\x1a\n<svg onload=alert(1)>"),
		"truncated png":    real[:len(real)/2],
		"png header, junk": append(append([]byte{}, real[:33]...), bytes.Repeat([]byte{0}, 64)...),
		"gif":              []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"jpeg signature":   []byte("\xff\xd8\xff\xe0 not a jpeg"),
		"webp signature":   []byte("RIFF\x10\x00\x00\x00WEBPVP8 junk"),
		"empty":            nil,
		"too wide":         huge,
	} {
		if _, err := checkLogo(body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_, err := checkLogo([]byte(`<svg/>`))
	if err == nil || !strings.Contains(err.Error(), "SVG") {
		t.Errorf("an SVG refusal should say SVG is not accepted, got %v", err)
	}
}

func TestLogoUploadReplaceAndRemove(t *testing.T) {
	srv, db := testServerWithDB(t)
	if _, err := db.CreateStatusPage(t.Context(), store.StatusPage{Slug: "acme", Title: "Acme", Selection: store.StatusPageSelectMonitors}); err != nil {
		t.Fatal(err)
	}

	rec := putLogo(t, srv, "acme", pngBytes(t, 40, 20), "image/svg+xml")
	mustStatus(t, rec, http.StatusOK, "upload")
	page := decodeStatusPageBody(t, rec)
	if page.Logo == nil || page.Logo.ContentType != "image/png" || page.Logo.Width != 40 || page.Logo.Height != 20 {
		t.Fatalf("logo = %+v, want a 40x20 PNG", page.Logo)
	}
	if !strings.HasPrefix(page.Logo.Path, "/status/logos/") || !strings.HasSuffix(page.Logo.Path, ".png") {
		t.Errorf("logo path = %q", page.Logo.Path)
	}

	// The operator can see it before the page is published.
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/status-pages/acme/logo", "")
	mustStatus(t, rec, http.StatusOK, "operator's logo")
	if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("operator's logo headers: %v", rec.Header())
	}

	rec = putLogo(t, srv, "acme", []byte(`<svg/>`), "image/png")
	mustStatus(t, rec, http.StatusBadRequest, "svg")
	var problem errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Field != "logo" {
		t.Errorf("svg refusal = %s, want a problem on the logo field", rec.Body.String())
	}

	rec = putLogo(t, srv, "acme", bytes.Repeat([]byte{0x89}, store.MaxStatusPageLogoBytes+1), "")
	mustStatus(t, rec, http.StatusRequestEntityTooLarge, "too large")

	rec = putLogo(t, srv, "acme", jpegBytes(t), "")
	mustStatus(t, rec, http.StatusOK, "replace")
	if replaced := decodeStatusPageBody(t, rec); replaced.Logo == nil || replaced.Logo.Path == page.Logo.Path {
		t.Errorf("a replaced logo must move to a new address: %+v", replaced.Logo)
	}

	rec = doJSON(t, srv, http.MethodDelete, "/api/v1/status-pages/acme/logo", "")
	mustStatus(t, rec, http.StatusOK, "remove")
	if decodeStatusPageBody(t, rec).Logo != nil {
		t.Error("logo still set after removal")
	}
	mustStatus(t, doJSON(t, srv, http.MethodDelete, "/api/v1/status-pages/acme/logo", ""), http.StatusNotFound, "remove again")
	mustStatus(t, putLogo(t, srv, "nope", pngBytes(t, 1, 1), ""), http.StatusNotFound, "unknown page")
}

func TestStatusPageBrandingSettingsRoundTrip(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"acme","title":"Acme","selection":"monitors"}`)
	mustStatus(t, rec, http.StatusCreated, "create")
	page := decodeStatusPageBody(t, rec)
	if page.Language != "en" || page.Accent != "" || page.HideCredit || page.Logo != nil {
		t.Errorf("defaults = %q/%q/%v/%v", page.Language, page.Accent, page.HideCredit, page.Logo)
	}
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme","selection":"monitors","language":"nl","accent":"#3B82F6","hide_credit":true}`)
	mustStatus(t, rec, http.StatusOK, "update")
	page = decodeStatusPageBody(t, rec)
	if page.Language != "nl" || page.Accent != "#3b82f6" || !page.HideCredit {
		t.Errorf("updated = %q/%q/%v", page.Language, page.Accent, page.HideCredit)
	}

	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme","selection":"monitors","accent":"#ffff00"}`)
	mustStatus(t, rec, http.StatusBadRequest, "low-contrast accent")
	var problem errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Field != "accent" ||
		!strings.Contains(problem.Error, ":1") {
		t.Errorf("accent refusal = %s, want the accent field and the measured ratio", rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme","selection":"monitors","language":"de"}`)
	mustStatus(t, rec, http.StatusBadRequest, "unknown language")
}

// The public page shows the logo from this server, in the page's language
// and accent, and the logo file follows the page: published, it is served
// with the page's cache lifetime; switched off, it is the same 404 as a
// name that never existed.
func TestPublicPageCarriesTheBranding(t *testing.T) {
	f := seedPublicPage(t)
	ctx := t.Context()
	logo, err := f.db.SetStatusPageLogo(ctx, f.page.ID, store.StatusPageLogoUpload{
		ContentType: "image/png", Width: 4, Height: 4, Data: pngBytes(t, 4, 4)})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := f.db.GetStatusPage(ctx, f.page.ID)
	p.Language, p.Accent, p.HideCredit = "nl", "#3b82f6", true
	if _, err := f.db.UpdateStatusPage(ctx, p); err != nil {
		t.Fatal(err)
	}

	rec := f.get(t, "/status/acme")
	mustStatus(t, rec, http.StatusOK, "page")
	html := rec.Body.String()
	for _, want := range []string{`<html lang="nl"`, `src="logos/` + logo.FileName() + `"`, ".sp-head h1{color:#3b82f6}"} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(html, "Monitored with") || strings.Contains(html, "Bewaakt met") {
		t.Error("a hidden credit is still shown")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self'") || strings.Count(csp, "'sha256-") != 3 {
		t.Errorf("policy %q: want img-src 'self' and three hashes (page CSS, accent, script)", csp)
	}

	rec = f.get(t, "/api/v1/status-pages/acme")
	var pub map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	if pub["language"] != "nl" || pub["accent"] != "#3b82f6" || pub["show_credit"] != false {
		t.Errorf("public JSON branding = %v %v %v", pub["language"], pub["accent"], pub["show_credit"])
	}
	if l, _ := pub["logo"].(map[string]any); l == nil || l["path"] != "/status/logos/"+logo.FileName() {
		t.Errorf("public JSON logo = %v", pub["logo"])
	}

	for _, path := range []string{"/status/logos/" + logo.FileName(), "/status/acme/logos/" + logo.FileName()} {
		rec = f.get(t, path)
		mustStatus(t, rec, http.StatusOK, path)
		if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			!strings.HasPrefix(rec.Header().Get("Cache-Control"), "public, max-age=") ||
			!strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s headers: %v", path, rec.Header())
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s sets a cookie", path)
		}
	}

	unknown := f.get(t, "/status/logos/0123456789abcdef.png")
	mustStatus(t, unknown, http.StatusNotFound, "unknown logo")

	p.Enabled = false
	if _, err := f.db.UpdateStatusPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	f.srv.publicPages.forget() // as the settings handler does on save
	off := f.get(t, "/status/logos/"+logo.FileName())
	mustStatus(t, off, http.StatusNotFound, "logo of a page that is off")
	if off.Body.String() != unknown.Body.String() {
		t.Errorf("a switched-off page's logo answers differently from an unknown one: %q vs %q", off.Body, unknown.Body)
	}
}

// A cached logo is served without spending the rate limit, as a cached page
// is, so a page shared during an outage keeps its logo.
func TestCachedLogoDoesNotSpendTheRateLimit(t *testing.T) {
	f := seedPublicPage(t)
	logo, err := f.db.SetStatusPageLogo(t.Context(), f.page.ID, store.StatusPageLogoUpload{
		ContentType: "image/png", Width: 4, Height: 4, Data: pngBytes(t, 4, 4)})
	if err != nil {
		t.Fatal(err)
	}
	path := "/status/logos/" + logo.FileName()
	for i := 0; i < int(statusPageIPBurst)*3; i++ {
		if rec := f.getFrom(t, addrA, path); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
	}
	// Past the cache, the limit applies again.
	f.now = f.now.Add(statusPageCacheFor + time.Second)
	var limited bool
	for i := 0; i < int(statusPageIPBurst)*3 && !limited; i++ {
		f.srv.publicPages.logos.forget()
		limited = f.getFrom(t, addrA, path).Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Error("uncached logo requests are never rate limited")
	}
}

// A logo read before an administrator's change and stored after it must not
// be cached: forget ran in between, so the read may be the file that was
// just removed, replaced or switched off.
func TestLogoCacheDropsAReadThatForgetOvertook(t *testing.T) {
	var pl publicLogos
	now := time.Now()
	f := publicLogo{contentType: "image/png", data: []byte("old"), expires: now.Add(time.Minute)}
	before := pl.current()
	pl.forget()
	pl.put("a.png", f, before)
	if _, ok := pl.get("a.png", now); ok {
		t.Error("a read taken before forget was cached after it")
	}
	pl.put("a.png", f, pl.current())
	if _, ok := pl.get("a.png", now); !ok {
		t.Error("a read taken after the last forget was not cached")
	}
}
