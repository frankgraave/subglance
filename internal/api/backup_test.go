package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/backup"
	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

type fakeBackups struct{ st backup.Status }

func (f fakeBackups) Status() backup.Status { return f.st }

func TestBackupStatusIsForAdministratorsOnly(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithBackups(fakeBackups{st: backup.Status{Target: "s3://bucket/nightly/"}})
	for role, want := range map[store.Role]int{
		store.RoleViewer: http.StatusForbidden,
		store.RoleEditor: http.StatusForbidden,
		store.RoleAdmin:  http.StatusOK,
	} {
		token := seedUser(t, srv, db, string(role)+"@backup.test", role)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", role, rec.Code, want)
		}
	}
}

func TestBackupStatusShapes(t *testing.T) {
	get := func(srv *Server) (int, map[string]any) {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil))
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Errorf("Cache-Control = %q", cc)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	unwired, _ := testServerWithDB(t)
	if code, _ := get(unwired); code != http.StatusServiceUnavailable {
		t.Errorf("never wired: %d, want 503", code)
	}

	off, _ := testServerWithDB(t)
	off.WithBackups(nil)
	code, body := get(off)
	if code != http.StatusOK || body["configured"] != false || body["target"] != nil || body["last_success_at"] != nil {
		t.Errorf("not configured: %d %v", code, body)
	}

	at := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	on, _ := testServerWithDB(t)
	on.WithBackups(fakeBackups{st: backup.Status{
		Target: "s3://bucket/nightly/", LastSuccess: at, LastObject: "subglance-20260925T030000Z.db.gz",
		LastSize: 4096, LastError: "upload: AccessDenied", LastErrorAt: at.Add(24 * time.Hour), Failures: 2,
	}})
	code, body = get(on)
	if code != http.StatusOK || body["configured"] != true || body["target"] != "s3://bucket/nightly/" ||
		body["last_success_at"] != "2026-09-25T03:00:00Z" || body["last_size_bytes"] != float64(4096) ||
		body["last_error"] != "upload: AccessDenied" || body["failures"] != float64(2) {
		t.Errorf("configured: %d %v", code, body)
	}
	if len(body) != 8 {
		t.Errorf("response has %d fields, want 8: %v", len(body), body)
	}
}

func TestMetricsCarryBackupSeriesOnlyWhenConfigured(t *testing.T) {
	scrape := func(srv *Server) string {
		srv.WithMetrics(fakeMetrics{m: monitor.Metrics{}})
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rec.Body.String()
	}

	off, _ := testServerWithDB(t)
	off.WithBackups(nil)
	if body := scrape(off); strings.Contains(body, "subglance_backup_") {
		t.Errorf("backup series on an instance without backups:\n%s", body)
	}

	on, _ := testServerWithDB(t)
	on.WithBackups(fakeBackups{st: backup.Status{
		LastSuccess: time.Unix(1790000000, 0), LastSize: 4096, Failures: 3,
	}})
	body := scrape(on)
	for _, line := range []string{
		"# TYPE subglance_backup_last_success_timestamp_seconds gauge\nsubglance_backup_last_success_timestamp_seconds 1790000000\n",
		"# TYPE subglance_backup_last_size_bytes gauge\nsubglance_backup_last_size_bytes 4096\n",
		"# TYPE subglance_backup_failures_total counter\nsubglance_backup_failures_total 3\n",
	} {
		if !strings.Contains(body, line) {
			t.Errorf("missing:\n%s\nin:\n%s", line, body)
		}
	}
}
