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

// Handler returns the root HTTP handler with all routes and middleware applied.
//
// Routes are split into three groups by what they require:
//
//	public   — health, readiness, setup and login
//	read     — any authenticated user, including viewers
//	write    — editors and admins
//	admin    — administrators only
//
// Everything that is not explicitly public requires authentication. That
// default matters: forgetting to guard a new route should fail closed, not
// silently expose it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/ready", s.handleReady)
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)

	// Authenticated: any role.
	read := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(h))
	}
	read("GET /api/v1/auth/me", s.handleMe)
	read("POST /api/v1/auth/password", s.handleChangePassword)

	read("GET /api/v1/monitors", s.handleListMonitors)
	read("GET /api/v1/monitors/{id}", s.handleGetMonitor)
	read("GET /api/v1/monitors/{id}/heartbeats", s.handleListHeartbeats)
	read("GET /api/v1/monitors/{id}/incidents", s.handleListMonitorIncidents)
	read("GET /api/v1/incidents", s.handleListOpenIncidents)

	// The live stream is a read: a viewer may watch, but watching is all it
	// does. It sits behind the same auth as everything else — an unguarded
	// stream would leak every monitor name and outage to anyone who can
	// reach the port.
	read("GET /api/v1/stream", s.handleStream)

	read("GET /api/v1/tokens", s.handleListTokens)
	read("POST /api/v1/tokens", s.handleCreateToken)
	read("DELETE /api/v1/tokens/{id}", s.handleRevokeToken)

	// Authenticated: editor or admin.
	requireWrite := s.requireRole(store.Role.CanWrite, "your role does not allow changes")
	write := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(requireWrite(h)))
	}
	write("POST /api/v1/monitors", s.handleCreateMonitor)
	write("DELETE /api/v1/monitors/{id}", s.handleDeleteMonitor)
	write("POST /api/v1/monitors/{id}/pause", s.handlePauseMonitor)
	write("POST /api/v1/monitors/{id}/resume", s.handleResumeMonitor)
	write("POST /api/v1/incidents/{id}/ack", s.handleAckIncident)

	// Authenticated: admin only.
	requireAdmin := s.requireRole(store.Role.CanAdmin, "this action requires an administrator")
	admin := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(requireAdmin(h)))
	}
	admin("GET /api/v1/users", s.handleListUsers)
	admin("POST /api/v1/users", s.handleCreateUser)
	admin("DELETE /api/v1/users/{id}", s.handleDeleteUser)

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
