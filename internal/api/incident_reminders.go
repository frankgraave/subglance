package api

import (
	"context"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// incidentResponses keeps the existing lifecycle/settings precedence, then
// evaluates maintenance using one request clock and at most one bulk read.
// A failed schedule read must never become a claim of reminder eligibility.
func (s *Server) incidentResponses(ctx context.Context, details []store.IncidentDetails, now time.Time) ([]incidentResponse, error) {
	out := make([]incidentResponse, 0, len(details))
	var active []store.MaintenanceWindow
	loaded := false
	for _, detail := range details {
		resp := s.toIncidentResponse(detail)
		if resp.ReminderStatus == "scheduled" {
			if !loaded {
				windows, err := s.db.ListMaintenance(ctx)
				if err != nil {
					return nil, err
				}
				for _, window := range windows {
					// Active intentionally returns false for invalid recurrence.
					// Validate first so corrupt storage cannot imply eligibility.
					if err := window.Validate(); err != nil {
						return nil, fmt.Errorf("invalid maintenance window %d: %w", window.ID, err)
					}
					if window.Active(now) {
						active = append(active, window)
					}
				}
				loaded = true
			}
			for _, window := range active {
				if window.MonitorID == detail.MonitorID || (window.TagKey != "" && detail.MonitorTags[window.TagKey] == window.TagValue) {
					resp.ReminderStatus = "maintenance"
					break
				}
			}
			if resp.ReminderStatus == "scheduled" && detail.MaintenancePending {
				resp.ReminderStatus = "maintenance_pending"
			}
			if resp.ReminderStatus != "scheduled" {
				resp.NextReminderAt = nil
			}
		}
		out = append(out, resp)
	}
	return out, nil
}
