package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A monitor whose certificate notice is open reads as "expiring" while its
// checks pass, never as "down"; a failure that is not confirmed yet is still
// the heartbeat's warning. The incident it links to says it is a notice.
func TestAnOpenNoticeReadsAsExpiring(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "tls", Type: "ssl", Target: "api.example.com:443", Enabled: true})
	opened := time.Now().Add(-time.Hour).Truncate(time.Second)
	notice, err := db.OpenNotice(t.Context(), m.ID, opened, "cert_expiry", "certificate expires in 6 days")
	if err != nil {
		t.Fatal(err)
	}

	read := func() map[string]any {
		t.Helper()
		rec := getMonitorRaw(t, srv, m.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: opened, OK: true, Assessment: "up"}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got["status"] != "expiring" || got["error"] != "certificate expires in 6 days" || got["failure_kind"] != "cert_expiry" {
		t.Errorf("with a passing check: status %v error %v kind %v, want expiring with the notice's text", got["status"], got["error"], got["failure_kind"])
	}
	if got["incident_id"] != float64(notice.ID) {
		t.Errorf("incident_id = %v, want %d", got["incident_id"], notice.ID)
	}

	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: opened.Add(time.Minute), OK: false, Assessment: "warning",
		Error: "dial tcp: connection refused", FailureKind: "connection",
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got["status"] != "warning" {
		t.Errorf("with an unconfirmed failure: status %v, want warning", got["status"])
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10)+"/incidents", nil)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	var incidents struct {
		Incidents []map[string]any `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &incidents); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(incidents.Incidents) != 1 || incidents.Incidents[0]["notice"] != true || incidents.Incidents[0]["confirmed"] != true {
		t.Errorf("incidents = %s, want one confirmed notice", rec.Body.String())
	}
}
