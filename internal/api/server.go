// Package api serves the SubGlance HTTP API and, in production builds, the
// embedded web UI.
//
// Product principle 4: the UI is a client of this API and
// gets no private endpoints. Anything the dashboard can do, a script can do.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/buildinfo"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/store"
)

// Server wires the HTTP routes together.
type Server struct {
	log       *slog.Logger
	db        *store.DB
	startedAt time.Time

	// bus carries live check results to streaming clients. Nil disables the
	// stream endpoint rather than crashing it, so the API stays usable in
	// tests and in any deployment that runs without a checker pipeline.
	bus *events.Bus

	// prober runs on-demand checks. Nil disables that endpoint, for the same
	// reason as bus: an API without a checker behind it should still serve
	// everything else.
	prober Prober

	// manualChecks rate-limits POST /monitors/{id}/check per monitor.
	manualChecks cooldown
}

// New returns a Server ready to be mounted.
func New(log *slog.Logger, db *store.DB) *Server {
	return &Server{log: log, db: db, startedAt: time.Now()}
}

// WithBus attaches an event bus, enabling GET /api/v1/stream.
func (s *Server) WithBus(b *events.Bus) *Server {
	s.bus = b
	return s
}

// WithProber attaches a prober, enabling POST /api/v1/monitors/{id}/check.
func (s *Server) WithProber(p Prober) *Server {
	s.prober = p
	return s
}

// access says what a route requires from its caller.
//
// The zero value is deliberately not "public": a route added without an
// explicit access level must fail closed, not silently expose itself.
type access int

const (
	// accessPublic is reachable without credentials — health, readiness,
	// setup and login.
	accessPublic access = iota + 1

	// accessRead is any authenticated user, including viewers.
	accessRead

	// accessWrite is editors and admins.
	accessWrite

	// accessAdmin is administrators only.
	accessAdmin
)

