package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// previewCooldown is the minimum spacing between preview checks per user.
//
// The manual-check cooldown is keyed by monitor id, which a preview does not
// have: nothing is saved, so there is no id to key on. Without a limit of its
// own this endpoint would be the one authenticated way to ask the server to
// make an arbitrary outbound request as fast as a loop can post, which is a
// worse version of exactly what manualCheckCooldown exists to prevent.
//
// Three seconds rather than five: a preview is something a human presses while
// correcting a typo in a form, and the whole promise of the endpoint is that
// the feedback is immediate. It is still far slower than a script needs to be
// useful as an amplifier.
const previewCooldown = 3 * time.Second

// previewRequest is the wire shape of a preview check.
//
// It mirrors createMonitorRequest minus everything that only matters once a
// monitor is stored: there is no name, no interval and no enabled flag,
// because nothing is scheduled and nothing is written.
type previewRequest struct {
	Type            string            `json:"type"`
	Target          string            `json:"target"`
	TimeoutS        int               `json:"timeout_s"`
	Method          string            `json:"method"`
	ExpectedStatus  string            `json:"expected_status"`
	Keyword         string            `json:"keyword"`
	KeywordMode     string            `json:"keyword_mode"`
	FollowRedirects *bool             `json:"follow_redirects"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	SSLWarnDays     *int              `json:"ssl_warn_days"`
	// Tags do not affect a probe — nothing is stored and nothing is
	// grouped. They are accepted and validated anyway so that a form can
	// preview the draft it holds without stripping fields first: the
	// decoder rejects unknown fields, so an ignored `tags` would be a 400,
	// and a tag that previews fine but fails to save is exactly the outcome
	// this endpoint exists to prevent.
	Tags map[string]string `json:"tags"`
}

// previewResponse is a check result plus the settings it was run with.
//
// Echoing the resolved type and target is the point of the endpoint as much as
// the result is. Someone who pasted `example.com` needs to see that it was
// read as an HTTPS monitor of `https://example.com` *before* they save, not
// discover it afterwards on a detail screen — and the form can fill its own
// fields from this rather than reimplementing the inference in TypeScript.
type previewResponse struct {
	OK        bool      `json:"ok"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMS int       `json:"latency_ms"`

	StatusCode int    `json:"status_code,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Error      string `json:"error,omitempty"`

	CertExpiry *time.Time `json:"cert_expiry,omitempty"`

	// Type and Target are what the probe actually used, after inference and
	// normalisation. They may differ from what was sent.
	Type   string `json:"type"`
	Target string `json:"target"`

	// Tags echoes the normalised tags, so a form can see that `Env: Prod `
	// will be stored as `env` -> `Prod` before it saves.
	Tags map[string]string `json:"tags,omitempty"`
}

// handlePreviewCheck probes a monitor that does not exist yet.
//
// Product principle 2 is sixty seconds from nothing to a working monitor, and
// the thing that decides whether that promise holds is not the form, it is
// whether you can tell the target is reachable *before* you commit. Without
// this, the only way to find out that a URL was wrong is to save it, wait for
// the first scheduled check, and then come back and edit — three screens and a
// minute of waiting for a missing `https://`.
//
// Nothing is written. The monitor handed to the prober has no id and is not
// enabled, which is the same path a paused monitor takes: CheckNow probes it
// and records nothing. That is deliberate rather than incidental — a preview
// that left heartbeats behind would put history under a monitor the user may
// never create.
func (s *Server) handlePreviewCheck(w http.ResponseWriter, r *http.Request) {
	if s.prober == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this instance runs without a checker, so preview checks are unavailable")
		return
	}

	var req previewRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// A second JSON value in the same body is a client bug, and silently
	// probing only the first target hides it. `{"target":"a"}{"target":"b"}`
	// used to preview "a" and report success for a request the caller did not
	// make.
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON: expected exactly one JSON object")
		return
	}

	typ, target, p := resolveTarget(req.Type, req.Target)
	if !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	if req.TimeoutS != 0 && (req.TimeoutS < 1 || req.TimeoutS > 120) {
		writeProblem(w, http.StatusBadRequest,
			fieldProblem("timeout_s", "timeout_s must be between 1 and 120"))
		return
	}
	if p := validateKeywordMode(req.KeywordMode); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	// The same validators creation runs (monitors.go). Preview exists to tell
	// you whether a monitor will work before you save it, so a request that
	// previews green and then fails to save is the one outcome it may not
	// produce: an unsupported method reached http.NewRequestWithContext, and
	// an out-of-range ssl_warn_days passed here and hit a SQLite constraint
	// on create.
	method, p := normaliseMethod(req.Method)
	if !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	if p := validateSSLWarnDays(req.SSLWarnDays); !p.ok() {
		writeProblem(w, http.StatusBadRequest, p)
		return
	}
	tags, err := store.NormaliseTags(req.Tags)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, fieldProblem("tags", err.Error()))
		return
	}

	// Keyed by user, because there is no monitor to key on. An unauthenticated
	// request never reaches here — the route is accessWrite — so the lookup
	// failing at all would mean the guard was removed, and refusing is the
	// only safe reading of that.
	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if wait, free := s.previewChecks.reserve(user.ID, time.Now(), previewCooldown); !free {
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests,
			"a preview check ran moments ago; try again in a few seconds")
		return
	}

	m := store.Monitor{
		// No name: nothing that reads this result cares, and inventing one
		// would put a placeholder into the checker's log lines.
		Type:            typ,
		Target:          target,
		TimeoutS:        req.TimeoutS,
		Method:          method,
		ExpectedStatus:  req.ExpectedStatus,
		Keyword:         req.Keyword,
		KeywordMode:     req.KeywordMode,
		FollowRedirects: true,
		Headers:         req.Headers,
		Body:            req.Body,
		// One attempt. Retries are a rule about when a failure becomes an
		// incident, and a preview does not open incidents — repeating a probe
		// that has already answered the user's question would only make the
		// button feel slow.
		Retries: 1,
		Enabled: false,
	}
	if req.FollowRedirects != nil {
		m.FollowRedirects = *req.FollowRedirects
	}
	if req.SSLWarnDays != nil {
		m.SSLWarnDays = *req.SSLWarnDays
	}
	// The same defaults the monitor would get once saved, so a preview that
	// passes is evidence about the monitor the user is about to create rather
	// than about a differently-configured probe.
	store.ApplyMonitorDefaults(&m)

	res, err := s.prober.CheckNow(r.Context(), m)
	if err != nil {
		s.log.Error("preview check failed to run", "type", typ, "error", err)
		writeError(w, http.StatusInternalServerError, "could not run the check")
		return
	}

	s.log.Info("preview check", "user_id", user.ID, "type", typ, "ok", res.OK)

	resp := previewResponse{
		OK:         res.OK,
		CheckedAt:  res.CheckedAt,
		LatencyMS:  int(res.Latency.Milliseconds()),
		StatusCode: res.StatusCode,
		Kind:       string(res.Kind),
		Error:      res.Error,
		Type:       typ,
		Target:     target,
		Tags:       tags,
	}
	if !res.CertExpiry.IsZero() {
		expiry := res.CertExpiry
		resp.CertExpiry = &expiry
	}
	if resp.CheckedAt.IsZero() {
		resp.CheckedAt = time.Now()
	}

	writeJSON(w, http.StatusOK, resp)
}

