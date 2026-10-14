package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"

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
	// Read raw first: which fields are allowed depends on the action, so
	// the field check below is this endpoint's own.
	var raw json.RawMessage
	if !readJSONLimit(w, r, maxBulkJSONBody, &raw) {
		return
	}
	var op store.TagOperation
	if err := json.Unmarshal(raw, &op); err != nil {
		status, p := bodyRefusal(err, &op, "", 0)
		writeProblem(w, status, p)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		writeError(w, http.StatusBadRequest, "expected a tag operation object")
		return
	}
	// Which fields are allowed depends on the action, so an unknown action is
	// refused as itself first: checked the other way round, a wrong action was
	// answered by blaming whichever correct field the map yielded first.
	if !store.ValidTagAction(op.Action) {
		writeProblem(w, http.StatusBadRequest, fieldProblem("action",
			"action must be apply, remove, rename_key or rename_value"))
		return
	}
	// Presence matters: a global operation must not quietly accept even an
	// empty selection, or a caller can mistake it for a selection-scoped write.
	// Sorted, so a body with two such fields gets the same answer every time.
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		allowed := field == "action" || field == "key" ||
			(field == "monitor_ids" && (op.Action == "apply" || op.Action == "remove")) ||
			(field == "value" && (op.Action == "apply" || op.Action == "remove" || op.Action == "rename_value")) ||
			(field == "new_key" && op.Action == "rename_key") ||
			(field == "new_value" && op.Action == "rename_value")
		if !allowed {
			writeProblem(w, http.StatusBadRequest, fieldProblem(field, field+" is not allowed for action "+op.Action))
			return
		}
	}
	result, etag, err := s.db.TransformTags(r.Context(), op, expected, preview)
	var invalid *store.TagOperationError
	switch {
	case errors.As(err, &invalid):
		// The store's own words without its "invalid tag operation:" prefix,
		// which names the Go error and tells a person nothing.
		writeProblem(w, http.StatusBadRequest, problem{field: invalid.Field, msg: invalid.Msg})
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "one or more selected monitors no longer exist; refresh the selection")
		return
	case errors.Is(err, store.ErrTagPreviewChanged):
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	case errors.Is(err, store.ErrTagRenameRuleConflict):
		writeError(w, http.StatusConflict, err.Error())
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
