package api

import (
	"encoding/json"
	"fmt"
	"github.com/frankgraave/subglance/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceCreateListCancel(t *testing.T) {
	s, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	body := fmt.Sprintf(`{"name":"Deploy","monitor_id":%d,"starts_at":%q,"ends_at":%q}`, id, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339))
	rec := httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/maintenance", strings.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("create maintenance = %d: %s", rec.Code, rec.Body.String())
	}
	var created store.MaintenanceWindow
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/maintenance", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Deploy"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/maintenance/%d", created.ID), nil))
	if rec.Code != 204 {
		t.Fatalf("cancel %d %s", rec.Code, rec.Body)
	}
	rows, err := db.ListMaintenance(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("cancel not persisted: %+v %v", rows, err)
	}
}

func TestMaintenanceAuthorizationAndValidation(t *testing.T) {
	s, db := testServerWithDB(t)
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, s, db, string(role)+"@example.invalid", role)
		for _, method := range []string{"POST", "DELETE"} {
			path := "/api/v1/maintenance"
			if method == "DELETE" {
				path += "/999"
			}
			for _, authed := range []bool{false, true} {
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				if authed {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, req)
				want := 401
				if authed {
					want = 403
					if role == store.RoleEditor {
						want = 400
						if method == "DELETE" {
							want = 404
						}
					}
				}
				if rec.Code != want {
					t.Fatalf("%s %s auth %v: %d want %d", role, method, authed, rec.Code, want)
				}
			}
		}
	}
	id := seedUptimeMonitor(t, db)
	base := fmt.Sprintf(`"name":"Deploy","monitor_id":%d`, id)
	for _, body := range []string{
		`{` + base + `,"starts_at":"2026-10-01T10:00:00Z","ends_at":"2026-10-01T10:00:00Z"}`,
		`{` + base + `,"timezone":"Mars/Olympus","weekdays":[0],"local_time":"02:00","duration_minutes":30}`,
		`{` + base + `,"timezone":"UTC","weekdays":[0,0],"local_time":"02:00","duration_minutes":30}`,
		`{` + base + `,"timezone":"UTC","weekdays":[7],"local_time":"02:00","duration_minutes":30}`,
		`{` + base + `,"timezone":"UTC","weekdays":[0],"local_time":"25:00","duration_minutes":30}`,
		`{` + base + `,"timezone":"UTC","weekdays":[0],"local_time":"02:00","duration_minutes":1441}`,
		`{` + base + `,"tag_key":"env","tag_value":"prod","starts_at":"2026-10-01T10:00:00Z","ends_at":"2026-10-01T11:00:00Z"}`,
		`{` + base + `,"unknown":true}`, `{` + base + `,"id":12}`,
	} {
		rec := httptest.NewRecorder()
		authedHandler(s).ServeHTTP(rec, jsonRequest("POST", "/api/v1/maintenance", body))
		if rec.Code != 400 {
			t.Fatalf("invalid accepted %s: %d %s", body, rec.Code, rec.Body)
		}
	}
}
func TestMaintenanceUptimeNullAndHistory(t *testing.T) {
	s, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{MonitorID: id, TS: time.Now(), Assessment: "down", Maintenance: true}); err != nil {
		t.Fatal(err)
	}
	rec := getUptime(t, s, fmt.Sprintf("/api/v1/monitors/%d/uptime", id))
	var body uptimeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, w := range body.Windows {
		if w.Uptime != nil || w.Total != 0 || w.Maintenance != 1 {
			t.Fatalf("maintenance-only window %+v", w)
		}
	}
	rec = httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/monitors/%d/heartbeats", id), nil))
	if !strings.Contains(rec.Body.String(), `"maintenance":true`) {
		t.Fatalf("history lost exclusion: %s", rec.Body)
	}
}

