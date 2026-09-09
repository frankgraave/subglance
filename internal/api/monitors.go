package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// monitorResponse is the API shape of a monitor plus its current state.
//
// It deliberately differs from store.Monitor: the wire format is a contract we
// have to keep stable, while the storage shape must stay free to change.
type monitorResponse struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`

	IntervalS int  `json:"interval_s"`
	TimeoutS  int  `json:"timeout_s"`
	Enabled   bool `json:"enabled"`

	Status     string     `json:"status"` // up, pending, or down
	LastCheck  *time.Time `json:"last_check,omitempty"`
	LatencyMS  int        `json:"latency_ms,omitempty"`
	StatusCode int        `json:"status_code,omitempty"`
	Error      string     `json:"error,omitempty"`

	// IncidentID and IncidentSince describe the open incident, if any. They
	// let the UI link straight from a red row to the incident without a
	// second round trip.
	IncidentID    int64      `json:"incident_id,omitempty"`
	IncidentSince *time.Time `json:"incident_since,omitempty"`

	Uptime24h float64 `json:"uptime_24h"`

	CreatedAt time.Time `json:"created_at"`
}

type createMonitorRequest struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Target          string            `json:"target"`
	IntervalS       int               `json:"interval_s"`
	TimeoutS        int               `json:"timeout_s"`
	Retries         *int              `json:"retries"`
	Method          string            `json:"method"`
	ExpectedStatus  string            `json:"expected_status"`
	Keyword         string            `json:"keyword"`
	KeywordMode     string            `json:"keyword_mode"`
	FollowRedirects *bool             `json:"follow_redirects"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	SSLWarnDays     *int              `json:"ssl_warn_days"`
	Enabled         *bool             `json:"enabled"`
}

