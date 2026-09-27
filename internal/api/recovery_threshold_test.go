package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// recovery_threshold decides how many passing checks in a row close a
// confirmed incident. The engine side is tested in internal/state; these tests
// pin the API contract: the default, the round trip, and the range.

func TestCreateMonitorDefaultsRecoveryThresholdToTwo(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"minimal","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.RecoveryThreshold != 2 {
		t.Errorf("stored recovery_threshold = %d, want 2", stored.RecoveryThreshold)
	}
}

func TestCreateMonitorKeepsExplicitRecoveryThreshold(t *testing.T) {
	srv, db := testServerWithDB(t)

	for _, n := range []int{1, 5, 10} {
		body := `{"name":"x","type":"http","target":"https://example.com","recovery_threshold":` + itoa(int64(n)) + `}`
		rec := post(t, srv, "/api/v1/monitors", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("recovery_threshold %d: status = %d, want 201: %s", n, rec.Code, rec.Body.String())
		}
		var got monitorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		stored, err := db.GetMonitor(t.Context(), got.ID)
		if err != nil {
			t.Fatalf("GetMonitor: %v", err)
		}
		if stored.RecoveryThreshold != n {
			t.Errorf("stored recovery_threshold = %d, want %d", stored.RecoveryThreshold, n)
		}
	}
}

// 0 is refused rather than read as "unset": the store default would turn it
// into 2, and the caller would be told it got something it did not ask for.
func TestCreateMonitorRejectsRecoveryThresholdOutOfRange(t *testing.T) {
	srv, _ := testServerWithDB(t)

	for _, n := range []int{0, -1, 11} {
		body := `{"name":"x","type":"http","target":"https://example.com","recovery_threshold":` + itoa(int64(n)) + `}`
		rec := post(t, srv, "/api/v1/monitors", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("recovery_threshold %d: status = %d, want 400: %s", n, rec.Code, rec.Body.String())
			continue
		}
		assertProblemField(t, rec, "recovery_threshold")
	}
}

func TestPatchMonitorRecoveryThreshold(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})

	rec := patch(t, srv, monitorPath(m.ID), `{"recovery_threshold":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryThreshold != 4 {
		t.Errorf("stored recovery_threshold = %d, want 4", stored.RecoveryThreshold)
	}

	// A rename must not touch it.
	if rec := patch(t, srv, monitorPath(m.ID), `{"name":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if stored, _ = db.GetMonitor(t.Context(), m.ID); stored.RecoveryThreshold != 4 {
		t.Errorf("after rename recovery_threshold = %d, want 4", stored.RecoveryThreshold)
	}

	for _, bad := range []string{`{"recovery_threshold":0}`, `{"recovery_threshold":11}`} {
		rec := patch(t, srv, monitorPath(m.ID), bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, rec.Code)
			continue
		}
		assertProblemField(t, rec, "recovery_threshold")
	}
	if stored, _ = db.GetMonitor(t.Context(), m.ID); stored.RecoveryThreshold != 4 {
		t.Errorf("a refused PATCH changed recovery_threshold to %d", stored.RecoveryThreshold)
	}
}

func TestMonitorDetailCarriesRecoveryThreshold(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true, RecoveryThreshold: 3})

	rec := getMonitorRaw(t, srv, m.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["recovery_threshold"] != float64(3) {
		t.Errorf("detail recovery_threshold = %#v, want 3", got["recovery_threshold"])
	}
}

// assertProblemField checks that a refused request names the field the client
// sent, so a form can put the message next to the right input.
func assertProblemField(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if body.Field != want {
		t.Errorf("problem field = %q, want %q", body.Field, want)
	}
}

// recoveryProbe stands in for the runner: the streak lives in the engine's
// memory, so the API asks the attached checker pipeline for it.
type recoveryProbe struct {
	streaks map[int64][2]int
}

func (p *recoveryProbe) Recovery(id int64) (passes, threshold int, ok bool) {
	s, ok := p.streaks[id]
	return s[0], s[1], ok
}

func (*recoveryProbe) CheckNow(context.Context, store.Monitor) (checker.Result, error) {
	panic("reading a monitor must never perform a check")
}

// A monitor whose confirmed incident is collecting passes reads as
// "recovering" with its streak (the list read shares describeMonitor). A
// monitor without a confirmed incident never does, whatever the engine says:
// recovering is a refinement of down, not a status of its own.
func TestRecoveringMonitorReportsItsStreak(t *testing.T) {
	srv, db := testServerWithDB(t)
	down := seedMonitor(t, db, store.Monitor{Name: "down", Type: "http", Target: "https://example.com/a", Enabled: true})
	healthy := seedMonitor(t, db, store.Monitor{Name: "healthy", Type: "http", Target: "https://example.com/b", Enabled: true})
	started := time.Now().Add(-10 * time.Minute)
	if _, err := db.OpenIncident(t.Context(), down.ID, started, "status", "HTTP 503"); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmIncident(t.Context(), down.ID, started.Add(time.Minute), "status", "HTTP 503"); err != nil {
		t.Fatal(err)
	}
	probe := &recoveryProbe{streaks: map[int64][2]int{down.ID: {1, 2}, healthy.ID: {1, 2}}}
	srv.WithProber(probe)

	for _, id := range []int64{down.ID, healthy.ID} {
		rec := getMonitorRaw(t, srv, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if id == healthy.ID {
			if got["status"] == "recovering" || got["recovery"] != nil {
				t.Errorf("a monitor without a confirmed incident reads as %v with recovery %v", got["status"], got["recovery"])
			}
			continue
		}
		if got["status"] != "recovering" {
			t.Errorf("detail status = %v, want recovering", got["status"])
		}
		want := map[string]any{"passes": float64(1), "threshold": float64(2)}
		if r, ok := got["recovery"].(map[string]any); !ok || r["passes"] != want["passes"] || r["threshold"] != want["threshold"] {
			t.Errorf("detail recovery = %#v, want %v", got["recovery"], want)
		}
	}

	delete(probe.streaks, down.ID)
	rec := getMonitorRaw(t, srv, down.ID)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "down" || got["recovery"] != nil {
		t.Errorf("once the streak breaks: status %v recovery %v, want down and no recovery", got["status"], got["recovery"])
	}
}
