package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestMaintenanceReminderReadIntegration(t *testing.T) {
	for _, scope := range []string{"monitor", "tag"} {
		t.Run(scope, func(t *testing.T) {
			srv, db, m, inc, confirmed := reminderFixture(t)
			now := time.Now().UTC()
			window := store.MaintenanceWindow{Name: "integration", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)}
			if scope == "monitor" {
				window.MonitorID = m.ID
			} else {
				window.TagKey = "env"
				window.TagValue = "prod"
			}
			w, err := db.CreateMaintenance(t.Context(), window)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"}
			assertState := func(status string) {
				t.Helper()
				for _, path := range paths {
					rows := readReminderRows(t, srv, path)
					if len(rows) != 1 {
						t.Fatalf("rows=%d", len(rows))
					}
					assertReminderJSON(t, rows[0], "reminder_status", status)
					assertReminderJSON(t, rows[0], "reminder_count", 0)
					assertReminderJSON(t, rows[0], "reminded_at", nil)
					if status == "scheduled" {
						assertReminderJSON(t, rows[0], "next_reminder_at", confirmed.Add(15*time.Minute))
					} else {
						assertReminderJSON(t, rows[0], "next_reminder_at", nil)
					}
				}
			}
			changeTag := func(action string) {
				t.Helper()
				body := `{"action":"` + action + `","monitor_ids":[` + strconv.FormatInt(m.ID, 10) + `],"key":"env","value":"prod"}`
				preview := previewTags(t, srv, body)
				response := tagRequest(srv, "", body, preview.Header().Get("ETag"))
				if response.Code != http.StatusOK {
					t.Fatalf("bulk tags: %d %s", response.Code, response.Body.String())
				}
			}
			if scope == "tag" {
				assertState("scheduled")
				changeTag("apply")
			}
			assertState("maintenance")
			if scope == "tag" {
				changeTag("remove")
				assertState("scheduled")
				changeTag("apply")
				assertState("maintenance")
			}
			if err := db.SetMaintenancePending(t.Context(), inc.ID, true); err != nil {
				t.Fatal(err)
			}
			assertState("maintenance")
			if err := db.DeleteMaintenance(t.Context(), w.ID); err != nil {
				t.Fatal(err)
			}
			assertState("initial_pending")
			// Read a new server instance with no runner/cache: deferred intent is durable.
			fresh := New(testLogger(), db)
			testCredentialsMu.Lock()
			testCredentials[fresh] = testCredentials[srv]
			testCredentialsMu.Unlock()
			srv = fresh
			assertState("initial_pending")
			if err := db.SetMaintenancePending(t.Context(), inc.ID, false); err != nil {
				t.Fatal(err)
			}
			assertState("scheduled")
		})
	}
}

func TestMaintenanceReminderReadFailureDoesNotPromiseSchedule(t *testing.T) {
	srv, db, m, _, _ := reminderFixture(t)
	if _, err := db.Writer.ExecContext(t.Context(), "DROP TABLE maintenance_windows"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		response := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("unknown maintenance must fail read: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestMaintenanceReminderInvalidScheduleDoesNotPromiseEligibility(t *testing.T) {
	srv, db, m, _, _ := reminderFixture(t)
	// Valid JSON can still hold an invalid recurrence after storage corruption.
	// Active returns false for this timezone; that must not imply eligibility.
	_, err := db.Writer.ExecContext(t.Context(), `INSERT INTO maintenance_windows(monitor_id,spec) VALUES (?,?)`, m.ID,
		`{"name":"invalid recurrence","monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"timezone":"Invalid/Timezone","local_time":"12:00","weekdays":[1],"duration_minutes":60}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		response := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("invalid maintenance must fail read: %d %s", response.Code, response.Body.String())
		}
	}
}
