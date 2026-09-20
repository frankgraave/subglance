package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func TestThreeSnapshotsKeepEvolvingSymptomsAcrossRestart(t *testing.T) {
	// A synthetic five-probe outage: three distinct initial causes followed by
	// one repeated and one late cause. The late cause is deliberately lost; the
	// allowance is a prefix of the outage, not a sampling or deduplication rule.
	symptoms := []string{"connecting to database", "database overloaded", "retry queue exhausted", "retry queue exhausted", "late disk failure"}
	next := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", fmt.Sprintf("synthetic-%d", next))
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, symptoms[next])
		next++
	}))
	defer target.Close()
	db := testDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "synthetic evolving outage", Type: "http", Target: target.URL, Enabled: true, CaptureResponse: true, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	r := recordingRunner(t, db)
	for i := range symptoms {
		if i == 2 {
			r = recordingRunner(t, db)
			if err := r.restore(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.CheckNow(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	reasons, err := db.HeartbeatCaptureReasons(t.Context(), hbs)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	suppressed := 0
	for _, hb := range hbs {
		if hb.Response != nil {
			got[hb.Response.Body] = true
		} else if reasons[hb.ID] == store.CaptureBudget {
			suppressed++
		}
	}
	if len(got) != 3 || suppressed != 2 {
		t.Fatalf("captured symptoms=%v suppressed=%d, want first three and two budget reasons", got, suppressed)
	}
	for _, symptom := range symptoms[:3] {
		if !got[symptom] {
			t.Errorf("early symptom %q lost across restart", symptom)
		}
	}
	if got["late disk failure"] {
		t.Fatal("late symptom exceeded the retained allowance")
	}
}

func TestRepeatedSettledOutagesAreNotATotalByteCap(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// Enough quiet time for the engine's default ten-minute window to clear.
	// Seven outages may legitimately keep 21 snapshots: documenting only 6 KiB
	// per monitor would understate retained bytes by an arbitrary factor.
	for outage := range 7 {
		atTime := start.Add(time.Duration(outage) * time.Hour)
		for failure := range 5 {
			if err := r.recordOutcome(at(failureWithBody(m, strings.Repeat("x", 2048)), atTime.Add(time.Duration(failure)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.recordOutcome(at(outcomeFor(m, m.Target, true), atTime.Add(5*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	var snapshots, bytes int
	if err := db.Reader.QueryRow("SELECT count(*), sum(length(CAST(body AS BLOB))) FROM heartbeat_responses").Scan(&snapshots, &bytes); err != nil {
		t.Fatal(err)
	}
	if snapshots != 21 || bytes != 43008 {
		t.Fatalf("seven settled outages stored %d snapshots / %d bytes, want 21 / 43008", snapshots, bytes)
	}
}
