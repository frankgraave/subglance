package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer() *Server {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
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
