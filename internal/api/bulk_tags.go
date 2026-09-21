package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/frankgraave/subglance/internal/store"
)

func (s *Server) handlePreviewTags(w http.ResponseWriter, r *http.Request) {
	s.handleTagOperation(w, r, true)
}
func (s *Server) handleTransformTags(w http.ResponseWriter, r *http.Request) {
	s.handleTagOperation(w, r, false)
}

func (s *Server) handleTagOperation(w http.ResponseWriter, r *http.Request, preview bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	expected := r.Header.Get("If-Match")
	if !preview {
		if expected == "" {
			writeError(w, http.StatusPreconditionRequired, "preview the tag change first and send its ETag in If-Match")
			return
		}
		if !store.ValidTagPreviewETag(expected) {
			writeError(w, http.StatusBadRequest, "If-Match must be one tag preview ETag")
			return
		}
	}
	var raw json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	if err := decoder.Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "expected exactly one JSON object")
		return
	}
	var op store.TagOperation
	if err := json.Unmarshal(raw, &op); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		writeError(w, http.StatusBadRequest, "expected a tag operation object")
		return
	}
	// Presence matters: a global operation must not quietly accept even an
	// empty selection, or a caller can mistake it for a selection-scoped write.
	for field := range fields {
		allowed := field == "action" || field == "key" ||
			(field == "monitor_ids" && (op.Action == "apply" || op.Action == "remove")) ||
			(field == "value" && (op.Action == "apply" || op.Action == "remove" || op.Action == "rename_value")) ||
			(field == "new_key" && op.Action == "rename_key") ||
			(field == "new_value" && op.Action == "rename_value")
		if !allowed {
			writeError(w, http.StatusBadRequest, "field "+field+" is not allowed for action "+op.Action)
			return
		}
	}
	result, etag, err := s.db.TransformTags(r.Context(), op, expected, preview)
	switch {
	case errors.Is(err, store.ErrInvalidTagOperation):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "one or more selected monitors no longer exist; refresh the selection")
		return
	case errors.Is(err, store.ErrTagPreviewChanged):
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	case err != nil:
		s.log.Error("transform monitor tags", "error", err)
		writeError(w, http.StatusInternalServerError, "could not change monitor tags")
		return
	}
	if preview {
		w.Header().Set("ETag", etag)
	}
	writeJSON(w, http.StatusOK, result)
}
