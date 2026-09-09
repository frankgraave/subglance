package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// incidentResponse is the API shape of an incident.
//
// The two timestamps are both exposed on purpose. StartedAt is when the first
// check failed; ConfirmedAt is when the failure count crossed the monitor's
// threshold and a human was told. Support conversations turn on that gap
// ("we saw it at 14:02, you alerted at 14:04"), so hiding it would be a
// disservice.
type incidentResponse struct {
	ID        int64 `json:"id"`
	MonitorID int64 `json:"monitor_id"`

	StartedAt   time.Time  `json:"started_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	AckedAt     *time.Time `json:"acked_at,omitempty"`

	Confirmed bool `json:"confirmed"`
	Resolved  bool `json:"resolved"`
	Acked     bool `json:"acked"`

	DurationS int `json:"duration_s"`

	Cause     string `json:"cause,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func toIncidentResponse(inc store.Incident) incidentResponse {
	resp := incidentResponse{
		ID:        inc.ID,
		MonitorID: inc.MonitorID,
		StartedAt: inc.StartedAt,
		Confirmed: inc.Confirmed(),
		Resolved:  inc.Resolved(),
		Acked:     !inc.AckedAt.IsZero(),
		DurationS: int(inc.Duration().Seconds()),
		Cause:     inc.Cause,
		LastError: inc.LastError,
	}

	if inc.Confirmed() {
		t := inc.ConfirmedAt
		resp.ConfirmedAt = &t
	}
	if inc.Resolved() {
		t := inc.ResolvedAt
		resp.ResolvedAt = &t
	}
	if !inc.AckedAt.IsZero() {
		t := inc.AckedAt
		resp.AckedAt = &t
	}
	return resp
}

// handleListOpenIncidents answers "what is broken right now" — the query the
// dashboard's landing view is built around.
func (s *Server) handleListOpenIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.db.ListOpenIncidents(r.Context())
	if err != nil {
		s.log.Error("list open incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, toIncidentResponse(inc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

// handleListMonitorIncidents returns the incident history for one monitor.
func (s *Server) handleListMonitorIncidents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}

	incidents, err := s.db.ListIncidents(r.Context(), id, limit)
	if err != nil {
		s.log.Error("list incidents", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, toIncidentResponse(inc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

// handleAckIncident marks an incident as acknowledged.
//
// Acknowledging says "seen, working on it". It stops repeat notifications
// without claiming the problem is solved, which is the honest middle state
// between alerting and resolving.
func (s *Server) handleAckIncident(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	err = s.db.AckIncident(r.Context(), id, time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		s.log.Error("ack incident", "incident_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not acknowledge incident")
		return
	}

	s.log.Info("incident acknowledged", "incident_id", id)
	w.WriteHeader(http.StatusNoContent)
}