func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	monitors, err := s.db.ListMonitors(ctx)
	if err != nil {
		s.log.Error("list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list monitors")
		return
	}

	out := make([]monitorResponse, 0, len(monitors))
	for _, m := range monitors {
		out = append(out, s.describeMonitor(r, m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": out})
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var req createMonitorRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if msg := validateCreateMonitor(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	m := store.Monitor{
		Name:            req.Name,
		Type:            req.Type,
		Target:          req.Target,
		IntervalS:       req.IntervalS,
		TimeoutS:        req.TimeoutS,
		Method:          req.Method,
		ExpectedStatus:  req.ExpectedStatus,
		Keyword:         req.Keyword,
		KeywordMode:     req.KeywordMode,
		FollowRedirects: true,
		Headers:         req.Headers,
		Body:            req.Body,
		Enabled:         true,
	}
	if req.Retries != nil {
		m.Retries = *req.Retries
	} else {
		m.Retries = 2
	}
	if req.FollowRedirects != nil {
		m.FollowRedirects = *req.FollowRedirects
	}
	if req.SSLWarnDays != nil {
		m.SSLWarnDays = *req.SSLWarnDays
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}

	created, err := s.db.CreateMonitor(r.Context(), m)
	if err != nil {
		s.log.Error("create monitor", "error", err)
		writeError(w, http.StatusBadRequest, "could not create monitor: "+err.Error())
		return
	}

	s.log.Info("monitor created", "id", created.ID, "name", created.Name, "target", created.Target)
	writeJSON(w, http.StatusCreated, s.describeMonitor(r, created))
}

func validateCreateMonitor(req createMonitorRequest) string {
	if req.Name == "" {
		return "name is required"
	}
	if req.Target == "" {
		return "target is required"
	}
	switch req.Type {
	case "http", "tcp", "ping", "ssl":
	case "":
		return "type is required"
	default:
		return "unknown type " + req.Type
	}
	if req.IntervalS != 0 && (req.IntervalS < 20 || req.IntervalS > 86400) {
		return "interval_s must be between 20 and 86400"
	}
	if req.TimeoutS != 0 && (req.TimeoutS < 1 || req.TimeoutS > 120) {
		return "timeout_s must be between 1 and 120"
	}
	if msg := validateTargetForType(req.Type, req.Target); msg != "" {
		return msg
	}
	return ""
}

// validateTargetForType rejects target shapes that cannot work for a type.
//
// Catching this at creation beats letting the monitor fail forever with an
// internal error: a mistake made while typing should be corrected while the
// user is still looking at the form.
func validateTargetForType(typ, target string) string {
	switch typ {
	case "http":
		u, err := url.Parse(target)
		if err != nil {
			return "target is not a valid URL: " + err.Error()
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "an http monitor needs a target starting with http:// or https://"
		}
		if u.Host == "" {
			return "target has no host"
		}

	case "tcp":
		host, port, err := checker.ParseHostPort(target, 0)
		if err != nil {
			return "invalid target: " + err.Error()
		}
		if host == "" {
			return "target has no host"
		}
		if port == 0 {
			return "a tcp monitor needs a port, for example db.example.com:5432"
		}

	case "ssl":
		host, _, err := checker.ParseHostPort(target, 443)
		if err != nil {
			return "invalid target: " + err.Error()
		}
		if host == "" {
			return "target has no host"
		}

	case "ping":
		// ICMP has no ports. A URL or a host:port here means the user picked
		// the wrong check type, so say which one they wanted rather than
		// complaining about a port they may not have realised they typed.
		if strings.Contains(target, "://") {
			return "a ping monitor takes a hostname or IP address, not a URL — " +
				"use " + hostOnly(target) + ", or an http monitor for the full URL"
		}
		if _, port, err := checker.ParseHostPort(target, 0); err == nil && port != 0 {
			return "a ping monitor cannot use a port; use a tcp monitor instead"
		}
	}
	return ""
}

// hostOnly extracts the host from a URL-shaped target, for use in error
// messages that suggest a correction.
func hostOnly(target string) string {
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return target
}

func (s *Server) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	m, err := s.db.GetMonitor(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.log.Error("get monitor", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}
	writeJSON(w, http.StatusOK, s.describeMonitor(r, m))
}

func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := s.db.DeleteMonitor(r.Context(), id); err != nil {
		s.log.Error("delete monitor", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete monitor")
		return
	}
	s.log.Info("monitor deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePauseMonitor(w http.ResponseWriter, r *http.Request) {
	s.setEnabled(w, r, false)
}

func (s *Server) handleResumeMonitor(w http.ResponseWriter, r *http.Request) {
	s.setEnabled(w, r, true)
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := s.db.SetMonitorEnabled(r.Context(), id, enabled); err != nil {
		s.log.Error("set monitor enabled", "id", id, "enabled", enabled, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update monitor")
		return
	}

	m, err := s.db.GetMonitor(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}
	writeJSON(w, http.StatusOK, s.describeMonitor(r, m))
}

func (s *Server) handleListHeartbeats(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return
		}
		limit = n
	}

	hbs, err := s.db.ListHeartbeats(r.Context(), id, limit)
	if err != nil {
		s.log.Error("list heartbeats", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load heartbeats")
		return
	}

	type heartbeatResponse struct {
		TS         time.Time `json:"ts"`
		OK         bool      `json:"ok"`
		LatencyMS  int       `json:"latency_ms"`
		StatusCode int       `json:"status_code,omitempty"`
		Error      string    `json:"error,omitempty"`
	}

	out := make([]heartbeatResponse, 0, len(hbs))
	for _, hb := range hbs {
		out = append(out, heartbeatResponse{
			TS: hb.TS, OK: hb.OK, LatencyMS: hb.LatencyMS,
			StatusCode: hb.StatusCode, Error: hb.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"heartbeats": out})
}

// describeMonitor enriches a stored monitor with its current state.
//
// Failures to load that state are logged but not fatal: a dashboard that shows
// monitors with unknown status beats a dashboard that shows nothing.
func (s *Server) describeMonitor(r *http.Request, m store.Monitor) monitorResponse {
	resp := monitorResponse{
		ID:        m.ID,
		Name:      m.Name,
		Type:      m.Type,
		Target:    m.Target,
		IntervalS: m.IntervalS,
		TimeoutS:  m.TimeoutS,
		Enabled:   m.Enabled,
		Status:    "pending",
		CreatedAt: m.CreatedAt,
	}

	ctx := r.Context()

	hb, err := s.db.LatestHeartbeat(ctx, m.ID)
	switch {
	case err == nil:
		// The last heartbeat says what the last probe saw; it does not say
		// whether the monitor is down. A single failed check is a blip until
		// the failure threshold is crossed, so the reported status comes from
		// the incident record — the same source the alerting uses. Deriving
		// it from the heartbeat alone would put the dashboard and the
		// notifications into disagreement, which is worse than either being
		// slightly stale.
		resp.Status = "up"
		if !hb.OK {
			resp.Status = "pending"
		}
		ts := hb.TS
		resp.LastCheck = &ts
		resp.LatencyMS = hb.LatencyMS
		resp.StatusCode = hb.StatusCode
		resp.Error = hb.Error
	case errors.Is(err, sql.ErrNoRows):
		// Never checked yet.
		resp.Status = "pending"
	default:
		s.log.Error("latest heartbeat", "monitor_id", m.ID, "error", err)
		resp.Status = "pending"
	}

	// A confirmed open incident is what "down" means.
	inc, err := s.db.OpenIncidentFor(ctx, m.ID)
	switch {
	case err == nil:
		resp.IncidentID = inc.ID
		resp.IncidentSince = &inc.StartedAt
		if inc.Confirmed() {
			resp.Status = "down"
		}
		if resp.Error == "" {
			resp.Error = inc.LastError
		}
	case errors.Is(err, store.ErrNoOpenIncident):
		// Nothing wrong.
	default:
		s.log.Error("open incident", "monitor_id", m.ID, "error", err)
	}

	if stats, err := s.db.Uptime(ctx, m.ID, 24*time.Hour); err == nil {
		resp.Uptime24h = stats.Percentage
	} else {
		s.log.Error("uptime", "monitor_id", m.ID, "error", err)
	}

	return resp
}

// pathID parses the {id} path segment, writing an error response when invalid.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return 0, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
