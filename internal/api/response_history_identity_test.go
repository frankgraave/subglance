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

func TestResponseHistoryRawIdentitySurvivesRefetchAndSameSecondInsert(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "identity", Type: "http", Target: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Unix(1_700_000_000, 0)
	record := func() {
		t.Helper()
		if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: ts, Response: &store.ResponseSnapshot{Body: "same response"}}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() []map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+itoa(m.ID)+"/heartbeats", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("history status = %d", rec.Code)
		}
		var body struct {
			Heartbeats []map[string]any `json:"heartbeats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		stored, err := db.ListHeartbeats(t.Context(), m.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(body.Heartbeats) != len(stored) {
			t.Fatalf("wire/store count differs")
		}
		for i, hb := range stored {
			if got, want := body.Heartbeats[i]["id"], strconv.FormatInt(hb.ID, 10); got != want {
				t.Fatalf("raw heartbeat identity = %v, want persisted decimal string %q", got, want)
			}
		}
		return body.Heartbeats
	}
	record()
	first := read()[0]["id"]
	if got := read()[0]["id"]; got != first {
		t.Fatalf("identity changed on refetch: %v -> %v", first, got)
	}
	record()
	rows := read()
	if len(rows) != 2 || rows[0]["id"] == rows[1]["id"] {
		t.Fatalf("same-second checks need distinct identities: %v", rows)
	}
	if rows[1]["id"] != first {
		t.Fatal("existing identity did not follow the prepended same-second check")
	}
	// Identity is independent of response capture and failure status, including
	// records made through the legacy writer without capture-reason metadata.
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: ts.Add(time.Second), OK: true}); err != nil {
		t.Fatal(err)
	}
	rows = read()
	if len(rows) != 3 || rows[0]["ok"] != true || rows[0]["response"] != nil || rows[0]["response_capture_reason"] != nil {
		t.Fatal("successful legacy heartbeat did not retain its raw identity contract")
	}
	// The dashboard's bulk route must stay small and must not acquire raw
	// diagnostics just because the detail serializer now exposes identity.
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors?heartbeats=100", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk history status = %d", rec.Code)
	}
	var bulk struct {
		Monitors []struct {
			Heartbeats []map[string]any `json:"heartbeats"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bulk); err != nil {
		t.Fatal(err)
	}
	if len(bulk.Monitors) != 1 || len(bulk.Monitors[0].Heartbeats) != 3 {
		t.Fatal("bulk fixture did not return all checks")
	}
	for _, hb := range bulk.Monitors[0].Heartbeats {
		for _, field := range []string{"id", "response", "response_capture_reason"} {
			if _, exists := hb[field]; exists {
				t.Errorf("raw field %s leaked into bulk heartbeat", field)
			}
		}
	}
}

func TestResponseHistoryIdentityPreservesInt64Precision(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rows, err := srv.describeResponseHistory(t.Context(), []store.Heartbeat{{ID: 9007199254740993}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var wire []map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire[0]["id"] != "9007199254740993" {
		t.Fatalf("id lost precision or is absent: %v", wire[0]["id"])
	}
}
