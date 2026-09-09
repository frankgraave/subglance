package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The monitor CRUD endpoints are the surface every user touches first. They
// were previously exercised only indirectly, which meant a broken create or a
// pause that silently did nothing would have shipped.

func post(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

func TestCreateMonitorRoundTrip(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors", `{
		"name": "site",
		"type": "http",
		"target": "https://example.com/health",
		"interval_s": 60,
		"timeout_s": 10,
		"retries": 3
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == 0 {
		t.Fatal("response carries no ID")
	}
	if got.Name != "site" || got.Target != "https://example.com/health" {
		t.Errorf("unexpected monitor: %+v", got)
	}
	// A monitor that has never been checked is pending, not down. Reporting
	// "down" for a monitor created five seconds ago is a false alarm.
	if got.Status != "pending" {
		t.Errorf("status = %q, want pending for a monitor with no checks yet", got.Status)
	}

	// And it actually landed in the database, with the threshold intact.
	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.Retries != 3 {
		t.Errorf("stored retries = %d, want 3", stored.Retries)
	}
	if !stored.Enabled {
		t.Error("a new monitor should be enabled")
	}
}

// Omitted fields must get sensible defaults rather than zeros: a monitor with
// retries=0 would alert on the first failed check, and interval 0 would be
// rejected by the schema.
func TestCreateMonitorAppliesDefaults(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"minimal","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)

	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.Retries != 2 {
		t.Errorf("default retries = %d, want 2 — a bare creation must not alert on one failure", stored.Retries)
	}
	if stored.IntervalS <= 0 {
		t.Errorf("default interval = %d, want a positive value", stored.IntervalS)
	}
	if !stored.FollowRedirects {
		t.Error("redirects should be followed by default")
	}
}

func TestCreateMonitorRejectsBadInput(t *testing.T) {
	srv, _ := testServerWithDB(t)

	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON", `{"name":`},
		{"no name", `{"type":"http","target":"https://example.com"}`},
		{"no target", `{"name":"x","type":"http"}`},
		{"no type", `{"name":"x","target":"https://example.com"}`},
		{"unknown type", `{"name":"x","type":"carrier-pigeon","target":"example.com"}`},
		{"interval too small", `{"name":"x","type":"http","target":"https://e.com","interval_s":5}`},
		{"interval too large", `{"name":"x","type":"http","target":"https://e.com","interval_s":999999}`},
		{"timeout too large", `{"name":"x","type":"http","target":"https://e.com","timeout_s":600}`},
		{"tcp without port", `{"name":"x","type":"tcp","target":"db.example.com"}`},
		{"ping with port", `{"name":"x","type":"ping","target":"example.com:80"}`},
		{"http without scheme", `{"name":"x","type":"http","target":"example.com"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(t, srv, "/api/v1/monitors", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			// The error must say what is wrong; "bad request" alone leaves
			// the user guessing.
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
				if body["error"] == "" {
					t.Error("no error message in the response")
				}
			}
		})
	}
}

func TestListMonitors(t *testing.T) {
	srv, db := testServerWithDB(t)

	for _, name := range []string{"a", "b", "c"} {
		if _, err := db.CreateMonitor(t.Context(), store.Monitor{
			Name: name, Type: "http", Target: "https://" + name + ".example.com",
			IntervalS: 60, TimeoutS: 10, Enabled: true,
		}); err != nil {
			t.Fatalf("CreateMonitor(%s): %v", name, err)
		}
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Monitors []monitorResponse `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Monitors) != 3 {
		t.Fatalf("got %d monitors, want 3", len(body.Monitors))
	}
}

// An empty list must serialise as [] rather than null: a dashboard iterating
// the response should not have to special-case a fresh install.
func TestListMonitorsEmptyIsAnArray(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))

	if !strings.Contains(rec.Body.String(), `"monitors":[]`) {
		t.Errorf("empty list should serialise as [], got: %s", rec.Body.String())
	}
}

func TestPauseAndResumeMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)

	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "pausable", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	id := strconv.FormatInt(m.ID, 10)

	rec := post(t, srv, "/api/v1/monitors/"+id+"/pause", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Enabled {
		t.Error("response says the monitor is still enabled after pausing")
	}

	// A paused monitor must actually leave the scheduler's working set.
	enabled, err := db.ListEnabledMonitors(t.Context())
	if err != nil {
		t.Fatalf("ListEnabledMonitors: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("a paused monitor is still in the enabled set: %+v", enabled)
	}

	rec = post(t, srv, "/api/v1/monitors/"+id+"/resume", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: status = %d, want 200", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Enabled {
		t.Error("response says the monitor is still paused after resuming")
	}
}

func TestDeleteMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)

	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "doomed", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	id := strconv.FormatInt(m.ID, 10)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/monitors/"+id, nil)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := db.GetMonitor(t.Context(), m.ID); err == nil {
		t.Error("the monitor is still in the database")
	}
}

func TestMonitorEndpointsRejectBadIDs(t *testing.T) {
	srv, _ := testServerWithDB(t)

	paths := []string{
		"/api/v1/monitors/abc",
		"/api/v1/monitors/0",
		"/api/v1/monitors/-1",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", p, rec.Code)
		}
	}
}

func TestGetUnknownMonitorIs404(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors/9999", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestListHeartbeats(t *testing.T) {
	srv, db := testServerWithDB(t)

	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "beating", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	now := time.Now()
	for i := range 5 {
		if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
			MonitorID: m.ID,
			TS:        now.Add(-time.Duration(i) * time.Minute),
			OK:        i%2 == 0,
			LatencyMS: 10 + i,
		}); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}
	}

	id := strconv.FormatInt(m.ID, 10)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/monitors/"+id+"/heartbeats?limit=3", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Heartbeats []struct {
			OK        bool `json:"ok"`
			LatencyMS int  `json:"latency_ms"`
		} `json:"heartbeats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Heartbeats) != 3 {
		t.Errorf("got %d heartbeats, want 3 (the limit)", len(body.Heartbeats))
	}
}

func TestListHeartbeatsRejectsBadLimit(t *testing.T) {
	srv, db := testServerWithDB(t)

	m, _ := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "x", Type: "http", Target: "https://example.com", Enabled: true,
	})
	id := strconv.FormatInt(m.ID, 10)

	for _, limit := range []string{"0", "-5", "9999", "abc"} {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/monitors/"+id+"/heartbeats?limit="+limit, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: status = %d, want 400", limit, rec.Code)
		}
	}
}
