package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The snapshot is the one part of the API that can echo back text SubGlance
// did not produce, and the one monitor field that governs what this instance
// stores about someone else's service. Both deserve a pinned wire contract.

type heartbeatsBody struct {
	Heartbeats []struct {
		OK       bool `json:"ok"`
		Response *struct {
			Body      string            `json:"body"`
			Headers   map[string]string `json:"headers"`
			Truncated bool              `json:"truncated"`
		} `json:"response"`
	} `json:"heartbeats"`
}

func getHeartbeats(t *testing.T, srv *Server, monitorID int64) heartbeatsBody {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+itoa(monitorID)+"/heartbeats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got heartbeatsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return got
}

func TestHeartbeatsExposeTheCapturedResponse(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "captures", Type: "http", Target: "https://example.com/health",
		Enabled: true, CaptureResponse: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: time.Now().UTC(), OK: false,
		StatusCode: 503, Error: "status 503, expected 200-299",
		Response: &store.ResponseSnapshot{
			Body:      `{"detail":"upstream database timeout"}`,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Truncated: true,
		},
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	got := getHeartbeats(t, srv, m.ID)
	if len(got.Heartbeats) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(got.Heartbeats))
	}
	resp := got.Heartbeats[0].Response
	if resp == nil {
		t.Fatal("the captured response was not returned")
	}
	if resp.Body != `{"detail":"upstream database timeout"}` {
		t.Errorf("body = %q", resp.Body)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Errorf("headers = %v", resp.Headers)
	}
	if !resp.Truncated {
		t.Error("truncated was not carried to the wire")
	}
}

// A heartbeat with no snapshot must omit the field entirely, so a response for
// a monitor that never captures anything is byte for byte what it always was.
func TestHeartbeatWithoutSnapshotOmitsTheField(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "plain", Type: "http", Target: "https://example.com/", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: time.Now().UTC(), OK: true, LatencyMS: 12,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+itoa(m.ID)+"/heartbeats", nil))

	var raw struct {
		Heartbeats []map[string]json.RawMessage `json:"heartbeats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw.Heartbeats) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(raw.Heartbeats))
	}
	if _, present := raw.Heartbeats[0]["response"]; present {
		t.Error("a heartbeat with no snapshot carried a response field")
	}
}

// Creating a monitor without an opinion turns capture on: a capture nobody
// enabled is one that is absent on the night it is needed.
func TestCreateDefaultsCaptureOn(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"default","type":"http","target":"https://example.com"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got struct {
		ID              int64 `json:"id"`
		CaptureResponse bool  `json:"capture_response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.CaptureResponse {
		t.Error("a monitor created without an opinion has capture off")
	}
}

// Opting out at create time must be honoured and must survive a read. This is
// the switch that keeps a target's responses out of the database, so a write
// path that ignored it would be a privacy failure rather than a display bug.
func TestCreateHonoursCaptureOptOut(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"sensitive","type":"http","target":"https://example.com","capture_response":false}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID              int64 `json:"id"`
		CaptureResponse bool  `json:"capture_response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.CaptureResponse {
		t.Fatal("capture_response:false was ignored on create")
	}

	stored, err := db.GetMonitor(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.CaptureResponse {
		t.Error("the opt-out did not reach the database")
	}
}

// Turning capture off on an existing monitor is the action an operator takes
// after realising a target answers with something sensitive. It has to work on
// a monitor that already has capture on.
func TestPatchTurnsCaptureOff(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "regrets", Type: "http", Target: "https://example.com/",
		Enabled: true, CaptureResponse: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch,
		"/api/v1/monitors/"+itoa(m.ID), `{"capture_response":false}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.CaptureResponse {
		t.Error("capture is still on after patching it off")
	}
}

// A patch that says nothing about capture must not change it. PATCH semantics
// are the whole reason the field is a pointer, and getting this wrong would
// silently re-enable capture on a monitor that opted out.
func TestPatchLeavesCaptureAloneWhenUnmentioned(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "untouched", Type: "http", Target: "https://example.com/",
		Enabled: true, CaptureResponse: false,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch,
		"/api/v1/monitors/"+itoa(m.ID), `{"name":"renamed"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.CaptureResponse {
		t.Error("a rename switched response capture back on")
	}
}
