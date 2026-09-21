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
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("cross-origin %s returned %d", method, rec.Code)
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
