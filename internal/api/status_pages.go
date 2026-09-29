package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Status page management: the operator's side of the public status page
// (docs/design/status-page.md). Everything here is admin-only; the public
// read path is a separate handler with its own response type.
//
// A page is addressed by its slug rather than its numeric id. The public JSON
// lives at GET /api/v1/status-pages/{slug}, and a slug may be all digits, so
// an id-keyed admin route on the same path could not tell "page 12" from the
// page whose slug is "12". Keying both by slug gives one path with one
// meaning: GET reads what visitors see, PUT and DELETE change the page.

// statusPageRequest is the body of POST /status-pages and PUT
// /status-pages/{slug}. PUT replaces every setting: an omitted boolean is
// false, which leaves a page unpublished and unindexed rather than the
// other way round.
type statusPageRequest struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Timezone    string `json:"timezone"`
	Selection   string `json:"selection"`
	TagKey      string `json:"tag_key"`
	TagValue    string `json:"tag_value"`
	Indexable   bool   `json:"indexable"`
	Enabled     bool   `json:"enabled"`
}

type statusPageEntryRequest struct {
	MonitorID   int64  `json:"monitor_id"`
	DisplayName string `json:"display_name"`
}

type statusPageEntriesRequest struct {
	Entries []statusPageEntryRequest `json:"entries"`
}

type statusPageEntryResponse struct {
	MonitorID   int64  `json:"monitor_id"`
	PublicKey   string `json:"public_key"`
	DisplayName string `json:"display_name"`
}