func TestMaintenanceRejectsCrossOriginSessionWrites(t *testing.T) {
	s, db := testServerWithDB(t)
	user, err := db.CreateUser(t.Context(), "maintenance-session@example.invalid", "correct-horse-battery-staple", store.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(t.Context(), user.ID, "fixture", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest("GET", "/api/v1/maintenance", nil)
	read.AddCookie(&http.Cookie{Name: "subglance_session", Value: session})
	readRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(readRec, read)
	if readRec.Code != 200 {
		t.Fatalf("session fixture was not authenticated: %d", readRec.Code)
	}
	for _, method := range []string{"POST", "DELETE"} {
		path := "/api/v1/maintenance"
		if method == "DELETE" {
			path += "/1"
		}
		req := jsonRequest(method, path, `{}`)
		req.AddCookie(&http.Cookie{Name: "subglance_session", Value: session})
		req.Header.Set("Origin", "https://other.example.invalid")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s returned %d, want 403", method, rec.Code)
		}
	}
}

func TestMaintenanceInvalidDeleteID(t *testing.T) {
	s, _ := testServerWithDB(t)
	for _, id := range []string{"bad", "0", "-1"} {
		rec := httptest.NewRecorder()
		authedHandler(s).ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/maintenance/"+id, nil))
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid maintenance id") {
			t.Fatalf("delete %s: %d %s", id, rec.Code, rec.Body)
		}
	}
}

// TestMaintenanceWeeklyAnswerHasNoAbsoluteTimes: a weekly window has no
// absolute start or end, and its answer says so by leaving both fields out.
// Year-1 timestamps in their place are a valid date that a client comparing
// ends_at with now reads as a window that ended long ago. A one-off window
// keeps both, and a client that echoes a weekly read back, year-1 values
// included, is still accepted.
func TestMaintenanceWeeklyAnswerHasNoAbsoluteTimes(t *testing.T) {
	s, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	weekly := `{"name":"Weekly deploy","tag_key":"env","tag_value":"prod","timezone":"Europe/Amsterdam","weekdays":[0],"local_time":"02:30","duration_minutes":60}`
	echoed := `{"name":"Echoed","tag_key":"env","tag_value":"prod","starts_at":"0001-01-01T00:00:00Z","ends_at":"0001-01-01T00:00:00Z","timezone":"Europe/Amsterdam","weekdays":[0],"local_time":"02:30","duration_minutes":60}`
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	oneOff := fmt.Sprintf(`{"name":"Deploy","monitor_id":%d,"starts_at":%q,"ends_at":%q}`, id,
		start.Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339))

	fields := func(t *testing.T, raw []byte) map[string]json.RawMessage {
		t.Helper()
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return out
	}
	for name, body := range map[string]string{"weekly": weekly, "echoed": echoed, "one-off": oneOff} {
		rec := httptest.NewRecorder()
		authedHandler(s).ServeHTTP(rec, jsonRequest("POST", "/api/v1/maintenance", body))
		if rec.Code != 201 {
			t.Fatalf("create %s = %d: %s", name, rec.Code, rec.Body)
		}
		got := fields(t, rec.Body.Bytes())
		_, hasStart := got["starts_at"]
		_, hasEnd := got["ends_at"]
		if wantTimes := name == "one-off"; hasStart != wantTimes || hasEnd != wantTimes {
			t.Errorf("create %s answered starts_at %v, ends_at %v, want both %v: %s",
				name, hasStart, hasEnd, wantTimes, rec.Body)
		}
	}

	// A weekly window stored by an earlier version keeps the year-1 values in
	// its spec; reading it back must not bring them into the answer.
	if _, err := db.Writer.ExecContext(t.Context(), `INSERT INTO maintenance_windows(monitor_id,spec) VALUES (NULL,?)`,
		`{"id":0,"name":"Stored earlier","tag_key":"env","tag_value":"prod","starts_at":"0001-01-01T00:00:00Z","ends_at":"0001-01-01T00:00:00Z","timezone":"UTC","weekdays":[1],"local_time":"03:00","duration_minutes":30,"created_at":"2026-10-01T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/maintenance", nil))
	if rec.Code != 200 {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body)
	}
	var list struct {
		Maintenance []json.RawMessage `json:"maintenance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Maintenance) != 4 {
		t.Fatalf("list: %v %s", err, rec.Body)
	}
	for _, raw := range list.Maintenance {
		got := fields(t, raw)
		_, weeklyWindow := got["timezone"]
		_, hasStart := got["starts_at"]
		_, hasEnd := got["ends_at"]
		if hasStart == weeklyWindow || hasEnd == weeklyWindow {
			t.Errorf("listed window (weekly %v) has starts_at %v, ends_at %v: %s",
				weeklyWindow, hasStart, hasEnd, raw)
		}
		if !weeklyWindow && string(got["starts_at"]) != fmt.Sprintf("%q", start.Format(time.RFC3339)) {
			t.Errorf("one-off starts_at = %s, want %s", got["starts_at"], start.Format(time.RFC3339))
		}
	}
}
