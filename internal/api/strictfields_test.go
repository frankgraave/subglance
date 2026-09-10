package api

import (
	"net/http"
	"strings"
	"testing"
)

// A misspelled field must fail loudly. Accepting it silently hands the caller
// a monitor configured differently from what they asked for, and the mistake
// only surfaces days later as "why is this checking so slowly".
func TestCreateMonitorRejectsUnknownFields(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"t","type":"http","target":"https://example.com","interval_seconds":5}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unknown field was accepted): %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "interval_seconds") {
		t.Errorf("error does not name the offending field: %s", rec.Body.String())
	}
}

// The correct spelling must still work.
func TestCreateMonitorAcceptsKnownFields(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"t","type":"http","target":"https://example.com","interval_s":60}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
