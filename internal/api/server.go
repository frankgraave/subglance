// Package api serves the SubGlance HTTP API and, in production builds, the
// embedded web UI.
//
// Product principle 4: the UI is a client of this API and
// gets no private endpoints. Anything the dashboard can do, a script can do.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/buildinfo"
)

// Server wires the HTTP routes together.
type Server struct {
	log       *slog.Logger
	startedAt time.Time
}

// New returns a Server ready to be mounted.
func New(log *slog.Logger) *Server {
	return &Server{log: log, startedAt: time.Now()}
}

// Handler returns the root HTTP handler with all routes and middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	return s.withRecovery(s.withLogging(mux))
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
