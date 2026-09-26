package api

import (
	"net/http"

	"github.com/frankgraave/subglance/internal/store"
)

// resetPhrase is what the caller must send to reset the instance. The
// settings page asks the operator to type it; the API asks for the same
// string, so a stray POST from a script or a mis-aimed curl cannot empty the
// database either. It is compared exactly: no trimming, no case folding.
const resetPhrase = "DELETE ALL DATA"

type resetRequest struct {
	Confirm string `json:"confirm"`
}

type resetResponse struct {
	Monitors           int64 `json:"monitors"`
	Channels           int64 `json:"channels"`
	APITokens          int64 `json:"api_tokens"`
	MaintenanceWindows int64 `json:"maintenance_windows"`
}

// handleResetInstance empties everything that is monitored and keeps the
// accounts and settings. See store.ResetInstance for exactly what goes.
func (s *Server) handleResetInstance(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Confirm != resetPhrase {
		writeProblem(w, http.StatusBadRequest,
			fieldProblem("confirm", `type "`+resetPhrase+`" exactly to reset this instance`))
		return
	}

	counts, err := s.db.ResetInstance(r.Context())
	if err != nil {
		s.log.Error("reset instance", "error", err)
		writeError(w, http.StatusInternalServerError, "could not reset the instance; nothing was deleted")
		return
	}
	// Every monitor id the cooldown knew is gone. Clearing it keeps a new
	// monitor that reuses nothing from inheriting a stale entry, and keeps
	// the map from holding ids that no longer exist.
	s.manualChecks.clear()

	// Logged at warn with who did it: this is the one line an operator will
	// go looking for when an instance is unexpectedly empty.
	var actor int64
	if user, ok := UserFromContext(r.Context()); ok {
		actor = user.ID
	}
	s.log.Warn("instance reset", "user_id", actor,
		"monitors", counts.Monitors, "channels", counts.Channels,
		"api_tokens", counts.APITokens, "maintenance_windows", counts.MaintenanceWindows)
	writeJSON(w, http.StatusOK, resetJSON(counts))
}

func resetJSON(c store.ResetCounts) resetResponse {
	return resetResponse{
		Monitors:           c.Monitors,
		Channels:           c.Channels,
		APITokens:          c.APITokens,
		MaintenanceWindows: c.MaintenanceWindows,
	}
}
