package api

import (
	"errors"
	"net/http"

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
	if !readJSON(w, r, &req) {
		return
	}

	q, err := store.NormaliseQuietHours(store.QuietHours{
		ChannelID: id, Start: req.Start, End: req.End, Timezone: req.Timezone, During: req.During,
	})
	if err != nil {
		writeProblem(w, http.StatusBadRequest, quietHoursProblem(err))
		return
	}

	err = s.db.SetQuietHours(r.Context(), q)
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

// quietHoursProblem turns the store's refusal into a problem on the field it
// names. A refusal that is not a *store.QuietHoursError keeps its words and
// blames no field rather than a guessed one.
func quietHoursProblem(err error) problem {
	var invalid *store.QuietHoursError
	if errors.As(err, &invalid) {
		return fieldProblem(invalid.Field, invalid.Msg)
	}
	return bodyProblem(err.Error())
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
