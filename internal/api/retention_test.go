package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func decodeRetention(t *testing.T, rec *httptest.ResponseRecorder) retentionResponse {
	t.Helper()
	var got retentionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return got
}

func TestRetentionSettingsRoundTrip(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Error("retention diagnostics are cacheable")
	}
	got := decodeRetention(t, rec)
	if got.Raw.Seconds != 30*86400 || got.Raw.Source != "default" || got.Raw.PinnedBy != nil {
		t.Errorf("raw = %+v, want the 30-day default", got.Raw)
	}
	if got.Rollup.Seconds != 0 || got.Rollup.Source != "default" {
		t.Errorf("rollup = %+v, want forever by default", got.Rollup)
	}
	if got.MinimumRawSeconds != 86400 {
		t.Errorf("minimum_raw_seconds = %d", got.MinimumRawSeconds)
	}
	if len(got.Tables) != 4 {
		t.Errorf("tables = %+v, want four", got.Tables)
	}

	rec = doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"raw_seconds":604800,"rollup_seconds":31536000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got = decodeRetention(t, rec)
	if got.Raw.Seconds != 604800 || got.Raw.Source != "database" || got.Rollup.Seconds != 31536000 {
		t.Errorf("after PUT: raw %+v rollup %+v", got.Raw, got.Rollup)
	}
}

// Once the windows are committed, a failure to measure the tables must not
// turn the answer into a 500: the client would report a save that happened
// as one that failed.
func TestRetentionSaveSurvivesAFailedMeasurement(t *testing.T) {
	srv, db := testServerWithDB(t)
	// With the table gone from under its name, measuring fails while the
	// settings themselves can still be written.
	if _, err := db.Writer.Exec(`ALTER TABLE heartbeat_responses RENAME TO heartbeat_responses_gone`); err != nil {
		t.Fatalf("rename table: %v", err)
	}
	if rec := doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET = %d, want the measurement to fail: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"raw_seconds":604800}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeRetention(t, rec)
	if got.Raw.Seconds != 604800 || got.Raw.Source != "database" {
		t.Errorf("raw = %+v, want the saved 7 days", got.Raw)
	}
	if got.Tables == nil || len(got.Tables) != 0 {
		t.Errorf("tables = %#v, want an empty list", got.Tables)
	}
}

func TestRetentionSettingsRefuseInvalidWindows(t *testing.T) {
	srv, _ := testServerWithDB(t)
	tests := []struct {
		name, body, field string
	}{
		{"raw under a day", `{"raw_seconds":3600}`, "raw_seconds"},
		{"negative", `{"rollup_seconds":-1}`, "rollup_seconds"},
		{"rollup inside raw", `{"raw_seconds":604800,"rollup_seconds":86400}`, "rollup_seconds"},
		{"overflow", `{"raw_seconds":9223372036854775807}`, "raw_seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			var e errorResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &e)
			if e.Field != tt.field {
				t.Errorf("field = %q, want %q (%s)", e.Field, tt.field, e.Error)
			}
		})
	}
	if rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty PUT = %d, want 400", rec.Code)
	}
}

func TestRetentionSettingsPinnedWindowIsReadOnly(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithRetentionPins(store.RetentionPins{
		Raw: &store.RetentionPin{Value: 14 * 24 * time.Hour, By: "--raw-retention"},
	})

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"raw_seconds":86400}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT over a pin = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "--raw-retention") {
		t.Errorf("the refusal does not name the flag: %s", rec.Body.String())
	}

	got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
	if got.Raw.Source != "pinned" || got.Raw.PinnedBy == nil || *got.Raw.PinnedBy != "--raw-retention" ||
		got.Raw.Seconds != 14*86400 {
		t.Errorf("raw = %+v, want pinned by --raw-retention", got.Raw)
	}

	// The unpinned window is still the operator's to change.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"rollup_seconds":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT of the unpinned window = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRetentionSettingsOnlyAdminsWrite(t *testing.T) {
	srv, db := testServerWithDB(t)
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, srv, db, string(role)+"@retention.test", role)

		get := httptest.NewRequest(http.MethodGet, "/api/v1/settings/retention", nil)
		get.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, get)
		if rec.Code != http.StatusOK {
			t.Errorf("%s cannot read retention: %d", role, rec.Code)
		}

		put := httptest.NewRequest(http.MethodPut, "/api/v1/settings/retention", strings.NewReader(`{"raw_seconds":86400}`))
		put.Header.Set("Authorization", "Bearer "+token)
		put.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, put)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s PUT retention = %d, want 403", role, rec.Code)
		}
	}
}

