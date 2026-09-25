package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

func getDiagnostics(t *testing.T, h http.Handler, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
	}
	return rec, body
}

// The card reads the same runner as /metrics, so every pool reading has to
// arrive under its own name with the runner's value, not a default.
func TestDiagnosticsReportsThePoolAndTheDatabase(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithMetrics(fakeMetrics{m: monitor.Metrics{
		ChecksRecorded:         1482,
		HeartbeatWriteFailures: 3,
		SkippedChecks:          9,
		QueueDepth:             4,
		Workers:                16,
		Busy:                   7,
		Scheduled:              62,
	}})

	rec, body := getDiagnostics(t, authedHandler(srv), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", cc)
	}

	sch, ok := body["scheduler"].(map[string]any)
	if !ok {
		t.Fatalf("scheduler = %v, want an object when a pipeline is attached", body["scheduler"])
	}
	want := map[string]float64{
		"workers": 16, "busy": 7, "queue_depth": 4, "scheduled": 62,
		"skipped_checks": 9, "checks_recorded": 1482, "heartbeat_write_failures": 3,
	}
	for k, v := range want {
		if sch[k] != v {
			t.Errorf("scheduler.%s = %v, want %v", k, sch[k], v)
		}
	}

	dbInfo, _ := body["database"].(map[string]any)
	if dbInfo["path"] != db.Path() {
		t.Errorf("database.path = %v, want %q", dbInfo["path"], db.Path())
	}
	if dbInfo["journal_mode"] != "wal" {
		t.Errorf("database.journal_mode = %v, want wal", dbInfo["journal_mode"])
	}
	if b, _ := dbInfo["bytes"].(float64); b <= 0 {
		t.Errorf("database.bytes = %v, want the size of a migrated file", dbInfo["bytes"])
	}
	if body["go_version"] != runtime.Version() {
		t.Errorf("go_version = %v, want %s", body["go_version"], runtime.Version())
	}
	if body["platform"] != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("platform = %v", body["platform"])
	}
	if _, ok := body["started_at"].(string); !ok {
		t.Errorf("started_at = %v, want a timestamp", body["started_at"])
	}
}

// Without a pipeline the pool is unknown, and unknown must not read as the
// healthy "0 busy, queue 0".
func TestDiagnosticsWithoutAPipelineSaysSo(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec, body := getDiagnostics(t, authedHandler(srv), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if v, present := body["scheduler"]; !present || v != nil {
		t.Errorf("scheduler = %v (present %v), want an explicit null", v, present)
	}
}

// The path and runtime describe the host. A viewer or editor gets 403, and
// nobody gets anything without credentials.
func TestDiagnosticsIsForAdministrators(t *testing.T) {
	srv, db := testServerWithDB(t)
	h := srv.Handler()

	if rec, _ := getDiagnostics(t, h, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", rec.Code)
	}
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, srv, db, string(role)+"@example.com", role)
		if rec, _ := getDiagnostics(t, h, token); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", role, rec.Code)
		}
	}
}