// statusPageResponse is a page as its administrator sees it. It is never
// served to a visitor.
type statusPageResponse struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Timezone    string `json:"timezone"`
	Selection   string `json:"selection"`
	TagKey      string `json:"tag_key"`
	TagValue    string `json:"tag_value"`
	Indexable   bool   `json:"indexable"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	// Entries is every entry in page order, including those a tag page
	// does not show right now because the monitor lost the tag.
	Entries []statusPageEntryResponse `json:"entries"`
	// UnnamedMonitorIDs lists, for a tag page, the monitors that carry the
	// tag but have no public name yet and are therefore not shown. It is
	// always empty for a page that selects monitors.
	UnnamedMonitorIDs []int64 `json:"unnamed_monitor_ids"`
}

// statusPageView reads a page's entries and unnamed monitors and builds its
// response.
func (s *Server) statusPageView(r *http.Request, p store.StatusPage) (statusPageResponse, error) {
	entries, err := s.db.ListStatusPageEntries(r.Context(), p.ID)
	if err != nil {
		return statusPageResponse{}, err
	}
	unnamed, err := s.db.UnnamedStatusPageMonitors(r.Context(), p)
	if err != nil {
		return statusPageResponse{}, err
	}
	out := statusPageResponse{
		ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description,
		Timezone: p.Timezone, Selection: p.Selection, TagKey: p.TagKey, TagValue: p.TagValue,
		Indexable: p.Indexable, Enabled: p.Enabled,
		CreatedAt:         p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         p.UpdatedAt.Format(time.RFC3339),
		Entries:           make([]statusPageEntryResponse, 0, len(entries)),
		UnnamedMonitorIDs: unnamed,
	}
	for _, e := range entries {
		out.Entries = append(out.Entries, statusPageEntryResponse{
			MonitorID: e.MonitorID, PublicKey: e.PublicKey, DisplayName: e.DisplayName,
		})
	}
	return out, nil
}

// writeStatusPage answers with one page, or with a 500 when its entries
// cannot be read.
func (s *Server) writeStatusPage(w http.ResponseWriter, r *http.Request, status int, p store.StatusPage) {
	view, err := s.statusPageView(r, p)
	if err != nil {
		s.log.Error("read status page entries", "page_id", p.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the status page")
		return
	}
	writeJSON(w, status, view)
}

// writeStatusPageError maps the store's refusals to statuses. It reports
// false when err was nil and nothing was written.
func (s *Server) writeStatusPageError(w http.ResponseWriter, err error, action string, pageID int64) bool {
	var invalid *store.StatusPageError
	switch {
	case err == nil:
		return false
	case errors.As(err, &invalid):
		writeProblem(w, http.StatusBadRequest, fieldProblem(invalid.Field, invalid.Msg))
	case errors.Is(err, store.ErrStatusPageSlugTaken):
		writeProblem(w, http.StatusConflict, fieldProblem("slug", "another status page already uses this slug"))
	case errors.Is(err, store.ErrUnknownMonitor):
		writeProblem(w, http.StatusBadRequest, fieldProblem("entries", err.Error()))
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "status page not found")
	default:
		s.log.Error(action+" status page", "page_id", pageID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not "+action+" the status page")
	}
	return true
}

// statusPageFromPath looks up the page named in the path. It writes the 404
// or 500 itself and reports false when there is no page to work on.
func (s *Server) statusPageFromPath(w http.ResponseWriter, r *http.Request) (store.StatusPage, bool) {
	p, err := s.db.GetStatusPageBySlug(r.Context(), r.PathValue("slug"))
	if s.writeStatusPageError(w, err, "read", 0) {
		return store.StatusPage{}, false
	}
	return p, true
}

func decodeStatusPage(w http.ResponseWriter, r *http.Request) (store.StatusPage, bool) {
	var req statusPageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return store.StatusPage{}, false
	}
	return store.StatusPage{
		Slug: req.Slug, Title: req.Title, Description: req.Description, Timezone: req.Timezone,
		Selection: req.Selection, TagKey: req.TagKey, TagValue: req.TagValue,
		Indexable: req.Indexable, Enabled: req.Enabled,
	}, true
}

func (s *Server) handleListStatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.db.ListStatusPages(r.Context())
	if err != nil {
		s.log.Error("list status pages", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list status pages")
		return
	}
	out := make([]statusPageResponse, 0, len(pages))
	for _, p := range pages {
		view, err := s.statusPageView(r, p)
		if err != nil {
			s.log.Error("read status page entries", "page_id", p.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "could not list status pages")
			return
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": out})
}

func (s *Server) handleCreateStatusPage(w http.ResponseWriter, r *http.Request) {
	p, ok := decodeStatusPage(w, r)
	if !ok {
		return
	}
	created, err := s.db.CreateStatusPage(r.Context(), p)
	if s.writeStatusPageError(w, err, "create", 0) {
		return
	}
	s.log.Info("status page created", "page_id", created.ID, "slug", created.Slug, "enabled", created.Enabled)
	s.writeStatusPage(w, r, http.StatusCreated, created)
}

func (s *Server) handleUpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	current, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	p, ok := decodeStatusPage(w, r)
	if !ok {
		return
	}
	p.ID = current.ID
	updated, err := s.db.UpdateStatusPage(r.Context(), p)
	if s.writeStatusPageError(w, err, "update", current.ID) {
		return
	}
	s.log.Info("status page updated", "page_id", updated.ID, "slug", updated.Slug, "enabled", updated.Enabled)
	s.writeStatusPage(w, r, http.StatusOK, updated)
}

func (s *Server) handleDeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	if s.writeStatusPageError(w, s.db.DeleteStatusPage(r.Context(), p.ID), "delete", p.ID) {
		return
	}
	s.log.Info("status page deleted", "page_id", p.ID, "slug", p.Slug)
	w.WriteHeader(http.StatusNoContent)
}

// handleSetStatusPageEntries makes the page list exactly the monitors given,
// in that order. A monitor that stays keeps its public key.
func (s *Server) handleSetStatusPageEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := s.statusPageFromPath(w, r)
	if !ok {
		return
	}
	var req statusPageEntriesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Entries == nil {
		// An omitted list and an empty one would otherwise both clear the
		// page; only the explicit one may.
		writeProblem(w, http.StatusBadRequest, fieldProblem("entries", "entries is required; send [] to remove every monitor"))
		return
	}
	in := make([]store.StatusPageEntryInput, len(req.Entries))
	for i, e := range req.Entries {
		in[i] = store.StatusPageEntryInput{MonitorID: e.MonitorID, DisplayName: e.DisplayName}
	}
	if _, err := s.db.SetStatusPageEntries(r.Context(), p.ID, in); s.writeStatusPageError(w, err, "update", p.ID) {
		return
	}
	s.log.Info("status page entries set", "page_id", p.ID, "slug", p.Slug, "entries", len(in))
	// Read the page again: setting entries moves its updated_at.
	fresh, err := s.db.GetStatusPage(r.Context(), p.ID)
	if s.writeStatusPageError(w, err, "read", p.ID) {
		return
	}
	s.writeStatusPage(w, r, http.StatusOK, fresh)
}
