package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/store"
)

// WithConnectivityPins records which connectivity settings were fixed by a
// flag or an environment variable, so the settings endpoints can show them as
// read-only and refuse to store a value underneath them.
func (s *Server) WithConnectivityPins(p store.ConnectivityPins) *Server {
	s.connectivityPins = p
	return s
}

// connectivitySettingsResponse is the wire shape of GET and PUT
// /api/v1/settings/connectivity.
type connectivitySettingsResponse struct {
	Enabled connectivityEnabledJSON `json:"enabled"`
	Targets connectivityTargetsJSON `json:"targets"`
	// DefaultTargets is what the check dials when nothing else is set, so
	// an editor can offer to go back to it.
	DefaultTargets []string `json:"default_targets"`
	MaxTargets     int      `json:"max_targets"`
}

type connectivityEnabledJSON struct {
	Value    bool    `json:"value"`
	Source   string  `json:"source"`
	PinnedBy *string `json:"pinned_by"`
}

type connectivityTargetsJSON struct {
	Value    []string `json:"value"`
	Source   string   `json:"source"`
	PinnedBy *string  `json:"pinned_by"`
}

func connectivitySettingsJSON(c store.ConnectivitySettings) connectivitySettingsResponse {
	return connectivitySettingsResponse{
		Enabled: connectivityEnabledJSON{
			Value: c.Enabled.Value, Source: c.Enabled.Source, PinnedBy: pinnedBy(c.Enabled.PinnedBy),
		},
		Targets: connectivityTargetsJSON{
			Value: c.Targets.Value, Source: c.Targets.Source, PinnedBy: pinnedBy(c.Targets.PinnedBy),
		},
		DefaultTargets: append([]string(nil), connectivity.DefaultTargets...),
		MaxTargets:     connectivity.MaxTargets,
	}
}

// connectivityReady reports whether the settings endpoints have a canary to
// apply a change to, answering 503 when not. An API assembled without one
// (or with the nil a caller passes for "off") cannot promise that a saved
// change takes effect, so it does not pretend to.
func (s *Server) connectivityReady(w http.ResponseWriter) bool {
	if !s.connectivityWired || s.connectivity == nil {
		writeError(w, http.StatusServiceUnavailable, "the connectivity check is not available on this server")
		return false
	}
	return true
}

