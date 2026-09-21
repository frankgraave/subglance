package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

// The fake prober and runner-facet unit tests are not enough to prove the join:
// use the real checker -> recorder -> engine -> authenticated HTTP read path.
func TestIncidentReminderRealRunnerSuppressionReachesHTTP(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer target.Close()
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "real runner", Type: "http", Target: target.URL,
		Enabled: true, IntervalS: 86400, TimeoutS: 5, Retries: 1, RepeatAfterS: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := monitor.New(monitor.Options{
		DB: db, Log: testLogger(), AllowPrivateTargets: true,
		FlapWindow: 10 * time.Minute, FlapThreshold: 3,
	})
	// This is the same composition used by cmd/subglance, not an API-only seam.
	srv.WithProber(runner)
	for _, code := range []int{503, 200, 503} {
		status.Store(int32(code))
		result, err := runner.CheckNow(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		if result.StatusCode != code {
			t.Fatalf("real checker got %d, want %d", result.StatusCode, code)
		}
	}
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents?limit=1"} {
		rows := readReminderRows(t, srv, path)
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want current outage", len(rows))
		}
		assertReminderJSON(t, rows[0], "confirmed", true)
		assertReminderJSON(t, rows[0], "reminder_status", "flapping")
		assertReminderJSON(t, rows[0], "next_reminder_at", nil)
		assertReminderJSON(t, rows[0], "reminder_count", 0)
		assertReminderJSON(t, rows[0], "reminded_at", nil)
	}
	beats, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(beats) != 3 {
		t.Fatalf("metadata reads changed heartbeat history: got %d, want 3", len(beats))
	}
}
