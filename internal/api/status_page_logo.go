package api

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/image/webp"

	"github.com/frankgraave/subglance/internal/statuspage"
	"github.com/frankgraave/subglance/internal/store"
)

// A status page's logo: uploaded by an administrator, stored in the
// database, and served beside the public page from this server, so the page
// loads nothing from a third party and its policy needs no outside origin.

// maxLogoSide bounds each side of a logo in pixels. It is checked from the
// image header before the pixels are decoded, so a small file that claims
// an enormous canvas is refused without allocating it.
const maxLogoSide = 2048

// logoFormats maps what the decoder recognised in the bytes to the type the
// logo is stored and served as. Only these decoders are consulted, so an SVG,
// a GIF or a document named logo.png matches none of them.
var logoFormats = []struct {
	contentType string
	magic       func([]byte) bool
	config      func(io.Reader) (image.Config, error)
	decode      func(io.Reader) (image.Image, error)
}{
	{"image/png", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) }, png.DecodeConfig, png.Decode},
	{"image/jpeg", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\xff\xd8\xff")) }, jpeg.DecodeConfig, jpeg.Decode},
	{"image/webp", func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
	}, webp.DecodeConfig, webp.Decode},
}

// errLogo is a refused upload; its text is the message the operator sees.
type errLogo string

func (e errLogo) Error() string { return string(e) }

// checkLogo decides a logo's type from its bytes and proves them: the
// header must name a PNG, JPEG or WebP image of a sane size, and the whole
// image must then decode. A file that only starts like a PNG fails the
// second step.
func checkLogo(data []byte) (store.StatusPageLogoUpload, error) {
	if len(data) == 0 {
		return store.StatusPageLogoUpload{}, errLogo("the file is empty")
	}
	if len(data) > store.MaxStatusPageLogoBytes {
		return store.StatusPageLogoUpload{}, errLogo("the logo is larger than " + strconv.Itoa(store.MaxStatusPageLogoBytes>>10) + " KB")
	}
	for _, f := range logoFormats {
		if !f.magic(data) {
			continue
		}
		cfg, err := f.config(bytes.NewReader(data))
		if err != nil {
			return store.StatusPageLogoUpload{}, errLogo("the file is not a readable " + logoName(f.contentType) + " image")
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxLogoSide || cfg.Height > maxLogoSide {
			return store.StatusPageLogoUpload{}, errLogo("the logo must be at most " + strconv.Itoa(maxLogoSide) + " pixels on each side")
		}
		if _, err := f.decode(bytes.NewReader(data)); err != nil {
			return store.StatusPageLogoUpload{}, errLogo("the file is not a readable " + logoName(f.contentType) + " image")
		}
		return store.StatusPageLogoUpload{ContentType: f.contentType, Width: cfg.Width, Height: cfg.Height, Data: data}, nil
	}
	return store.StatusPageLogoUpload{}, errLogo("the logo must be a PNG, JPEG or WebP image; SVG is not accepted, because it can carry script")
}

func logoName(contentType string) string {
	switch contentType {
	case "image/png":
		return "PNG"
	case "image/jpeg":
		return "JPEG"
	default:
		return "WebP"
	}
}

func logoResponse(p store.StatusPage) *statusPageLogoResponse {
	if p.Logo == nil {
		return nil
	}
	return &statusPageLogoResponse{
		ContentType: p.Logo.ContentType, Width: p.Logo.Width, Height: p.Logo.Height, Bytes: p.Logo.Bytes,
		Path:      statuspage.LogoRoot + statuspage.LogoPath + p.Logo.FileName(),
		UpdatedAt: p.Logo.UpdatedAt.Format(time.RFC3339),
	}
}

// statusPageLogoResponse describes a page's logo to its administrator.
// Path is where visitors load it while the page is published.
type statusPageLogoResponse struct {
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Bytes       int    `json:"bytes"`
	Path        string `json:"path"`
	UpdatedAt   string `json:"updated_at"`
}

// handleSetStatusPageLogo stores the request body as the page's logo. The
// body is the image itself; its Content-Type header and any file name are
// ignored, because only the bytes say what the file is.
func (s *Server) handleSetStatusPageLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, store.MaxStatusPageLogoBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge,
				fieldProblem("logo", "the logo is larger than "+strconv.Itoa(store.MaxStatusPageLogoBytes>>10)+" KB"))
			return
		}
		writeError(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	logo, err := checkLogo(data)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, fieldProblem("logo", err.Error()))
		return
	}
	if _, err := s.db.SetStatusPageLogo(r.Context(), p.ID, logo); s.writeStatusPageError(w, err, "update", p.ID) {
		return
	}
	s.log.Info("status page logo set", "page_id", p.ID, "slug", p.Slug, "type", logo.ContentType, "bytes", len(data))
	s.publicPages.forget()
	fresh, err := s.db.GetStatusPage(r.Context(), p.ID)
	if s.writeStatusPageError(w, err, "read", p.ID) {
		return
	}
	s.writeStatusPage(w, r, http.StatusOK, fresh)
}

func (s *Server) handleDeleteStatusPageLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteStatusPageLogo(r.Context(), p.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "this status page has no logo")
			return
		}
		s.writeStatusPageError(w, err, "update", p.ID)
		return
	}
	s.log.Info("status page logo removed", "page_id", p.ID, "slug", p.Slug)
	s.publicPages.forget()
	fresh, err := s.db.GetStatusPage(r.Context(), p.ID)
	if s.writeStatusPageError(w, err, "read", p.ID) {
		return
	}
	s.writeStatusPage(w, r, http.StatusOK, fresh)
}

// handleGetStatusPageLogo serves the logo to the administrator, published
// or not, so the settings form can show it before the page goes out.
func (s *Server) handleGetStatusPageLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	logo, data, err := s.db.StatusPageLogoData(r.Context(), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "this status page has no logo")
		return
	}
	if s.writeStatusPageError(w, err, "read", p.ID) {
		return
	}
	writeLogo(w, logo.ContentType, data, "private, no-store")
}

// writeLogo writes stored image bytes as the type decided on upload. The
// server-wide nosniff and default-src 'none' headers already stand; the
// sandbox keeps a browser that opens the file directly from treating it as
// anything with an origin of its own.
func writeLogo(w http.ResponseWriter, contentType string, data []byte, cacheControl string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Cache-Control", cacheControl)
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) //nolint:gosec // G705: stored image bytes, checked as an image on upload
}

// publicLogo is one cached logo file.
type publicLogo struct {
	contentType string
	data        []byte
	expires     time.Time
}

// publicLogos caches the logo files the public pages link to, for as long
// as a page answer is cached, so a crowd opening a page during an outage
// does not read the image from the database once each. It holds only files
// that were found and published, never a name a prober invented, and
// publicPages.forget empties it with the pages.
type publicLogos struct {
	mu    sync.Mutex
	files map[string]publicLogo
}

func (pl *publicLogos) get(name string, now time.Time) (publicLogo, bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	f, ok := pl.files[name]
	if !ok || !now.Before(f.expires) {
		return publicLogo{}, false
	}
	return f, true
}

func (pl *publicLogos) put(name string, f publicLogo) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.files == nil {
		pl.files = map[string]publicLogo{}
	}
	pl.files[name] = f
}

func (pl *publicLogos) forget() {
	pl.mu.Lock()
	pl.files = nil
	pl.mu.Unlock()
}

// handlePublicStatusPageLogo serves a published page's logo by its random
// file name. It answers like the page does: from the cache without spending
// the rate limits, otherwise after them, and with the same 404 for an
// unknown file and for the logo of a page that is switched off.
func (s *Server) handlePublicStatusPageLogo(w http.ResponseWriter, r *http.Request) {
	pp := &s.publicPages
	now := pp.clock()
	name := r.PathValue("file")
	cache := "public, max-age=" + maxAge(statusPageCacheFor)
	if f, ok := pp.logos.get(name, now); ok {
		writeLogo(w, f.contentType, f.data, cache)
		return
	}
	if !pp.floodByIP.allow(s.clientIP(r), now, statusPageIPRate, statusPageIPBurst) ||
		!pp.flood.allow(now, statusPageFloodRate, statusPageFloodBurst) {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusTooManyRequests, "too many requests; try again in a moment")
		return
	}
	if s.db == nil {
		writeLogoNotFound(w)
		return
	}
	logo, data, err := s.db.PublishedStatusPageLogo(r.Context(), name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLogoNotFound(w)
		return
	case err != nil:
		s.log.Error("public status page: read logo", "error", err)
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusServiceUnavailable, "this logo cannot be shown right now")
		return
	}
	pp.logos.put(name, publicLogo{contentType: logo.ContentType, data: data, expires: now.Add(statusPageCacheFor)})
	writeLogo(w, logo.ContentType, data, cache)
}

func writeLogoNotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	writeError(w, http.StatusNotFound, "not found")
}
