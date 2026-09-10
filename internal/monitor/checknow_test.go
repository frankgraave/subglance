package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// These cover the seam the API test necessarily fakes: that a manual check
// really does reuse the scheduler's checkers and really does go through the
// recording path.

func TestCheckNowRecordsHeartbeatForEnabledMonitor(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger(), AllowPrivateTargets: true})

	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "manual", Type: "http", Target: target.URL,
		// An interval far longer than the test: if a heartbeat appears, the
		// manual check put it there, not the schedule.
		IntervalS: 86400, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	res, err := r.CheckNow(context.Background(), m)
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if !res.OK || res.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want a passing 200", res)
	}

	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 10)
	if err != nil {
		t.Fatalf("list heartbeats: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats, want 1: a manual check on an enabled monitor must count", len(hbs))
	}
	if !hbs[0].OK {
		t.Error("recorded heartbeat is not OK")
	}
}

func TestCheckNowLeavesPausedMonitorHistoryAlone(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger(), AllowPrivateTargets: true})

	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "paused", Type: "http", Target: target.URL,
		IntervalS: 86400, TimeoutS: 5, Retries: 1, Enabled: false,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	res, err := r.CheckNow(context.Background(), m)
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if !res.OK {
		t.Fatalf("result = %+v, want a passing check: a paused monitor must still be probed", res)
	}

	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 10)
	if err != nil {
		t.Fatalf("list heartbeats: %v", err)
	}
	if len(hbs) != 0 {
		t.Errorf("got %d heartbeats for a paused monitor, want 0", len(hbs))
	}
}

func TestCheckNowRejectsUnknownType(t *testing.T) {
	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger(), AllowPrivateTargets: true})

	// Bypass the store so the invalid type survives the CHECK constraint:
	// the point is that CheckNow itself refuses rather than panicking on a
	// missing map entry.
	_, err := r.CheckNow(context.Background(), store.Monitor{
		ID: 1, Name: "weird", Type: "carrier-pigeon", Target: "x",
		TimeoutS: 1, Enabled: true,
	})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("error = %v, want ErrUnsupportedType", err)
	}
}

func TestCheckNowFailureOpensIncidentWhenThresholdIsOne(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	db := testDB(t)
	r := New(Options{DB: db, Log: quietLogger(), AllowPrivateTargets: true})

	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "failing", Type: "http", Target: target.URL,
		IntervalS: 86400, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	res, err := r.CheckNow(context.Background(), m)
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if res.OK {
		t.Fatal("result is OK, want a failure against a 500")
	}

	// The whole argument for recording manual checks is that they count.
	// If they did not reach the state engine, a manual check could never
	// resolve an incident either — which is the case people actually use.
	open, err := db.ListOpenIncidents(context.Background())
	if err != nil {
		t.Fatalf("list open incidents: %v", err)
	}
	if len(open) != 1 || open[0].MonitorID != m.ID {
		t.Fatalf("open incidents = %+v, want exactly one for monitor %d", open, m.ID)
	}
}
