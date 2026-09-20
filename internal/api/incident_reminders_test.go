package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

type reminderStateProbe struct{ suppressed map[int64]bool }

func (p *reminderStateProbe) Flapping(id int64) bool { return p.suppressed[id] }
func (*reminderStateProbe) CheckNow(context.Context, store.Monitor) (checker.Result, error) {
	panic("a reminder metadata read must never perform a check")
}

func TestIncidentReminderUsesLiveSuppressionWithoutAdvancingClock(t *testing.T) {
	srv, db, m, inc, confirmed := reminderFixture(t)
	probe := &reminderStateProbe{suppressed: map[int64]bool{m.ID: true}}
	srv.WithProber(probe)
	last := confirmed.Add(15 * time.Minute)
	if err := db.RecordReminder(t.Context(), inc.ID, last); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		rows := readReminderRows(t, srv, path)
		assertReminderJSON(t, rows[0], "reminder_status", "flapping")
		assertReminderJSON(t, rows[0], "next_reminder_at", nil)
		assertReminderJSON(t, rows[0], "reminder_count", 1)
		assertReminderJSON(t, rows[0], "reminded_at", last)
	}
	probe.suppressed[m.ID] = false
	rows := readReminderRows(t, srv, "/api/v1/incidents")
	assertReminderJSON(t, rows[0], "reminder_status", "scheduled")
	assertReminderJSON(t, rows[0], "next_reminder_at", last.Add(time.Hour))
	assertReminderJSON(t, rows[0], "reminder_count", 1)
	assertReminderJSON(t, rows[0], "reminded_at", last)
}

func reminderFixture(t *testing.T) (*Server, *store.DB, store.Monitor, store.Incident, time.Time) {
	t.Helper()
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "reminders", Type: "http", Target: "https://example.com", Enabled: true,
		IntervalS: 60, TimeoutS: 5, Retries: 1, RepeatAfterS: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	inc, err := db.OpenIncident(t.Context(), m.ID, started, "status", "HTTP 503")
	if err != nil {
		t.Fatal(err)
	}
	confirmed := started.Add(2 * time.Minute)
	if err := db.ConfirmIncident(t.Context(), m.ID, confirmed, "status", "HTTP 503"); err != nil {
		t.Fatal(err)
	}
	return srv, db, m, inc, confirmed
}

func readReminderRows(t *testing.T, srv *Server, path string) []map[string]json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Incidents []map[string]json.RawMessage `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Incidents
}

func assertReminderJSON(t *testing.T, row map[string]json.RawMessage, key string, want any) {
	t.Helper()
	raw, exists := row[key]
	if !exists {
		t.Fatalf("missing %s in incident response", key)
	}
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(expected) {
		t.Errorf("%s = %s, want %s", key, raw, expected)
	}
}

func TestIncidentReminderInactiveStatesKeepHistoryButNoDueTime(t *testing.T) {
	for _, tc := range []struct {
		name, status                        string
		confirmed, acked, resolved, enabled bool
		repeat                              int
	}{
		{"unconfirmed", "unconfirmed", false, false, false, true, 900},
		{"acknowledged", "acknowledged", true, true, false, true, 900},
		{"resolved beats acknowledged", "resolved", true, true, true, true, 900},
		{"paused", "paused", true, false, false, false, 900},
		{"disabled", "disabled", true, false, false, true, 0},
		{"acked beats paused", "acknowledged", true, true, false, false, 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, m, inc, confirmed := reminderFixture(t)
			last := confirmed.Add(15 * time.Minute)
			if err := db.RecordReminder(t.Context(), inc.ID, last); err != nil {
				t.Fatal(err)
			}
			m.Enabled, m.RepeatAfterS = tc.enabled, tc.repeat
			if _, err := db.UpdateMonitor(t.Context(), m); err != nil {
				t.Fatal(err)
			}
			if !tc.confirmed {
				if _, err := db.Writer.ExecContext(t.Context(), "UPDATE incidents SET confirmed_at = NULL WHERE id = ?", inc.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.acked {
				if err := db.AckIncident(t.Context(), inc.ID, last.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.resolved {
				if _, err := db.ResolveIncident(t.Context(), m.ID, last.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			paths := []string{"/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"}
			if !tc.resolved {
				paths = append(paths, "/api/v1/incidents")
			}
			for _, path := range paths {
				rows := readReminderRows(t, srv, path)
				if len(rows) != 1 {
					t.Fatalf("rows = %d", len(rows))
				}
				assertReminderJSON(t, rows[0], "reminder_status", tc.status)
				assertReminderJSON(t, rows[0], "next_reminder_at", nil)
				assertReminderJSON(t, rows[0], "reminder_count", 1)
				assertReminderJSON(t, rows[0], "reminded_at", last)
			}
		})
	}
}

func TestIncidentReminderScheduleSurvivesReloadAndSettingChanges(t *testing.T) {
	srv, db, m, inc, confirmed := reminderFixture(t)
	last := confirmed
	for n, gap := range []time.Duration{time.Hour, 4 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		last = last.Add(17 * time.Minute)
		if err := db.RecordReminder(t.Context(), inc.ID, last); err != nil {
			t.Fatal(err)
		}
		// A new API instance has no reminder cache to reconstruct or reset.
		fresh := New(testLogger(), db)
		for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			// Handler authorization is exercised by the ordinary read helper;
			// invoke this replacement server's handler with that same valid token.
			testCredentialsMu.Lock()
			request.Header.Set("Authorization", "Bearer "+testCredentials[srv])
			testCredentialsMu.Unlock()
			fresh.Handler().ServeHTTP(rec, request)
			if rec.Code != http.StatusOK {
				t.Fatalf("reloaded API: %d %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Incidents []map[string]json.RawMessage `json:"incidents"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Incidents) != 1 {
				t.Fatalf("rows = %d", len(body.Incidents))
			}
			assertReminderJSON(t, body.Incidents[0], "reminder_count", n+1)
			assertReminderJSON(t, body.Incidents[0], "reminded_at", last)
			assertReminderJSON(t, body.Incidents[0], "next_reminder_at", last.Add(gap))
		}
	}
	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10)
	for _, seconds := range []int{0, 77, 86400} {
		rec := patch(t, srv, path, `{"repeat_after_s":`+strconv.Itoa(seconds)+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch repeat: %d %s", rec.Code, rec.Body.String())
		}
		rows := readReminderRows(t, srv, path+"/incidents")
		if seconds == 0 {
			assertReminderJSON(t, rows[0], "reminder_status", "disabled")
			assertReminderJSON(t, rows[0], "next_reminder_at", nil)
		} else {
			assertReminderJSON(t, rows[0], "reminder_status", "scheduled")
			// An arbitrary legal interval remains exact; the longer base caps.
			gap := 24 * time.Hour
			if seconds == 77 {
				gap = 78848 * time.Second
			}
			assertReminderJSON(t, rows[0], "next_reminder_at", last.Add(gap))
		}
		assertReminderJSON(t, rows[0], "reminder_count", 5)
	}
}

func TestIncidentReminderFirstDueUsesConfirmationAndExplicitZero(t *testing.T) {
	srv, _, m, _, confirmed := reminderFixture(t)
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		t.Run(path, func(t *testing.T) {
			rows := readReminderRows(t, srv, path)
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(rows))
			}
			assertReminderJSON(t, rows[0], "reminder_count", 0)
			assertReminderJSON(t, rows[0], "reminded_at", nil)
			assertReminderJSON(t, rows[0], "next_reminder_at", confirmed.Add(15*time.Minute))
			assertReminderJSON(t, rows[0], "reminder_status", "scheduled")
		})
	}
}
