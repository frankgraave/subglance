package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// storeFailureText is what the failing trigger raises. It stands in for the
// driver's own wording, which names tables and constraints: it must reach the
// log, and must not reach the client.
const storeFailureText = "zz-store-failure-detail"

// failMonitorWrites makes the next write of kind ("INSERT" or "UPDATE") to the
// monitors table fail inside SQLite, the way a locked or full database would:
// after the API has accepted the request, at the moment the store writes it.
func failMonitorWrites(t *testing.T, db *store.DB, kind string) {
	t.Helper()
	_, err := db.Writer.ExecContext(t.Context(), fmt.Sprintf(
		`CREATE TRIGGER zz_fail_monitor_write BEFORE %s ON monitors BEGIN SELECT RAISE(ABORT, '%s'); END`,
		kind, storeFailureText))
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}
}

// wantServerFailure checks that a write the store could not complete is
// answered as the server's failure: 500, a fixed sentence, no field to blame,
// and the store's own text in the log rather than in the response.
func wantServerFailure(t *testing.T, code int, body, logged, want string) {
	t.Helper()
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — the request was valid, the write failed: %s", code, body)
	}
	var e errorResponse
	must(t, json.Unmarshal([]byte(body), &e))
	if e.Error != want {
		t.Errorf("error = %q, want %q", e.Error, want)
	}
	if e.Field != "" {
		t.Errorf("field = %q, want none: no input is wrong", e.Field)
	}
	if strings.Contains(body, storeFailureText) {
		t.Errorf("the store's error text reached the client: %s", body)
	}
	if !strings.Contains(logged, storeFailureText) {
		t.Errorf("the store's error text is missing from the log:\n%s", logged)
	}
}

func TestCreateMonitorAnswersAStoreFailureAsTheServersFault(t *testing.T) {
	srv, db := testServerWithDB(t)
	var logged bytes.Buffer
	srv.log = slog.New(slog.NewTextHandler(&logged, nil))
	failMonitorWrites(t, db, "INSERT")

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"API","type":"http","target":"https://example.com"}`)

	wantServerFailure(t, rec.Code, rec.Body.String(), logged.String(), "could not create monitor")
	if mons, _ := db.ListMonitors(t.Context()); len(mons) != 0 {
		t.Errorf("a failed create stored %d monitors", len(mons))
	}
}

func TestPatchMonitorAnswersAStoreFailureAsTheServersFault(t *testing.T) {
	srv, db := testServerWithDB(t)
	var logged bytes.Buffer
	srv.log = slog.New(slog.NewTextHandler(&logged, nil))
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com"})
	failMonitorWrites(t, db, "UPDATE")

	rec := patch(t, srv, fmt.Sprintf("/api/v1/monitors/%d", m.ID), `{"name":"Renamed"}`)

	wantServerFailure(t, rec.Code, rec.Body.String(), logged.String(), "could not update monitor")
	if after, _ := db.GetMonitor(t.Context(), m.ID); after.Name != "API" {
		t.Errorf("a failed update changed the name to %q", after.Name)
	}
}
