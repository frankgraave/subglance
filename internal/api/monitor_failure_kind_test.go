package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A list names a failure by its kind and leaves the full message to the
// monitor's page (SUB-194), so the monitor object carries the kind of the
// error it reports. The two travel together: an error taken from the open
// incident must not wear the kind of a heartbeat it did not come from.

func monitorFailure(t *testing.T, srv *Server, id int64) (errText, kind string, present bool) {
	t.Helper()
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range body.Monitors {
		if int64(m["id"].(float64)) != id {
			continue
		}
		e, _ := m["error"].(string)
		k, ok := m["failure_kind"].(string)
		return e, k, ok
	}
	t.Fatalf("monitor %d not in the listing", id)
	return "", "", false
}

func TestMonitorCarriesTheFailingHeartbeatsKind(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	at := time.Unix(1_700_000_000, 0).UTC()
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: at, OK: false, Error: "context deadline exceeded", FailureKind: "timeout",
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	errText, kind, _ := monitorFailure(t, srv, m.ID)
	if errText != "context deadline exceeded" || kind != "timeout" {
		t.Errorf("error, failure_kind = %q, %q; want the failing heartbeat's message and kind", errText, kind)
	}
}

func TestMonitorOmitsFailureKindOnAPassingCheck(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	// A kind on a passing row is not a failure to name; the field follows
	// the check's outcome, not whatever the row happens to hold.
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: time.Unix(1_700_000_000, 0).UTC(), OK: true, FailureKind: "status",
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	if _, kind, present := monitorFailure(t, srv, m.ID); present {
		t.Errorf("failure_kind = %q on a passing check, want it omitted", kind)
	}
}

func TestMonitorTakesTheIncidentsCauseWithItsError(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "job", Type: "http", Target: "https://example.com", Enabled: true})
	at := time.Unix(1_700_000_000, 0).UTC()
	if _, err := db.OpenIncident(t.Context(), m.ID, at, "connection", "dial tcp: connect: connection refused"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	// The latest check failed without a message of its own, so the error
	// comes from the incident, and so must the kind.
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: m.ID, TS: at.Add(time.Minute), OK: false, FailureKind: "timeout",
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	errText, kind, _ := monitorFailure(t, srv, m.ID)
	if errText != "dial tcp: connect: connection refused" || kind != "connection" {
		t.Errorf("error, failure_kind = %q, %q; want the incident's message and its cause", errText, kind)
	}
}

func TestMonitorWithoutAFailureHasNoKind(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "new", Type: "http", Target: "https://example.com", Enabled: true})

	if _, kind, present := monitorFailure(t, srv, m.ID); present {
		t.Errorf("failure_kind = %q on a never-checked monitor, want it omitted", kind)
	}
}
