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

	// Heartbeats is filled in only when the caller asked for it with the
	// `heartbeats` query parameter, oldest first. Omitting the field
	// entirely when it was not requested keeps the default response byte
	// for byte what it has always been.
	Heartbeats []heartbeatResponse `json:"heartbeats,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// heartbeatResponse is the wire shape of one recorded check result. It is
// shared by the per-monitor heartbeat listing and the optional beats embedded
// in a monitor listing, so both cannot drift apart.
type heartbeatResponse struct {
	TS         time.Time `json:"ts"`
	OK         bool      `json:"ok"`
	LatencyMS  int       `json:"latency_ms"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// describeHeartbeat converts a stored heartbeat to its wire shape.
func describeHeartbeat(hb store.Heartbeat) heartbeatResponse {
	return heartbeatResponse{
		TS:         hb.TS,
		OK:         hb.OK,
		LatencyMS:  hb.LatencyMS,
		StatusCode: hb.StatusCode,
		Error:      hb.Error,
	}
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

	// `heartbeats` is opt-in so the existing response shape is untouched for
	// callers that do not want the extra payload.
	perMonitor := 0
	if v := r.URL.Query().Get("heartbeats"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "heartbeats must be between 1 and 1000")
			return
		}
		perMonitor = n
	}

	monitors, err := s.db.ListMonitors(ctx)
	if err != nil {
		s.log.Error("list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list monitors")
		return
	}

	// One bulk query rather than one per monitor: at a few hundred monitors
	// the per-monitor version is a few hundred round trips per page load.
	// A failure here only costs the beat bars, so the listing still goes out.
	var beats map[int64][]store.Heartbeat
	if perMonitor > 0 {
		beats, err = s.db.RecentHeartbeatsForAll(ctx, perMonitor)
		if err != nil {
			s.log.Error("recent heartbeats for all monitors", "error", err)
			beats = nil
		}
	}

	out := make([]monitorResponse, 0, len(monitors))
	for _, m := range monitors {
		resp := s.describeMonitor(r, m)
		if perMonitor > 0 {
			resp.Heartbeats = oldestFirstHeartbeats(beats[m.ID])
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": out})
}

// oldestFirstHeartbeats reverses a newest-first run of heartbeats.
//
// The store returns newest first, which is the natural order for "show me the
// last N". The dashboard's beat bar reads left to right through time, so it
// needs the opposite. Reversing once here keeps both contracts honest instead
// of making one of them lie about its order.
//
// A monitor with no heartbeats yields nil, so `omitempty` leaves the field out
// entirely rather than shipping an empty array.
func oldestFirstHeartbeats(hbs []store.Heartbeat) []heartbeatResponse {
	if len(hbs) == 0 {
		return nil
	}
	out := make([]heartbeatResponse, 0, len(hbs))
	for i := len(hbs) - 1; i >= 0; i-- {
		out = append(out, describeHeartbeat(hbs[i]))
	}
	return out
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var req createMonitorRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	// Reject fields the API does not know.
	//
	// Silently ignoring them is a trap: a client that sends interval_seconds
	// instead of interval_s gets a 201, believes it configured a 5-second
	// check, and receives the default instead. The failure surfaces much
	// later as "why is this monitor so slow" — a typo should be an error at
	// the moment it is made.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if msg := validateCreateMonitor(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	method, _ := normaliseMethod(req.Method)

	m := store.Monitor{
		Name:      req.Name,
		Type:      req.Type,
		Target:    req.Target,
		IntervalS: req.IntervalS,
		TimeoutS:  req.TimeoutS,
		// Already validated above; the error is discarded because a second
		// failure here would be unreachable.
		Method:          method,
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
	// Creation used to skip these three entirely, so a bad method or an
	// out-of-range ssl_warn_days only surfaced as a SQLite constraint error or,
	// worse, as a monitor that failed every check forever.
	if _, msg := normaliseMethod(req.Method); msg != "" {
		return msg
	}
	if msg := validateKeywordMode(req.KeywordMode); msg != "" {
		return msg
	}
	if msg := validateSSLWarnDays(req.SSLWarnDays); msg != "" {
		return msg
	}
	if msg := validateTargetForType(req.Type, req.Target); msg != "" {
		return msg
	}
	return ""
}

// monitorHTTPMethods is the set an http monitor may use, and the single place
// the list is written down. (Not `httpMethods`: openapi_test.go already owns
// that name for the operation keys in a spec path item.) Anything outside it reaches http.NewRequestWithContext
// and fails there, far from the field that caused it.
var monitorHTTPMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true,
	"PATCH": true, "DELETE": true, "OPTIONS": true,
}

// normaliseMethod upper-cases a method and rejects anything unsupported.
//
// An empty method is left empty rather than defaulted here: the store decides
// what an unset method becomes, and duplicating that choice in the API is how
// the two drift apart.
func normaliseMethod(method string) (string, string) {
	if method == "" {
		return "", ""
	}
	upper := strings.ToUpper(method)
	if !monitorHTTPMethods[upper] {
		return "", "unsupported HTTP method " + method
	}
	return upper, ""
}

// validateSSLWarnDays applies the one range the column allows.
//
// 0 is rejected rather than accepted as "never warn", because
// store.ApplyMonitorDefaults would turn it back into 14 and the client would
// be told it had disabled a warning it still gets.
func validateSSLWarnDays(days *int) string {
	if days == nil {
		return ""
	}
	if *days < 1 || *days > 365 {
		return "ssl_warn_days must be between 1 and 365"
	}
	return ""
}

// validateKeywordMode rejects modes the checker cannot act on.
func validateKeywordMode(mode string) string {
	switch mode {
	case "", "absent_ok", "must_contain", "must_not_contain":
		return ""
	default:
		return "unknown keyword_mode " + mode
	}
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
	setMonitorETag(w, m)
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
	s.manualChecks.forget(id)
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
	if !s.requireMonitor(w, r, id) {
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

	out := make([]heartbeatResponse, 0, len(hbs))
	for _, hb := range hbs {
		out = append(out, describeHeartbeat(hb))
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

// requireMonitor confirms a monitor exists before a sub-resource is read.
//
// Without it, an unknown id returns a tidy empty list, which reads as "this
// monitor has no data yet" rather than "this monitor does not exist". A client
// polling a deleted monitor would keep getting cheerful 200s and never learn it
// is querying a ghost. Absence and emptiness are different facts, so they get
// different answers.
//
// A lookup failure that is not "no rows" is a 500, not a 404: claiming the
// monitor is gone because the database hiccuped would be the same confident lie
// in a different costume.
func (s *Server) requireMonitor(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, err := s.db.GetMonitor(r.Context(), id)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "monitor not found")
		return false
	default:
		s.log.Error("get monitor", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return false
	}
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

// patchMonitorRequest is a partial update: every field is a pointer so that
// "not sent" and "sent as the zero value" stay distinguishable.
//
// That distinction is the whole point of PATCH. With plain values, sending
// {"name":"x"} would also read as interval_s=0 and enabled=false, and a rename
// would silently pause the monitor and reset its schedule.
type patchMonitorRequest struct {
	Name            *string            `json:"name"`
	Type            *string            `json:"type"`
	Target          *string            `json:"target"`
	IntervalS       *int               `json:"interval_s"`
	TimeoutS        *int               `json:"timeout_s"`
	Retries         *int               `json:"retries"`
	Method          *string            `json:"method"`
	ExpectedStatus  *string            `json:"expected_status"`
	Keyword         *string            `json:"keyword"`
	KeywordMode     *string            `json:"keyword_mode"`
	FollowRedirects *bool              `json:"follow_redirects"`
	Headers         *map[string]string `json:"headers"`
	Body            *string            `json:"body"`
	SSLWarnDays     *int               `json:"ssl_warn_days"`
	Enabled         *bool              `json:"enabled"`
}

// handlePatchMonitor applies a partial update to an existing monitor.
//
// Until now the only way to change a monitor was to delete and recreate it,
// which throws away its heartbeats, its uptime history and any open incident.
// Correcting a typo in a URL should not erase the record of last week's
// outage.
//
// An If-Match header makes the write conditional on the monitor not having
// changed since the client read it. Without one the write stays
// last-write-wins, because making the header mandatory would break every
// script written before it existed.
func (s *Server) handlePatchMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Parse the precondition before the body: a malformed If-Match is a
	// client error regardless of what it was trying to send.
	ifMatch := r.Header.Get("If-Match")
	var (
		wantTags []string
		wantAny  bool
	)
	if ifMatch != "" {
		var valid bool
		wantTags, wantAny, valid = parseIfMatch(ifMatch)
		if !valid {
			writeError(w, http.StatusBadRequest, "malformed If-Match header")
			return
		}
	}

	var req patchMonitorRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	ctx := r.Context()

	m, err := s.db.GetMonitor(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		// 404 rather than 412, even for a conditional request. RFC 9110 would
		// allow either, and a monitor that never existed is more usefully
		// described as missing than as a stale version: 412 tells the client to
		// re-fetch and retry, which would loop forever on an id that is simply
		// wrong. The genuine race — deleted between this read and the write
		// below — is answered as a conflict, where re-fetching is the right
		// advice.
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.log.Error("get monitor for patch", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not load monitor")
		return
	}

	// A conditional request is decided by the UPDATE itself, so nothing is
	// compared here. `*` asks only that the monitor exist, which the
	// successful load above already established, so it needs no version
	// predicate and falls through to the unconditional write.
	conditional := ifMatch != "" && !wantAny

	if msg := applyMonitorPatch(&m, req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	var updated store.Monitor
	if conditional {
		updated, err = s.db.UpdateMonitorIfUnchanged(ctx, m, ifMatchVersions(wantTags))
	} else {
		updated, err = s.db.UpdateMonitor(ctx, m)
	}
	switch {
	case err == nil:
	case errors.Is(err, store.ErrVersionConflict):
		writeConflict(w, id)
		return
	case errors.Is(err, sql.ErrNoRows):
		// Deleted between the read and the write. For `If-Match: *` that is a
		// failed precondition, not a missing resource: the client asked us to
		// write only if the monitor still existed, and by the time we wrote it
		// did not. 404 would describe the row; 412 answers the question that
		// was actually asked.
		if wantAny {
			writeConflict(w, id)
			return
		}
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	default:
		s.log.Error("update monitor", "id", id, "error", err)
		writeError(w, http.StatusBadRequest, "could not update monitor: "+err.Error())
		return
	}

	// The response carries the new version so a client editing repeatedly can
	// keep going without re-fetching — without it, every conditional PATCH
	// would have to be followed by a GET just to learn the next validator.
	setMonitorETag(w, updated)

	s.log.Info("monitor updated", "id", updated.ID, "name", updated.Name, "target", updated.Target)
	writeJSON(w, http.StatusOK, s.describeMonitor(r, updated))
}

// writeConflict reports that the monitor moved on since the caller read it.
//
// No fresh ETag is returned with it: the client has to look at what the other
// editor wrote before deciding whether its own change still makes sense, and
// handing over a validator it could blindly retry with would invite exactly
// the overwrite this endpoint refused.
func writeConflict(w http.ResponseWriter, id int64) {
	writeError(w, http.StatusPreconditionFailed,
		"monitor "+strconv.FormatInt(id, 10)+" was modified by someone else; "+
			"fetch it again and reapply your change")
}

// applyMonitorPatch merges the request into m and validates the result. It
// returns an error message, or "" when the merged monitor is valid.
//
// Validation runs on the merged monitor, not on the request. Changing only the
// type of an existing monitor can invalidate a target that was never touched,
// and that combination has to be rejected just as firmly as a bad create.
func applyMonitorPatch(m *store.Monitor, req patchMonitorRequest) string {
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return "name cannot be empty"
		}
		m.Name = *req.Name
	}
	if req.Type != nil {
		switch *req.Type {
		case "http", "tcp", "ping", "ssl":
			m.Type = *req.Type
		default:
			return "unknown type " + *req.Type
		}
	}
	if req.Target != nil {
		if strings.TrimSpace(*req.Target) == "" {
			return "target cannot be empty"
		}
		m.Target = *req.Target
	}

	// Unlike create, an explicit 0 here is a mistake rather than "use the
	// default": the client asked for a 0-second interval on a monitor that
	// already has a working one.
	if req.IntervalS != nil {
		if *req.IntervalS < 20 || *req.IntervalS > 86400 {
			return "interval_s must be between 20 and 86400"
		}
		m.IntervalS = *req.IntervalS
	}
	if req.TimeoutS != nil {
		if *req.TimeoutS < 1 || *req.TimeoutS > 120 {
			return "timeout_s must be between 1 and 120"
		}
		m.TimeoutS = *req.TimeoutS
	}
	if req.Retries != nil {
		if *req.Retries < 0 || *req.Retries > 10 {
			return "retries must be between 0 and 10"
		}
		m.Retries = *req.Retries
	}
	if req.Method != nil {
		method, msg := normaliseMethod(*req.Method)
		if msg != "" {
			return msg
		}
		m.Method = method
	}
	if req.ExpectedStatus != nil {
		m.ExpectedStatus = *req.ExpectedStatus
	}
	if req.Keyword != nil {
		m.Keyword = *req.Keyword
	}
	if req.KeywordMode != nil {
		if msg := validateKeywordMode(*req.KeywordMode); msg != "" {
			return msg
		}
		m.KeywordMode = *req.KeywordMode
	}
	if req.FollowRedirects != nil {
		m.FollowRedirects = *req.FollowRedirects
	}
	if req.Headers != nil {
		m.Headers = *req.Headers
	}
	if req.Body != nil {
		m.Body = *req.Body
	}
	if req.SSLWarnDays != nil {
		if msg := validateSSLWarnDays(req.SSLWarnDays); msg != "" {
			return msg
		}
		m.SSLWarnDays = *req.SSLWarnDays
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}

	// Re-validate whenever either half of the pair moved. The target the
	// monitor ends up with is what the checker will dial, so that is what has
	// to be legal — the SSRF guard itself runs at dial time, in the checker.
	if req.Target != nil || req.Type != nil {
		if msg := validateTargetForType(m.Type, m.Target); msg != "" {
			return msg
		}
	}
	return ""
}
