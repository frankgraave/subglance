package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func TestMonitorListIncludesAllChannelAttachments(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	channel, err := db.CreateChannel(ctx, store.Channel{Name: "Operations", Type: store.ChannelWebhook, Enabled: false, Config: map[string]string{"url": "https://secret.invalid/token"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateChannel(ctx, store.Channel{Name: "Backup", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 45)
	for i := range ids {
		ids[i] = seedMonitor(t, db, store.Monitor{Name: strconv.Itoa(i), Type: "http", Target: "https://example.com"}).ID
	}
	if err := db.SetMonitorChannels(ctx, ids[44], []int64{second.ID, channel.ID}); err != nil {
		t.Fatal(err)
	}
	queries := countAttachmentQueries(t, db)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	if got := queries.Load(); got != 1 {
		t.Errorf("attachment queries = %d, want one bulk query independent of monitor count", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Monitors []struct {
			ID       int64           `json:"id"`
			Channels json.RawMessage `json:"channels"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != len(ids) {
		t.Fatalf("got %d monitors, want %d", len(got.Monitors), len(ids))
	}
	for _, m := range got.Monitors {
		want := `[]`
		if m.ID == ids[44] {
			want = `[{"id":` + strconv.FormatInt(channel.ID, 10) + `,"name":"Operations"},{"id":` + strconv.FormatInt(second.ID, 10) + `,"name":"Backup"}]`
		}
		if string(m.Channels) != want {
			t.Errorf("monitor %d channels = %s, want %s", m.ID, m.Channels, want)
		}
	}
	if strings.Contains(rec.Body.String(), "secret.invalid") || strings.Contains(rec.Body.String(), "config") {
		t.Fatal("list leaked channel configuration")
	}
}

// Count actual SQLite reads, scoped to the attachment query added here.
func countAttachmentQueries(t *testing.T, db *store.DB) *atomic.Int64 {
	t.Helper()
	var path string
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int64)
	original := db.Reader
	db.Reader = sql.OpenDB(attachmentConnector{driver: original.Driver(), path: path, count: count})
	t.Cleanup(func() { _ = original.Close() })
	return count
}

type attachmentConnector struct {
	driver driver.Driver
	path   string
	count  *atomic.Int64
}

func (c attachmentConnector) Driver() driver.Driver { return c.driver }
func (c attachmentConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.path)
	if err != nil {
		return nil, err
	}
	return attachmentConn{Conn: conn, count: c.count}, nil
}

type attachmentConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c attachmentConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "monitor_channels") {
		c.count.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestMonitorListChannelFailureIsUnknownNotEmpty(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com"})
	if _, err := db.Writer.ExecContext(t.Context(), "DROP TABLE monitor_channels"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("channel failure must not blank monitors: %d", rec.Code)
	}
	var got struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 1 {
		t.Fatalf("monitors = %d", len(got.Monitors))
	}
	if _, ok := got.Monitors[0]["channels"]; ok {
		t.Fatal("failed channel query claimed a known attachment set")
	}
}
