package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Exercise actual SQL reads, not a source assertion that a join exists.
func TestIncidentReminderReadsStayBounded(t *testing.T) {
	for _, size := range []int{1, 31} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			srv, db := testServerWithDB(t)
			var first int64
			for n := range size {
				m := seedMonitor(t, db, store.Monitor{Name: strconv.Itoa(n), Type: "http", Target: "https://example.com", Enabled: true, RepeatAfterS: 900, Tags: map[string]string{"env": "prod"}})
				if n == 0 {
					first = m.ID
				}
				at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
				if _, err := db.OpenIncident(t.Context(), m.ID, at, "status", "HTTP 503"); err != nil {
					t.Fatal(err)
				}
				if err := db.ConfirmIncident(t.Context(), m.ID, at.Add(time.Minute), "status", "HTTP 503"); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			if _, err := db.CreateMaintenance(t.Context(), store.MaintenanceWindow{Name: "bounded group", TagKey: "env", TagValue: "prod", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			var path string
			if err := db.Reader.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
				t.Fatal(err)
			}
			count := new(atomic.Int64)
			total := new(atomic.Int64)
			original := db.Reader
			db.Reader = sql.OpenDB(incidentReadConnector{driver: original.Driver(), path: path, count: count, total: total})
			t.Cleanup(func() { _ = original.Close() })
			rows := readReminderRows(t, srv, "/api/v1/incidents")
			if len(rows) != size {
				t.Fatalf("rows = %d, want %d", len(rows), size)
			}
			if got := count.Load(); got != 1 {
				t.Fatalf("incident/monitor reads = %d, want one joined read for %d rows", got, size)
			}
			if got := total.Load(); got != 3 {
				t.Fatalf("total reads=%d, want joined incidents, schedules and batch tags", got)
			}
			assertReminderJSON(t, rows[0], "reminder_status", "maintenance")
			total.Store(0)
			count.Store(0)
			rows = readReminderRows(t, srv, "/api/v1/monitors/"+strconv.FormatInt(first, 10)+"/incidents?limit=1")
			if len(rows) != 1 {
				t.Fatalf("history rows = %d", len(rows))
			}
			if got := total.Load(); got != 5 {
				t.Fatalf("history total reads=%d, want monitor/tag existence check plus three bounded reads", got)
			}
			if got := count.Load(); got != 2 {
				t.Fatalf("history reads = %d, want existence check plus joined history", got)
			}
		})
	}
}

type incidentReadConnector struct {
	driver driver.Driver
	path   string
	count  *atomic.Int64
	total  *atomic.Int64
}

func (c incidentReadConnector) Driver() driver.Driver { return c.driver }
func (c incidentReadConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.path)
	if err != nil {
		return nil, err
	}
	return incidentReadConn{Conn: conn, count: c.count, total: c.total}, nil
}

type incidentReadConn struct {
	driver.Conn
	count *atomic.Int64
	total *atomic.Int64
}

func (c incidentReadConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.ToLower(query)
	if strings.Contains(q, "from incidents") || strings.Contains(q, "from monitors") || strings.Contains(q, "from monitor_tags") || strings.Contains(q, "from maintenance_windows") {
		c.total.Add(1)
	}
	if strings.Contains(q, "from incidents") || strings.Contains(q, "from monitors") {
		c.count.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestIncidentReminderRoutesStillEnforceAccessAndErrors(t *testing.T) {
	srv, db, m, _, _ := reminderFixture(t)
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d", rec.Code)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+viewer)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"next_reminder_at"`) {
			t.Fatalf("viewer: %d %s", rec.Code, rec.Body.String())
		}
	}
	if _, err := db.Writer.ExecContext(t.Context(), "DROP TABLE incidents"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/incidents", "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/incidents"} {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("failed history must not become an empty list: %d %s", rec.Code, rec.Body.String())
		}
	}
}