// handleGetConnectivitySettings reports the connectivity check's switch and
// targets, where each came from, and the defaults.
//
// Administrators only, unlike GET /api/v1/connectivity: targets such as
// gateway:443 can name hosts on the operator's own network, which is why
// that endpoint leaves them out.
func (s *Server) handleGetConnectivitySettings(w http.ResponseWriter, r *http.Request) {
	if !s.connectivityReady(w) {
		return
	}
	// Held while reading, so the version and the settings come from the
	// same save: a PUT committing between the two reads would otherwise
	// hand out the old ETag with the new body, and that ETag would then be
	// refused in If-Match although the caller holds the latest settings.
	s.connectivityMu.Lock()
	defer s.connectivityMu.Unlock()
	version, err := s.db.ConnectivityVersion(r.Context())
	if err != nil {
		s.log.Error("read connectivity settings version", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read connectivity settings")
		return
	}
	resolved, err := s.db.ResolveConnectivity(r.Context(), s.connectivityPins)
	if err != nil {
		s.log.Error("read connectivity settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read connectivity settings")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", retentionETag(version))
	writeJSON(w, http.StatusOK, connectivitySettingsJSON(resolved))
}

// connectivitySettingsRequest is the body of PUT
// /api/v1/settings/connectivity. An absent field is left as it is.
type connectivitySettingsRequest struct {
	Enabled *bool     `json:"enabled"`
	Targets *[]string `json:"targets"`
}

// handleSetConnectivitySettings stores the switch and the targets and applies
// them to the running canary, without a restart.
//
// If-Match works as it does for retention: optional, and when present the
// save is refused if anyone saved since the caller read the settings.
//
// Targets are not run through the private-address guard that monitor and
// channel targets are. The check is meant to dial the operator's own gateway
// or another host on their network, it only ever completes a TCP handshake
// and sends nothing, and the one thing it reports is whether every target
// failed at once. The flag has always accepted any address; the API accepts
// the same ones.
func (s *Server) handleSetConnectivitySettings(w http.ResponseWriter, r *http.Request) {
	if !s.connectivityReady(w) {
		return
	}
	ifMatchValues, hasIfMatch := r.Header["If-Match"]
	var (
		wantTags []string
		wantAny  bool
	)
	if hasIfMatch {
		var valid bool
		if wantTags, wantAny, valid = parseIfMatch(strings.Join(ifMatchValues, ", ")); !valid {
			writeError(w, http.StatusBadRequest, "malformed If-Match header")
			return
		}
	}

	var req connectivitySettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Enabled == nil && req.Targets == nil {
		writeProblem(w, http.StatusBadRequest, bodyProblem("send at least one of enabled and targets"))
		return
	}
	change, status, p := s.connectivityInput(req)
	if !p.ok() {
		writeProblem(w, status, p)
		return
	}

	// Saving and applying happen under one lock, and what is applied is
	// read back from the database rather than taken from this request.
	// Two saves racing would otherwise be able to leave the canary running
	// the older one while the database holds the newer.
	s.connectivityMu.Lock()
	defer s.connectivityMu.Unlock()

	var (
		version int64
		err     error
	)
	if hasIfMatch && !wantAny {
		version, err = s.db.SaveConnectivityIfVersion(r.Context(), change, retentionVersions(wantTags))
	} else {
		version, err = s.db.SaveConnectivity(r.Context(), change)
	}
	if errors.Is(err, store.ErrConnectivityVersion) {
		writeError(w, http.StatusPreconditionFailed,
			"the connectivity check was changed by someone else since you read it; load it again and reapply your change")
		return
	}
	if err != nil {
		s.log.Error("save connectivity settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not save connectivity settings")
		return
	}

	resolved, err := s.db.ResolveConnectivity(r.Context(), s.connectivityPins)
	if err == nil {
		err = s.connectivity.Configure(resolved.Canary())
	}
	if err != nil {
		// Saved but not applied: the running check keeps what it had until
		// the next start reads the saved row. Reported as a failure, because
		// a success here would claim the new targets are being used.
		s.log.Error("apply connectivity settings", "error", err)
		writeError(w, http.StatusInternalServerError,
			"the connectivity settings were saved but could not be applied; they take effect on the next start")
		return
	}
	s.log.Info("connectivity settings changed",
		"enabled", resolved.Enabled.Value, "targets", strings.Join(resolved.Targets.Value, ","))

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", retentionETag(version))
	writeJSON(w, http.StatusOK, connectivitySettingsJSON(resolved))
}

// connectivityInput checks a PUT against the pins and the target rules. A
// pinned field is a conflict with the server's configuration (409), a bad
// value a bad request (400).
func (s *Server) connectivityInput(req connectivitySettingsRequest) (store.ConnectivityChange, int, problem) {
	var change store.ConnectivityChange
	if req.Enabled != nil {
		if pin := s.connectivityPins.Enabled; pin != nil {
			return change, http.StatusConflict, fieldProblem("enabled",
				"the connectivity check is turned on or off by "+pin.By+" and can only be changed there")
		}
		change.Enabled = req.Enabled
	}
	if req.Targets != nil {
		if pin := s.connectivityPins.Targets; pin != nil {
			return change, http.StatusConflict, fieldProblem("targets",
				"the connectivity targets are set by "+pin.By+" and can only be changed there")
		}
		targets := make([]string, 0, len(*req.Targets))
		for _, t := range *req.Targets {
			targets = append(targets, strings.TrimSpace(t))
		}
		for _, t := range targets {
			if t == "" {
				return change, http.StatusBadRequest, fieldProblem("targets", "a connectivity target is empty")
			}
		}
		if err := connectivity.ValidateTargets(targets); err != nil {
			return change, http.StatusBadRequest, fieldProblem("targets", err.Error())
		}
		change.Targets = targets
	}
	return change, 0, problem{}
}