// resolveTarget works out which check type a target wants and tidies it up.
//
// The sixty-second promise starts with pasting a URL, and what people paste is
// `example.com`, `https://example.com/health`, or `db.internal:5432` — not a
// type field they have already chosen from a dropdown. Guessing here is not a
// convenience feature: making the user classify their own input before the
// product will look at it is most of the minute.
//
// It stays conservative. An explicit type is always obeyed, and an input that
// does not clearly say what it is becomes an error rather than a guess, so the
// inference can never quietly monitor something other than what was meant.
func resolveTarget(typ, target string) (resolvedType, resolvedTarget string, bad problem) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fieldProblem("target", "target is required")
	}

	switch typ {
	case "":
		typ = inferType(target)
		if typ == "" {
			// Blamed on the target, not the type: the caller sent no type,
			// and the input they can actually correct is what they pasted.
			return "", "", fieldProblem("target", "could not tell what to check from "+strconv.Quote(target)+
				"; give a URL like https://example.com, a host:port like db.example.com:5432, "+
				"or set type explicitly")
		}
	case "http", "tcp", "ping", "ssl":
	default:
		return "", "", fieldProblem("type", "unknown type "+typ)
	}

	if typ == "http" && !strings.Contains(target, "://") {
		// A bare hostname was read as HTTP. https, not http: defaulting to
		// the unencrypted scheme would silently monitor a redirect on most
		// of the web, and quietly teach the habit on the rest.
		target = "https://" + target
	}

	if p := validateTargetForType(typ, target); !p.ok() {
		return "", "", p
	}
	return typ, target, problem{}
}

// inferType classifies a target that arrived without a type.
//
// Ordered from most to least certain: a scheme is a statement, a port is
// nearly one, and a bare hostname is the common case the dashboard exists for.
func inferType(target string) string {
	if u, err := url.Parse(target); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return "http"
	}
	if strings.Contains(target, "://") {
		// Some other scheme — postgres://, redis://. We cannot speak it, and
		// pretending it is a hostname would monitor something absurd.
		return ""
	}
	if _, port, err := checker.ParseHostPort(target, 0); err == nil && port != 0 {
		return "tcp"
	}
	if strings.ContainsAny(target, " /?#") {
		return ""
	}
	return "http"
}
