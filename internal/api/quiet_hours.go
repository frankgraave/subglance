package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/store"
)

// quietHoursRequest is the body of PUT /api/v1/channels/{id}/quiet-hours.
//
// During is optional and defaults to hold. A client that says nothing about
// what should happen to a 03:00 alert gets the answer that cannot lose it.
type quietHoursRequest struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
	During   string `json:"during"`
}

// handleSetQuietHours creates or replaces a channel's quiet hours.
func (s *Server) handleSetQuietHours(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var req quietHoursRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	q := store.QuietHours{
		ChannelID: id,
		Start:     strings.TrimSpace(req.Start),
		End:       strings.TrimSpace(req.End),
		Timezone:  strings.TrimSpace(req.Timezone),
		During:    strings.TrimSpace(req.During),
	}
	if q.During == "" {
		q.During = store.QuietHold
	}
	if err := q.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err := s.db.SetQuietHours(r.Context(), q)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.log.Error("set quiet hours", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save quiet hours")
		return
	}

	s.log.Info("quiet hours set", "channel_id", id, "start", q.Start, "end", q.End,
		"timezone", q.Timezone, "during", q.During)
	writeJSON(w, http.StatusOK, q)
}

// handleClearQuietHours removes a channel's quiet hours. Anything they were
// holding is released at once rather than left waiting for a window that no
// longer exists.
func (s *Server) handleClearQuietHours(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	err := s.db.ClearQuietHours(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel has no quiet hours")
		return
	}
	if err != nil {
		s.log.Error("clear quiet hours", "channel_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clear quiet hours")
		return
	}

	s.log.Info("quiet hours cleared", "channel_id", id)
	w.WriteHeader(http.StatusNoContent)
}
