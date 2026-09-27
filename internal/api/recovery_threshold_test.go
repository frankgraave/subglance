package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
