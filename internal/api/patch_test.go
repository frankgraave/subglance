package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// PATCH is the only way to edit a monitor without losing its history, so the
// thing worth proving is not that a field changes but that the fields nobody
// mentioned do not.

func patch(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

// seedMonitor creates a monitor directly in the store, bypassing the API, so a
// PATCH test does not depend on create staying correct.
func seedMonitor(t *testing.T, db *store.DB, m store.Monitor) store.Monitor {
	t.Helper()
	created, err := db.CreateMonitor(t.Context(), m)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return created
}

func TestPatchMonitorLeavesUnmentionedFieldsAlone(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 120, TimeoutS: 30, Retries: 5,
		Method: "POST", ExpectedStatus: "200-204", Keyword: "welkom",
		KeywordMode: "must_contain", FollowRedirects: false,
		Headers: map[string]string{"X-Token": "abc"}, Body: "ping",
		SSLWarnDays: 21, Enabled: true,
	})

	rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10),
		`{"name": "site (prod)"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != "site (prod)" {
		t.Errorf("name = %q, want %q", got.Name, "site (prod)")
	}

	// The zero-value trap: with plain (non-pointer) request fields, this
	// rename would have reset the interval to the default, cleared the
	// keyword, dropped the headers and paused the monitor.
	if got.IntervalS != 120 || got.TimeoutS != 30 || got.Retries != 5 {
		t.Errorf("schedule changed: interval=%d timeout=%d retries=%d",
			got.IntervalS, got.TimeoutS, got.Retries)
	}
	if got.Method != "POST" || got.ExpectedStatus != "200-204" {
		t.Errorf("http settings changed: method=%q expected=%q", got.Method, got.ExpectedStatus)
	}
	if got.Keyword != "welkom" || got.KeywordMode != "must_contain" {
		t.Errorf("keyword changed: %q/%q", got.Keyword, got.KeywordMode)
	}
	if got.FollowRedirects {
		t.Error("follow_redirects flipped back to true")
	}
	if got.Headers["X-Token"] != "abc" {
		t.Errorf("headers changed: %v", got.Headers)
	}
	if got.Body != "ping" {
		t.Errorf("body = %q, want %q", got.Body, "ping")
	}
	if got.SSLWarnDays != 21 {
		t.Errorf("ssl_warn_days = %d, want 21", got.SSLWarnDays)
	}
	if !got.Enabled {
		t.Error("monitor was paused by a rename")
	}
	if got.Target != "https://example.com" {
		t.Errorf("target = %q, want unchanged", got.Target)
	}
}

// An explicit false must be honoured, which is the other half of the pointer
// distinction: "absent" and "false" have to mean different things.
func TestPatchMonitorAppliesExplicitFalse(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		FollowRedirects: true, Enabled: true,
	})

	rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10),
		`{"enabled": false, "follow_redirects": false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Enabled {
		t.Error("enabled=false was ignored")
	}
	if got.FollowRedirects {
		t.Error("follow_redirects=false was ignored")
	}
}

// Editing must not cost the monitor its history. Delete-and-recreate, the only
// option before PATCH, does exactly that.
func TestPatchMonitorKeepsHeartbeats(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{
		MonitorID: m.ID, TS: time.Now().UTC(), OK: true, LatencyMS: 42,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10),
		`{"target": "https://example.com/health"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	hbs, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("heartbeats = %d, want 1 — editing destroyed history", len(hbs))
	}
}

// The merged monitor is what gets checked, so validation has to run on the
// result rather than on the request. Changing only the type can invalidate a
// target that the request never mentions.
func TestPatchMonitorValidatesMergedResult(t *testing.T) {
	srv, db := testServerWithDB(t)

	tests := []struct {
		name string
		seed store.Monitor
		body string
		want string
	}{
		{
			name: "type change invalidates untouched target",
			seed: store.Monitor{Name: "a", Type: "http", Target: "https://example.com", Enabled: true},
			body: `{"type": "ping"}`,
			want: "ping monitor takes a hostname",
		},
		{
			name: "tcp without port",
			seed: store.Monitor{Name: "b", Type: "tcp", Target: "db.example.com:5432", Enabled: true},
			body: `{"target": "db.example.com"}`,
			want: "needs a port",
		},
		{
			name: "interval below the floor",
			seed: store.Monitor{Name: "c", Type: "http", Target: "https://example.com", Enabled: true},
			body: `{"interval_s": 5}`,
			want: "interval_s must be between",
		},
		{
			name: "empty name",
			seed: store.Monitor{Name: "d", Type: "http", Target: "https://example.com", Enabled: true},
			body: `{"name": "   "}`,
			want: "name cannot be empty",
		},
		{
			name: "unknown field",
			seed: store.Monitor{Name: "e", Type: "http", Target: "https://example.com", Enabled: true},
			body: `{"interval_seconds": 60}`,
			want: "invalid JSON",
		},
		{
			name: "keyword mode outside the schema check",
			seed: store.Monitor{Name: "f", Type: "http", Target: "https://example.com", Enabled: true},
			body: `{"keyword_mode": "required"}`,
			want: "unknown keyword_mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := seedMonitor(t, db, tc.seed)
			before, err := db.GetMonitor(t.Context(), m.ID)
			if err != nil {
				t.Fatalf("GetMonitor: %v", err)
			}

			rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10), tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}

			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !strings.Contains(body.Error, tc.want) {
				t.Errorf("error = %q, want it to mention %q", body.Error, tc.want)
			}

			// A rejected patch must change nothing at all.
			after, err := db.GetMonitor(t.Context(), m.ID)
			if err != nil {
				t.Fatalf("GetMonitor: %v", err)
			}
			if after.Type != before.Type || after.Target != before.Target ||
				after.IntervalS != before.IntervalS || after.Name != before.Name ||
				after.KeywordMode != before.KeywordMode {
				t.Errorf("rejected patch still wrote: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestPatchMonitorUnknownID(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := patch(t, srv, "/api/v1/monitors/9999", `{"name": "x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// PATCH mutates, so it belongs behind the write role, not the read role.
func TestPatchMonitorRequiresWriteRole(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})

	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)

	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/monitors/"+strconv.FormatInt(m.ID, 10),
		strings.NewReader(`{"name": "hacked"}`))
	req.Header.Set("Authorization", "Bearer "+viewer)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != "site" {
		t.Errorf("a viewer renamed a monitor: %q", got.Name)
	}
}
