package api

import (
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestIncidentReminderMaintenanceOneOffBoundaries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Until(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)))
		srv, db, m, inc, confirmed := reminderFixture(t)
		now := time.Now()
		if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "one-off", MonitorID: m.ID, StartsAt: now.Add(time.Minute), EndsAt: now.Add(2 * time.Minute)}); err != nil {
			t.Fatal(err)
		}
		other := seedMonitor(t, db, store.Monitor{Name: "unrelated", Type: "http", Target: "https://example.com", Enabled: true})
		if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "unrelated", MonitorID: other.ID, StartsAt: now, EndsAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			advance time.Duration
			pending bool
			status  string
		}{
			{0, false, "scheduled"},
			{time.Minute, false, "maintenance"},
			{0, true, "maintenance"},
			{time.Minute, true, "maintenance_pending"},
			{0, false, "scheduled"},
		} {
			time.Sleep(step.advance)
			if err := db.SetMaintenancePending(t.Context(), inc.ID, step.pending); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
				rows := readReminderRows(t, srv, path)
				assertReminderJSON(t, rows[0], "reminder_status", step.status)
				var next any
				if step.status == "scheduled" {
					next = confirmed.Add(15 * time.Minute)
				}
				assertReminderJSON(t, rows[0], "next_reminder_at", next)
				assertReminderJSON(t, rows[0], "reminder_count", 0)
				assertReminderJSON(t, rows[0], "reminded_at", nil)
			}
		}
	})
}

func TestIncidentReminderMaintenanceUsesCapturedRequestTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Until(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)))
		srv, db, m, _, _ := reminderFixture(t)
		now := time.Now()
		if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "boundary", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
		details, err := db.ListOpenIncidentDetails(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		// Simulate the request crossing a boundary while reading its data. All
		// rows must use the captured clock, not a fresh per-window/per-row time.
		time.Sleep(2 * time.Second)
		rows, err := srv.incidentResponses(t.Context(), details, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].ReminderStatus != "maintenance" || rows[0].NextReminderAt != nil {
			t.Fatalf("captured request time lost: %+v", rows)
		}
	})
}

func TestIncidentReminderMaintenanceCurrentTagsAndRecurrence(t *testing.T) {
	for _, tc := range []struct {
		name, at string
		minutes  int
		active   bool
	}{
		{"weekly active", "2026-10-18T00:45:00Z", 60, true},
		{"weekly future", "2026-10-18T00:29:59Z", 60, false},
		{"weekly end exclusive", "2026-10-18T01:30:00Z", 60, false},
		{"wrong weekday", "2026-10-19T00:45:00Z", 60, false},
		{"DST missing skipped", "2026-03-29T01:45:00Z", 60, false},
		{"DST earlier fold", "2026-10-25T00:30:00Z", 30, true},
		{"DST later fold skipped", "2026-10-25T01:30:00Z", 30, false},
		{"DST elapsed duration", "2026-10-25T01:45:00Z", 120, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				at, err := time.Parse(time.RFC3339, tc.at)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Until(at))
				srv, db, m, _, confirmed := reminderFixture(t)
				if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{
					Name: tc.name, TagKey: "env", TagValue: "prod", Timezone: "Europe/Amsterdam",
					Weekdays: []int{0}, LocalTime: "02:30", DurationMinutes: tc.minutes,
				}); err != nil {
					t.Fatal(err)
				}
				// Membership is current, not captured on the incident or a beat.
				for _, value := range []string{"prod", "stage", "", "prod"} {
					m.Tags = nil
					if value != "" {
						m.Tags = map[string]string{"env": value}
					}
					if _, err := db.UpdateMonitor(t.Context(), m); err != nil {
						t.Fatal(err)
					}
					status := "scheduled"
					var next any = confirmed.Add(15 * time.Minute)
					if tc.active && value == "prod" {
						status, next = "maintenance", nil
					}
					for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
						rows := readReminderRows(t, srv, path)
						assertReminderJSON(t, rows[0], "reminder_status", status)
						assertReminderJSON(t, rows[0], "next_reminder_at", next)
					}
				}
			})
		})
	}
}
