package api

import (
	"net/http"

	"github.com/frankgraave/subglance/internal/watchdog"
)

// WithWatchdog attaches the very instance that sends pings. Explicit nil means
// disabled; never calling this method means the source is unknown (503).
func (s *Server) WithWatchdog(w *watchdog.Watchdog) *Server {
	s.watchdogSnapshot = w.Snapshot
	return s
}

func (s *Server) handleWatchdog(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if s.watchdogSnapshot == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "watchdog state unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, s.watchdogSnapshot())
}
