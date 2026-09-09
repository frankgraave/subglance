package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testServer returns a Server with no database, for routes that do not need one.
func testServer() *Server {
	return New(testLogger(), nil)
}

// testServerWithDB returns a Server backed by a real temporary database.
func testServerWithDB(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(t.TempDir(), "api.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(testLogger(), db), db
}

func TestHealth(t *testing.T) {
	srv := testServer()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Version == "" {
		t.Error("version is empty")
	}
	if got.Uptime == "" {
		t.Error("uptime is empty")
	}
}

func TestHealthUnderAPIPrefix(t *testing.T) {
	srv := testServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	srv := testServer()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWrongMethodIsRejected(t *testing.T) {
	srv := testServer()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("POST /health returned 200; the route is registered GET-only")
	}
}

// A panicking handler must not take the process down: a monitoring tool that
// dies on a bad request is worse than useless.
func TestRecoveryMiddleware(t *testing.T) {
	srv := testServer()

	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	h := srv.withRecovery(mux)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req) // must not panic out of here

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestReadyWithHealthyDatabase(t *testing.T) {
	srv, _ := testServerWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var got readyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ready" || got.Database != "up" {
		t.Errorf("got %+v, want status=ready database=up", got)
	}
}

// A closed database must make readiness fail. If it did not, an orchestrator
// would keep routing traffic to an instance that cannot answer a single query.
func TestReadyWithBrokenDatabase(t *testing.T) {
	srv, db := testServerWithDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var got readyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Database != "down" {
		t.Errorf("database = %q, want down", got.Database)
	}
}

// /health must keep answering even when the database is gone: it decides
// whether to restart the process, and a restart would not fix a sick database.
func TestHealthStaysUpWhenDatabaseIsDown(t *testing.T) {
	srv, db := testServerWithDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: /health must not depend on the database", rec.Code)
	}
}
