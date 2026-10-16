package api

import (
	"database/sql"
	"errors"
	"net/http"
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
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID != 0 || !req.CreatedAt.IsZero() {
		writeError(w, 400, "id and created_at are server-managed")
		return
	}
	req, err := store.NormaliseMaintenance(req)
	if err != nil {
		writeMaintenanceProblem(w, err)
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

// writeMaintenanceProblem answers a refused window with a 400 that names the
// field at fault, when there is one.
func writeMaintenanceProblem(w http.ResponseWriter, err error) {
	var invalid *store.MaintenanceError
	if errors.As(err, &invalid) {
		writeProblem(w, http.StatusBadRequest, problem{field: invalid.Field, msg: invalid.Msg})
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "maintenance")
	if !ok {
		return
	}
	err := s.db.DeleteMaintenance(r.Context(), id)
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
