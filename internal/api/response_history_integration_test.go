package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Integration seam: this test is applied together with the parent-owned route
// edit, not skipped behind an environment flag in the unit suite.
func TestResponseHistoryRawRoute(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "raw history", Type: "http", Target: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, reason := range []store.CaptureReason{"", store.CaptureDisabled, store.CaptureFlapping} {
		if err := db.RecordHeartbeatWithCaptureReason(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: time.Unix(int64(i), 0)}, reason); err != nil {
			t.Fatal(err)
		}
	}
	url := "/api/v1/monitors/" + itoa(m.ID) + "/heartbeats"
	unauth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, url, nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed history status = %d", unauth.Code)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Heartbeats []map[string]any `json:"heartbeats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Heartbeats) != 3 {
		t.Fatalf("heartbeat count = %d", len(body.Heartbeats))
	}
	for i, want := range []any{"flapping", "disabled", nil} {
		if got := body.Heartbeats[i]["response_capture_reason"]; got != want {
			t.Errorf("raw route heartbeat[%d] reason = %v, want %v", i, got, want)
		}
	}
	// A failed reason read is not a successful page of unexplained history.
	if _, err := db.Writer.ExecContext(t.Context(), "ALTER TABLE heartbeats DROP COLUMN response_capture_reason"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reason read failure status = %d, want 500", rec.Code)
	}
}
