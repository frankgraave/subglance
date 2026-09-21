package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestIncidentReminderMaintenanceReadFailures(t *testing.T) {
	for _, fault := range []string{"lookup", "json", "timezone", "tag lookup"} {
		t.Run(fault, func(t *testing.T) {
			srv, db, m, _, _ := reminderFixture(t)
			now := time.Now()
			if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "test", MonitorID: m.ID, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			query := "DROP TABLE maintenance_windows"
			switch fault {
			case "json":
				query = `UPDATE maintenance_windows SET spec = '{broken'`
			case "timezone":
				query = `UPDATE maintenance_windows SET spec = '{"name":"bad timezone","monitor_id":1,"timezone":"Not/AZone","weekdays":[0],"local_time":"12:00","duration_minutes":60}'`
			case "tag lookup":
				query = "DROP TABLE monitor_tags"
			}
			if _, err := db.Writer.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
				rec := httptest.NewRecorder()
				authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), `"scheduled"`) {
					t.Fatalf("failed %s read must not claim eligibility: %d %s", fault, rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestIncidentReminderMaintenancePreservesPrecedence(t *testing.T) {
	for _, status := range []string{"resolved", "acknowledged", "unconfirmed", "paused", "disabled", "flapping"} {
		t.Run(status, func(t *testing.T) {
			srv, db, m, inc, confirmed := reminderFixture(t)
			if err := db.RecordReminder(t.Context(), inc.ID, confirmed); err != nil {
				t.Fatal(err)
			}
			if err := db.SetMaintenancePending(t.Context(), inc.ID, true); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "active", MonitorID: m.ID, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			switch status {
			case "resolved":
				if _, err := db.ResolveIncident(t.Context(), m.ID, now); err != nil {
					t.Fatal(err)
				}
			case "acknowledged":
				if err := db.AckIncident(t.Context(), inc.ID, now); err != nil {
					t.Fatal(err)
				}
			case "unconfirmed":
				if _, err := db.Writer.ExecContext(t.Context(), "UPDATE incidents SET confirmed_at=NULL WHERE id=?", inc.ID); err != nil {
					t.Fatal(err)
				}
			case "paused", "disabled":
				if status == "paused" {
					m.Enabled = false
				} else {
					m.RepeatAfterS = 0
				}
				if _, err := db.UpdateMonitor(t.Context(), m); err != nil {
					t.Fatal(err)
				}
			case "flapping":
				srv.WithProber(&reminderStateProbe{suppressed: map[int64]bool{m.ID: true}})
			}
			paths := []string{"/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"}
			if status == "resolved" {
				paths = append(paths, "/api/v1/incidents/resolved?days=730")
			} else {
				paths = append(paths, "/api/v1/incidents")
			}
			// These lifecycle states need no maintenance lookup at all. Assert
			// precedence both before and after the windows table becomes unreadable.
			for pass := 0; pass < 2; pass++ {
				for _, path := range paths {
					rows := readReminderRows(t, srv, path)
					assertReminderJSON(t, rows[0], "reminder_status", status)
					assertReminderJSON(t, rows[0], "next_reminder_at", nil)
					assertReminderJSON(t, rows[0], "reminder_count", 1)
					assertReminderJSON(t, rows[0], "reminded_at", confirmed)
				}
				if pass == 0 {
					if _, err := db.Writer.ExecContext(t.Context(), "DROP TABLE maintenance_windows"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestResolvedIncidentRemindersNeedNoSettingsOrWindows(t *testing.T) {
	srv, db, m, _, _ := reminderFixture(t)
	if _, err := db.ResolveIncident(t.Context(), m.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	count, windows, tags := new(atomic.Int64), new(atomic.Int64), new(atomic.Int64)
	original := db.Reader
	db.Reader = sql.OpenDB(incidentReadConnector{driver: original.Driver(), path: path, count: count, windows: windows, tags: tags})
	t.Cleanup(func() { _ = original.Close() })
	// A malformed private setting would break GetMonitor; global resolved
	// history must read neither that setting nor maintenance/tag tables.
	for _, query := range []string{`UPDATE monitors SET headers_json='{broken'`, "DROP TABLE maintenance_windows", "DROP TABLE monitor_tags"} {
		if _, err := db.Writer.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	rows := readReminderRows(t, srv, "/api/v1/incidents/resolved?days=730")
	if len(rows) != 1 {
		t.Fatalf("resolved rows = %d", len(rows))
	}
	assertReminderJSON(t, rows[0], "reminder_status", "resolved")
	if count.Load() != 1 || windows.Load() != 0 || tags.Load() != 0 {
		t.Fatalf("resolved reads: incident/monitor=%d maintenance=%d standalone tags=%d, want 1/0/0", count.Load(), windows.Load(), tags.Load())
	}
}
