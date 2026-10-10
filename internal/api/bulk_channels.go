package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/frankgraave/subglance/internal/store"
)

// handlePreviewMonitorChannels and handleChangeMonitorChannels add one channel
// to, or remove it from, a selection of monitors: a preview that counts, then
// a commit under the preview's ETag. They follow the bulk tag routes, so the
// inventory's two selection actions behave alike.
func (s *Server) handlePreviewMonitorChannels(w http.ResponseWriter, r *http.Request) {
	s.handleChannelOperation(w, r, true)
}

func (s *Server) handleChangeMonitorChannels(w http.ResponseWriter, r *http.Request) {
	s.handleChannelOperation(w, r, false)
}

func (s *Server) handleChannelOperation(w http.ResponseWriter, r *http.Request, preview bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	expected := r.Header.Get("If-Match")
	if !preview {
		if expected == "" {
			writeError(w, http.StatusPreconditionRequired, "preview the channel change first and send its ETag in If-Match")
			return
		}
		if !store.ValidChannelPreviewETag(expected) {
			writeError(w, http.StatusBadRequest, "If-Match must be one channel preview ETag")
			return
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	// An unknown field is refused rather than ignored: a client that sent
	// "channel_ids" expecting a replacement must not get an add of nothing.
	decoder.DisallowUnknownFields()
	var op store.ChannelOperation
	if err := decoder.Decode(&op); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "expected exactly one JSON object")
		return
	}
	result, etag, err := s.db.ChangeMonitorChannels(r.Context(), op, expected, preview)
	switch {
	case errors.Is(err, store.ErrInvalidChannelOperation), errors.Is(err, store.ErrUnknownChannel):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "one or more selected monitors no longer exist; refresh the selection")
		return
	case errors.Is(err, store.ErrChannelPreviewChanged):
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	case err != nil:
		s.log.Error("change monitor channels", "error", err)
		writeError(w, http.StatusInternalServerError, "could not change monitor channels")
		return
	}
	if preview {
		w.Header().Set("ETag", etag)
	} else {
		s.log.Info("monitor channels changed", "action", op.Action, "channel_id", op.ChannelID, "changed", result.Changed)
	}
	writeJSON(w, http.StatusOK, result)
}
