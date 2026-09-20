package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/frankgraave/subglance/internal/state"
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

	// These describe reminder issuance, not successful channel delivery.
	ReminderCount  int        `json:"reminder_count"`
	RemindedAt     *time.Time `json:"reminded_at"`
	NextReminderAt *time.Time `json:"next_reminder_at"`
	ReminderStatus string     `json:"reminder_status"`
}

// reminderStateSource is the read-only facet of the attached checker pipeline.
// It exposes the same suppression state the reminder loop consults.
type reminderStateSource interface {
	Flapping(monitorID int64) bool
}

func (s *Server) reminderFlapping(id int64) bool {
	source, ok := s.prober.(reminderStateSource)
	return ok && source.Flapping(id)
}

func (s *Server) toIncidentResponse(detail store.IncidentDetails) incidentResponse {
	inc := detail.Incident
	resp := incidentResponse{
		ID:             inc.ID,
		MonitorID:      inc.MonitorID,
		StartedAt:      inc.StartedAt,
		Confirmed:      inc.Confirmed(),
		Resolved:       inc.Resolved(),
		Acked:          !inc.AckedAt.IsZero(),
		DurationS:      int(inc.Duration().Seconds()),
		Cause:          inc.Cause,
		LastError:      inc.LastError,
		ReminderCount:  inc.ReminderCount,
		ReminderStatus: "scheduled",
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
	if !inc.RemindedAt.IsZero() {
		t := inc.RemindedAt
		resp.RemindedAt = &t
	}
	switch {
	case inc.Resolved():
		resp.ReminderStatus = "resolved"
	case inc.Acked():
		resp.ReminderStatus = "acknowledged"
	case !inc.Confirmed():
		resp.ReminderStatus = "unconfirmed"
	case !detail.MonitorEnabled:
		resp.ReminderStatus = "paused"
	case !state.ReminderEnabled(time.Duration(detail.RepeatAfterS) * time.Second):
		resp.ReminderStatus = "disabled"
	case s.reminderFlapping(inc.MonitorID):
		resp.ReminderStatus = "flapping"
	default:
		next := state.NextReminder(inc.ConfirmedAt, inc.RemindedAt, inc.ReminderCount, time.Duration(detail.RepeatAfterS)*time.Second)
		resp.NextReminderAt = &next
	}
	return resp
}

// handleListOpenIncidents answers "what is broken right now" — the query the
// dashboard's landing view is built around.
func (s *Server) handleListOpenIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.db.ListOpenIncidentDetails(r.Context())
	if err != nil {
		s.log.Error("list open incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, s.toIncidentResponse(inc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

// handleListMonitorIncidents returns the incident history for one monitor.
func (s *Server) handleListMonitorIncidents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.requireMonitor(w, r, id) {
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

	incidents, err := s.db.ListIncidentDetails(r.Context(), id, limit)
	if err != nil {
		s.log.Error("list incidents", "monitor_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}

	out := make([]incidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		out = append(out, s.toIncidentResponse(inc))
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
