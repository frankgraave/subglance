package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestIncidentReminderMaintenanceSuppression(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "active"
		if pending {
			name = "pending_after_end"
		}
		t.Run(name, func(t *testing.T) {
			srv, db, m, inc, confirmed := reminderFixture(t)
			last := confirmed.Add(15 * time.Minute)
			if err := db.RecordReminder(t.Context(), inc.ID, last); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			end := now.Add(time.Hour)
			if pending {
				end = now.Add(-time.Minute)
				if err := db.SetMaintenancePending(t.Context(), inc.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{
				Name: name, MonitorID: m.ID, StartsAt: now.Add(-time.Hour), EndsAt: end,
			}); err != nil {
				t.Fatal(err)
			}
			status := "maintenance"
			if pending {
				status = "maintenance_pending"
			}
			for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
				t.Run(path, func(t *testing.T) {
					rows := readReminderRows(t, srv, path)
					if len(rows) != 1 {
						t.Fatalf("rows = %d, want one", len(rows))
					}
					assertReminderJSON(t, rows[0], "reminder_status", status)
					assertReminderJSON(t, rows[0], "next_reminder_at", nil)
					assertReminderJSON(t, rows[0], "reminder_count", 1)
					assertReminderJSON(t, rows[0], "reminded_at", last)
				})
			}
			if pending {
				if err := db.SetMaintenancePending(t.Context(), inc.ID, false); err != nil {
					t.Fatal(err)
				}
				rows := readReminderRows(t, srv, "/api/v1/incidents")
				assertReminderJSON(t, rows[0], "reminder_status", "scheduled")
				assertReminderJSON(t, rows[0], "next_reminder_at", last.Add(time.Hour))
			}
		})
	}
}