func TestRetentionPreviewCountsTheProposedChange(t *testing.T) {
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "old", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{20 * 24 * time.Hour, 10 * 24 * time.Hour, time.Hour} {
		if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{MonitorID: m.ID, TS: time.Now().Add(-age), OK: true, Assessment: "up"}); err != nil {
			t.Fatal(err)
		}
	}

	// Under the 30-day default nothing is old enough.
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention/preview", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"heartbeats":0`) {
		t.Fatalf("default preview = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention/preview?raw_seconds=604800", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"heartbeats":2`) {
		t.Fatalf("7-day preview = %d %s, want 2 heartbeats", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention/preview?raw_seconds=60", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preview of an invalid window = %d, want 400", rec.Code)
	}
}

func putRetention(t *testing.T, srv *Server, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/retention", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, r)
	return rec
}

// The page previews only a change that shortens a window. Two administrators
// who both read 30 days, one saving 90 and the other 60, would otherwise
// shorten retention from 90 to 60 without anyone having seen what that
// removes. The version makes the second save fail instead.
func TestRetentionSaveIsConditionalOnTheVersionRead(t *testing.T) {
	srv, _ := testServerWithDB(t)

	get := doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "")
	tag := get.Header().Get("ETag")
	if tag != `W/"0"` {
		t.Fatalf("ETag before any save = %q, want W/\"0\"", tag)
	}

	first := putRetention(t, srv, `{"raw_seconds":7776000}`, tag)
	if first.Code != http.StatusOK {
		t.Fatalf("first conditional PUT = %d: %s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("ETag"); got != `W/"1"` {
		t.Errorf("ETag after a save = %q, want W/\"1\"", got)
	}

	stale := putRetention(t, srv, `{"raw_seconds":5184000}`, tag)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale conditional PUT = %d, want 412: %s", stale.Code, stale.Body.String())
	}
	if stale.Header().Get("ETag") != "" {
		t.Error("a 412 hands back a validator the client could blindly retry with")
	}
	got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
	if got.Raw.Seconds != 7776000 {
		t.Fatalf("raw = %d after a refused save, want the first administrator's 90 days", got.Raw.Seconds)
	}

	// Re-read, the second administrator's save goes through.
	fresh := doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "").Header().Get("ETag")
	if rec := putRetention(t, srv, `{"raw_seconds":5184000}`, fresh); rec.Code != http.StatusOK {
		t.Fatalf("PUT with the fresh ETag = %d: %s", rec.Code, rec.Body.String())
	}
}

// Header.Get cannot tell an absent If-Match from an empty one. Treating the
// empty one as absent would turn a conditional save into an unconditional one.
func TestRetentionSaveEmptyIfMatchIsMalformed(t *testing.T) {
	srv, _ := testServerWithDB(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/retention", strings.NewReader(`{"raw_seconds":2592000}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header["If-Match"] = []string{""}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty If-Match = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestRetentionSaveIfMatchForms(t *testing.T) {
	tests := []struct {
		name, ifMatch string
		want          int
	}{
		// No header: last-write-wins, as before the header existed.
		{"absent", "", http.StatusOK},
		{"star", "*", http.StatusOK},
		{"strong spelling of a current tag", `"1"`, http.StatusOK},
		{"one of several", `W/"9", W/"1"`, http.StatusOK},
		{"re-spelled current tag", `W/"01"`, http.StatusPreconditionFailed},
		{"not a tag this API issues", `W/"abc"`, http.StatusPreconditionFailed},
		{"malformed", `W/abc`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each case starts from one earlier save, so version 1 is current.
			srv, _ := testServerWithDB(t)
			if rec := putRetention(t, srv, `{"raw_seconds":1209600}`, ""); rec.Code != http.StatusOK {
				t.Fatalf("setup PUT = %d: %s", rec.Code, rec.Body.String())
			}
			rec := putRetention(t, srv, `{"raw_seconds":2592000}`, tt.ifMatch)
			if rec.Code != tt.want {
				t.Fatalf("If-Match %q = %d, want %d: %s", tt.ifMatch, rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
