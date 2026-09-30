package api

import (
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/connectivity"
)

// WithConnectivity attaches the canary the runner consults before confirming
// an outage on a network error. A nil canary means the check is turned off,
// which the endpoint reports as such; never calling this means the state is
// unknown (503), the same split as WithWatchdog and WithBackups.
//
// It takes the concrete type rather than an interface so that main can pass
// the nil it gets back from a disabled config straight through: a nil
// *Canary stored in an interface is not a nil interface, and the handler
// would call a method on it.
func (s *Server) WithConnectivity(c *connectivity.Canary) *Server {
	s.connectivityWired = true
	s.connectivity = c
	return s
}

// connectivityResponse is the wire shape of GET /api/v1/connectivity.
//
// It carries no targets. They are the operator's own reference addresses,
// possibly internal ones, and this endpoint is readable by every role so that
// a viewer's dashboard can explain a wall of warnings. The watchdog endpoint
// withholds its receiver for the same reason.
type connectivityResponse struct {
	// Enabled is false when the connectivity check is turned off, by a flag
	// or through the settings API. Offline is then always false: nothing is
	// measuring it.
	Enabled bool `json:"enabled"`
	// Offline is true when every target failed in the most recent round.
	// The canary re-dials every 30 seconds while it holds, so the answer is
	// never older than that during an episode.
	Offline bool `json:"offline"`
	// OfflineSince is when the first failing round of the current episode
	// ran; null while online.
	OfflineSince *time.Time `json:"offline_since"`
}

// handleConnectivity reports whether this host can currently reach its
// connectivity targets.
//
// It exists for the dashboard (SUB-151): while the host is offline, every
// monitor whose check failed on a network error is a warning filed under
// local_network, and twenty amber rows with no sentence above them read as
// twenty separate problems. This is the sentence.
//
// Reading it never dials anything. It reports the state the last round left
// behind, so polling it cannot turn the canary into the periodic outbound
// traffic its design rules out.
func (s *Server) handleConnectivity(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !s.connectivityWired {
		writeError(w, http.StatusServiceUnavailable, "connectivity state unavailable")
		return
	}
	resp := connectivityResponse{Enabled: s.connectivity != nil && s.connectivity.Enabled()}
	if resp.Enabled {
		if since, offline := s.connectivity.OfflineSince(); offline {
			at := since.UTC()
			resp.Offline = true
			resp.OfflineSince = &at
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