// String renders the access level for error messages and for the OpenAPI
// drift test.
func (a access) String() string {
	switch a {
	case accessPublic:
		return "public"
	case accessRead:
		return "read"
	case accessWrite:
		return "write"
	case accessAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// route is one entry in the API surface.
type route struct {
	Method  string
	Pattern string // path only, e.g. "/api/v1/monitors/{id}"
	Access  access
}

// routes is the single source of truth for the API surface.
//
// It is a table rather than a series of registration calls so the surface can
// be inspected programmatically: docs/openapi.yaml is checked against this
// list by TestOpenAPIMatchesRoutes, which makes it impossible for a route and
// its documentation to drift apart unnoticed.
//
// Groups are ordered by what they require:
//
//	public   — health, readiness, setup and login
//	read     — any authenticated user, including viewers
//	write    — editors and admins
//	admin    — administrators only
//
// Everything that is not explicitly public requires authentication. That
// default matters: forgetting to guard a new route should fail closed, not
// silently expose it.
func (s *Server) routes() []route {
	return []route{
		// Public.
		{http.MethodGet, "/health", accessPublic},
		{http.MethodGet, "/api/v1/health", accessPublic},
		{http.MethodGet, "/api/v1/ready", accessPublic},
		{http.MethodGet, "/api/v1/setup", accessPublic},
		{http.MethodPost, "/api/v1/setup", accessPublic},
		{http.MethodPost, "/api/v1/auth/login", accessPublic},
		{http.MethodPost, "/api/v1/auth/logout", accessPublic},

		// Authenticated: any role.
		{http.MethodGet, "/api/v1/auth/me", accessRead},
		{http.MethodPost, "/api/v1/auth/password", accessRead},

		{http.MethodGet, "/api/v1/monitors", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/heartbeats", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/uptime", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/incidents", accessRead},
		{http.MethodGet, "/api/v1/incidents", accessRead},
		{http.MethodGet, "/api/v1/monitors/{id}/channels", accessRead},

		// Channel secrets are masked on read (see maskConfig), so a viewer may
		// see which targets exist without being handed the credentials to post
		// to them.
		{http.MethodGet, "/api/v1/channels", accessRead},
		{http.MethodGet, "/api/v1/channels/{id}", accessRead},

		// The live stream is a read: a viewer may watch, but watching is all it
		// does. It sits behind the same auth as everything else — an unguarded
		// stream would leak every monitor name and outage to anyone who can
		// reach the port.
		{http.MethodGet, "/api/v1/stream", accessRead},

		{http.MethodGet, "/api/v1/tokens", accessRead},
		{http.MethodPost, "/api/v1/tokens", accessRead},
		{http.MethodDelete, "/api/v1/tokens/{id}", accessRead},

		// Authenticated: editor or admin.
		{http.MethodPost, "/api/v1/monitors", accessWrite},
		{http.MethodPatch, "/api/v1/monitors/{id}", accessWrite},
		{http.MethodDelete, "/api/v1/monitors/{id}", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/check", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/pause", accessWrite},
		{http.MethodPost, "/api/v1/monitors/{id}/resume", accessWrite},
		{http.MethodPost, "/api/v1/incidents/{id}/ack", accessWrite},
		{http.MethodPut, "/api/v1/monitors/{id}/channels", accessWrite},

		{http.MethodPost, "/api/v1/channels", accessWrite},
		{http.MethodPut, "/api/v1/channels/{id}", accessWrite},
		{http.MethodDelete, "/api/v1/channels/{id}", accessWrite},

		// Authenticated: admin only.
		{http.MethodGet, "/api/v1/users", accessAdmin},
		{http.MethodPost, "/api/v1/users", accessAdmin},
		{http.MethodDelete, "/api/v1/users/{id}", accessAdmin},
	}
}

// handlerFor maps a route to the handler that serves it.
//
// Keeping this separate from routes() is what lets the drift test walk the
// surface without constructing handlers, and it makes an unrouted entry a
// startup panic instead of a silent 404 in production.
func (s *Server) handlerFor(rt route) http.HandlerFunc {
	switch rt.Method + " " + rt.Pattern {
	case "GET /health", "GET /api/v1/health":
		return s.handleHealth
	case "GET /api/v1/ready":
		return s.handleReady
	case "GET /api/v1/setup":
		return s.handleSetupStatus
	case "POST /api/v1/setup":
		return s.handleSetup
	case "POST /api/v1/auth/login":
		return s.handleLogin
	case "POST /api/v1/auth/logout":
		return s.handleLogout

	case "GET /api/v1/auth/me":
		return s.handleMe
	case "POST /api/v1/auth/password":
		return s.handleChangePassword

	case "GET /api/v1/monitors":
		return s.handleListMonitors
	case "GET /api/v1/monitors/{id}":
		return s.handleGetMonitor
	case "GET /api/v1/monitors/{id}/heartbeats":
		return s.handleListHeartbeats
	case "GET /api/v1/monitors/{id}/uptime":
		return s.handleMonitorUptime
	case "GET /api/v1/monitors/{id}/incidents":
		return s.handleListMonitorIncidents
	case "GET /api/v1/incidents":
		return s.handleListOpenIncidents
	case "GET /api/v1/monitors/{id}/channels":
		return s.handleListMonitorChannels

	case "GET /api/v1/channels":
		return s.handleListChannels
	case "GET /api/v1/channels/{id}":
		return s.handleGetChannel

	case "GET /api/v1/stream":
		return s.handleStream

	case "GET /api/v1/tokens":
		return s.handleListTokens
	case "POST /api/v1/tokens":
		return s.handleCreateToken
	case "DELETE /api/v1/tokens/{id}":
		return s.handleRevokeToken

	case "POST /api/v1/monitors":
		return s.handleCreateMonitor
	case "PATCH /api/v1/monitors/{id}":
		return s.handlePatchMonitor
	case "DELETE /api/v1/monitors/{id}":
		return s.handleDeleteMonitor
	case "POST /api/v1/monitors/{id}/check":
		return s.handleCheckMonitor
	case "POST /api/v1/monitors/{id}/pause":
		return s.handlePauseMonitor
	case "POST /api/v1/monitors/{id}/resume":
		return s.handleResumeMonitor
	case "POST /api/v1/incidents/{id}/ack":
		return s.handleAckIncident
	case "PUT /api/v1/monitors/{id}/channels":
		return s.handleSetMonitorChannels

	case "POST /api/v1/channels":
		return s.handleCreateChannel
	case "PUT /api/v1/channels/{id}":
		return s.handleUpdateChannel
	case "DELETE /api/v1/channels/{id}":
		return s.handleDeleteChannel

	case "GET /api/v1/users":
		return s.handleListUsers
	case "POST /api/v1/users":
		return s.handleCreateUser
	case "DELETE /api/v1/users/{id}":
		return s.handleDeleteUser
	}
	return nil
}

// Handler returns the root HTTP handler with all routes and middleware applied.
//
// It iterates routes() and wraps each handler in the guards its access level
// demands. An entry with no handler, or with an access level this function
// does not know, panics at construction time rather than being registered
// unguarded — the fail-closed default in executable form.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	requireWrite := s.requireRole(store.Role.CanWrite, "your role does not allow changes")
	requireAdmin := s.requireRole(store.Role.CanAdmin, "this action requires an administrator")

	for _, rt := range s.routes() {
		h := s.handlerFor(rt)
		if h == nil {
			panic("api: no handler for route " + rt.Method + " " + rt.Pattern)
		}

		pattern := rt.Method + " " + rt.Pattern
		switch rt.Access {
		case accessPublic:
			mux.Handle(pattern, h)
		case accessRead:
			mux.Handle(pattern, s.requireAuth(h))
		case accessWrite:
			mux.Handle(pattern, s.requireAuth(requireWrite(h)))
		case accessAdmin:
			mux.Handle(pattern, s.requireAuth(requireAdmin(h)))
		default:
			panic("api: unknown access level for route " + pattern)
		}
	}

	return s.withRecovery(s.withSecurityHeaders(s.withLogging(mux)))
}

// withSecurityHeaders sets defensive headers on every response.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		// The API returns only JSON, so the strictest possible policy applies.
		// The UI will need its own policy when it is served from here.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

// handleHealth reports whether SubGlance itself is alive.
//
// This is deliberately dependency-free: it must answer even when the database
// is unhappy, because an orchestrator uses it to decide whether to restart us.
// Dependency health belongs in a separate readiness endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: buildinfo.Short(),
		Uptime:  time.Since(s.startedAt).Truncate(time.Second).String(),
	})
}

type readyResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Error    string `json:"error,omitempty"`
}

// handleReady reports whether SubGlance can actually serve traffic, which
// unlike /health means checking its dependencies.
//
// The split matters operationally: /health answering 200 while /ready returns
// 503 tells an orchestrator "leave this process alone, but send it no traffic
// yet" — restarting it would not fix a database that is still coming up.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable", Database: "down", Error: "no database configured",
		})
		return
	}

	if err := s.db.Reader.PingContext(ctx); err != nil {
		s.log.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable", Database: "down", Error: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, readyResponse{Status: "ready", Database: "up"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written, so the response is beyond
		// saving; the connection will simply be cut short.
		return
	}
}

// statusRecorder captures the status code so the logging middleware can
// report it. http.ResponseWriter does not expose it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush forwards to the underlying writer so streaming responses still work.
//
// Wrapping a ResponseWriter silently drops any optional interface it
// implemented — here http.Flusher. Without this, the SSE endpoint cannot push
// anything to the client and correctly refuses to start, which looks like a
// bug in the stream but is really a bug in this wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.log.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
		)
	})
}

// withRecovery keeps one panicking handler from taking down the whole process.
// A monitoring tool that dies because of a bad request is worse than useless.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in handler",
					"panic", v,
					"method", r.Method,
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
