package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestResponseHistoryUsesStoredReasonsAndLeavesLegacyUnknown(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "history", Type: "http", Target: "https://example.com", Enabled: true, CaptureResponse: false})
	if err != nil {
		t.Fatal(err)
	}
	for i, reason := range []store.CaptureReason{"", store.CaptureDisabled, store.CaptureFlapping, store.CaptureBudget} {
		err := db.RecordHeartbeatWithCaptureReason(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: time.Unix(int64(i), 0)}, reason)
		if err != nil {
			t.Fatal(err)
		}
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	out, err := srv.describeResponseHistory(t.Context(), hbs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire []map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for i, want := range []any{"budget", "flapping", "disabled", nil} {
		if got := wire[i]["response_capture_reason"]; got != want {
			t.Errorf("history[%d] reason = %v, want %v", i, got, want)
		}
		if _, ok := wire[i]["response"]; ok {
			t.Errorf("invented response for missing snapshot: %v", wire[i])
		}
	}
	// The shared monitor serializer is deliberately unchanged.
	bulk, err := json.Marshal(describeHeartbeat(hbs[0]))
	if err != nil {
		t.Fatal(err)
	}
	var bulkWire map[string]any
	if err := json.Unmarshal(bulk, &bulkWire); err != nil {
		t.Fatal(err)
	}
	if _, ok := bulkWire["response_capture_reason"]; ok {
		t.Fatal("reason leaked into bulk heartbeat payload")
	}
}
