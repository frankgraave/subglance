package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func (s *Server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := s.db.ListMaintenance(r.Context())
	if err != nil {
		s.log.Error("list maintenance", "error", err)
		writeError(w, 500, "could not read maintenance windows")
		return
	}
	type entry struct {
		store.MaintenanceWindow
		Active bool `json:"active"`
	}
	out := make([]entry, 0, len(windows))
	now := time.Now()
	for _, window := range windows {
		out = append(out, entry{window, window.Active(now)})
	}
	writeJSON(w, 200, map[string]any{"maintenance": out})
}
func (s *Server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var req store.MaintenanceWindow
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if req.ID != 0 || !req.CreatedAt.IsZero() {
		writeError(w, 400, "id and created_at are server-managed")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	created, err := s.db.CreateMaintenance(r.Context(), req)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "monitor not found")
		return
	}
	if errors.Is(err, store.ErrMaintenanceLimit) {
		writeError(w, 400, err.Error())
		return
	}
	if err != nil {
		s.log.Error("create maintenance", "error", err)
		writeError(w, 500, "could not save maintenance window")
		return
	}
	writeJSON(w, 201, created)
}
func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid maintenance id")
		return
	}
	err = s.db.DeleteMaintenance(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "maintenance window not found")
		return
	}
	if err != nil {
		writeError(w, 500, "could not cancel maintenance")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
