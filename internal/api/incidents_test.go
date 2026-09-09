package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A single failed check must not paint the dashboard red. This is the API-side
// half of product principle 5 — if the UI says "down" before the alerting
// does, the two disagree and the user trusts neither.
func TestMonitorStatusPendingUntilConfirmed(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	now := time.Now()

	// One failed heartbeat plus an unconfirmed incident: pending, not down.
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{
		MonitorID: m.ID, TS: now, OK: false, StatusCode: 500, Error: "unexpected status 500",
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	if _, err := db.OpenIncident(ctx, m.ID, now, "status", "unexpected status 500"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	got := getMonitor(t, srv, m.ID)
	if got.Status != "pending" {
		t.Errorf("status after one failure = %q, want pending", got.Status)
	}
	if got.IncidentID == 0 {
		t.Error("expected the open incident to be linked from the monitor")
	}

	// Confirm it: now it is down.
	if err := db.ConfirmIncident(ctx, m.ID, now.Add(time.Minute), "status", "unexpected status 500"); err != nil {
		t.Fatalf("ConfirmIncident: %v", err)
	}

	got = getMonitor(t, srv, m.ID)
	if got.Status != "down" {
		t.Errorf("status after confirmation = %q, want down", got.Status)
	}
	if got.Error == "" {
		t.Error("a down monitor should report why")
	}

	// Resolve it: back to up.
	if _, err := db.ResolveIncident(ctx, m.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{
		MonitorID: m.ID, TS: now.Add(2 * time.Minute), OK: true, StatusCode: 200, LatencyMS: 12,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	got = getMonitor(t, srv, m.ID)
	if got.Status != "up" {
		t.Errorf("status after recovery = %q, want up", got.Status)
	}
	if got.IncidentID != 0 {
		t.Error("a recovered monitor should not link an open incident")
	}
}

func TestListOpenIncidentsEndpoint(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)

	a, _ := db.CreateMonitor(ctx, store.Monitor{
		Name: "a", Type: "http", Target: "https://a.example.com", Enabled: true,
	})
	b, _ := db.CreateMonitor(ctx, store.Monitor{
		Name: "b", Type: "http", Target: "https://b.example.com", Enabled: true,
	})

	now := time.Now()
	if _, err := db.OpenIncident(ctx, a.ID, now, "timeout", "deadline exceeded"); err != nil {
		t.Fatalf("open a: %v", err)
	}
	if _, err := db.OpenIncident(ctx, b.ID, now, "status", "500"); err != nil {
		t.Fatalf("open b: %v", err)
	}
	if _, err := db.ResolveIncident(ctx, b.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolve b: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Incidents []incidentResponse `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Incidents) != 1 {
		t.Fatalf("got %d open incidents, want 1", len(body.Incidents))
	}
	if body.Incidents[0].MonitorID != a.ID {
		t.Errorf("open incident monitor = %d, want %d", body.Incidents[0].MonitorID, a.ID)
	}
	if body.Incidents[0].Cause != "timeout" {
		t.Errorf("cause = %q, want timeout", body.Incidents[0].Cause)
	}
	if body.Incidents[0].Resolved {
		t.Error("an open incident must not report itself resolved")
	}
}

func TestMonitorIncidentHistoryEndpoint(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)

	m, _ := db.CreateMonitor(ctx, store.Monitor{
		Name: "history", Type: "http", Target: "https://example.com", Enabled: true,
	})

	base := time.Now().Add(-time.Hour)
	for i := range 3 {
		at := base.Add(time.Duration(i) * 10 * time.Minute)
		if _, err := db.OpenIncident(ctx, m.ID, at, "status", "500"); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, err := db.ResolveIncident(ctx, m.ID, at.Add(time.Minute)); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/monitors/"+strconv.FormatInt(m.ID, 10)+"/incidents", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Incidents []incidentResponse `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Incidents) != 3 {
		t.Fatalf("got %d incidents, want 3", len(body.Incidents))
	}
	for _, inc := range body.Incidents {
		if !inc.Resolved {
			t.Error("all three incidents should be resolved")
		}
		if inc.DurationS != 60 {
			t.Errorf("duration = %ds, want 60", inc.DurationS)
		}
	}
}

func TestAckIncidentEndpoint(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)

	m, _ := db.CreateMonitor(ctx, store.Monitor{
		Name: "ackable", Type: "http", Target: "https://example.com", Enabled: true,
	})
	inc, err := db.OpenIncident(ctx, m.ID, time.Now(), "status", "500")
	if err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/incidents/"+strconv.FormatInt(inc.ID, 10)+"/ack", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	got, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("OpenIncidentFor: %v", err)
	}
	if got.AckedAt.IsZero() {
		t.Error("incident should be acknowledged")
	}
	// Acknowledging is not resolving.
	if got.Resolved() {
		t.Error("acknowledging must not resolve the incident")
	}
}

func TestAckUnknownIncidentIs404(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/incidents/9999/ack", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func getMonitor(t *testing.T, srv *Server, id int64) monitorResponse {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+strconv.FormatInt(id, 10), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("get monitor %d: status = %d, body = %s", id, rec.Code, rec.Body.String())
	}

	var out monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode monitor: %v", err)
	}
	return out
}
