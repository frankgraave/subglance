package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

type uptimeBody struct {
	MonitorID int64          `json:"monitor_id"`
	Windows   []uptimeWindow `json:"windows"`
}

func getUptime(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func seedUptimeMonitor(t *testing.T, db *store.DB) int64 {
	t.Helper()
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "uptime", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m.ID
}

// Three ups and one down inside the last hour is 75% over every window that
// contains them.
func TestMonitorUptimeReturnsStandardWindows(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)

	now := time.Now()
	for i, ok := range []bool{true, true, true, false} {
		if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
			MonitorID: id,
			TS:        now.Add(-time.Duration(i+1) * time.Minute),
			OK:        ok,
			LatencyMS: 100,
		}); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}
	}

	rec := getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(id, 10)+"/uptime")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body uptimeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.MonitorID != id {
		t.Errorf("monitor_id = %d, want %d", body.MonitorID, id)
	}
	if len(body.Windows) != 3 {
		t.Fatalf("got %d windows, want 3: %s", len(body.Windows), rec.Body.String())
	}
	for _, w := range body.Windows {
		if w.Total != 4 || w.Up != 3 || w.Down != 1 {
			t.Errorf("window %s: total/up/down = %d/%d/%d, want 4/3/1", w.Window, w.Total, w.Up, w.Down)
		}
		if w.Uptime == nil || *w.Uptime != 75 {
			t.Errorf("window %s: uptime = %v, want 75", w.Window, w.Uptime)
		}
		if w.AvgLatencyMS != 100 {
			t.Errorf("window %s: avg latency = %d, want 100", w.Window, w.AvgLatencyMS)
		}
	}
	if body.Windows[0].WindowS != 86400 {
		t.Errorf("first window_s = %d, want 86400", body.Windows[0].WindowS)
	}
}

// A monitor with no samples must report null, not 0%. Zero would read as a
// total outage on a monitor that has simply never been checked.
func TestMonitorUptimeDistinguishesNoDataFromZero(t *testing.T) {
	srv, db := testServerWithDB(t)

	fresh := seedUptimeMonitor(t, db)
	rec := getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(fresh, 10)+"/uptime?window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body uptimeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(body.Windows))
	}
	if body.Windows[0].Uptime != nil {
		t.Errorf("uptime = %v for a monitor with no checks, want null", *body.Windows[0].Uptime)
	}
	if body.Windows[0].Total != 0 {
		t.Errorf("total = %d, want 0", body.Windows[0].Total)
	}

	// The all-failing monitor is the contrast: a real 0%, not an absence.
	broken := seedUptimeMonitor(t, db)
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: broken, TS: time.Now().Add(-time.Minute), OK: false,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	rec = getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(broken, 10)+"/uptime?window=24h")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Windows[0].Uptime == nil || *body.Windows[0].Uptime != 0 {
		t.Errorf("uptime = %v for an all-failing monitor, want 0", body.Windows[0].Uptime)
	}
}

// Samples older than the window must not count, or "uptime over the last hour"
// would silently mean "uptime ever".
func TestMonitorUptimeHonoursWindowBoundary(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)

	now := time.Now()
	// Inside a 1h window.
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: id, TS: now.Add(-10 * time.Minute), OK: true,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	// Outside it.
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: id, TS: now.Add(-3 * time.Hour), OK: false,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	rec := getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(id, 10)+"/uptime?window=1h")
	var body uptimeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Windows[0].Total != 1 || body.Windows[0].Up != 1 {
		t.Errorf("1h window counted %d samples (%d up), want 1 up — the 3h-old failure leaked in",
			body.Windows[0].Total, body.Windows[0].Up)
	}
	if body.Windows[0].Window != "1h" {
		t.Errorf("window echoed as %q, want 1h", body.Windows[0].Window)
	}
}

// An unknown monitor is a 404, not an empty-looking success.
func TestMonitorUptimeUnknownMonitor(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := getUptime(t, srv, "/api/v1/monitors/99999/uptime")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestMonitorUptimeRejectsBadWindow(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := strconv.FormatInt(seedUptimeMonitor(t, db), 10)

	for _, spec := range []string{"abc", "0s", "1s", "365d", "-24h", "", "7"} {
		path := "/api/v1/monitors/" + id + "/uptime?window=" + spec
		rec := getUptime(t, srv, path)
		want := http.StatusBadRequest
		if spec == "" {
			// An empty value falls back to the defaults rather than erroring.
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Errorf("window=%q: status = %d, want %d: %s", spec, rec.Code, want, rec.Body.String())
		}
	}
}

func TestParseUptimeWindowDaySuffix(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want time.Duration
	}{
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"90m", 90 * time.Minute},
	} {
		got, err := parseUptimeWindow(tc.spec)
		if err != nil {
			t.Errorf("parseUptimeWindow(%q): %v", tc.spec, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseUptimeWindow(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}

	// Mixed day syntax is rejected rather than half-parsed.
	if _, err := parseUptimeWindow("7d12h"); err == nil {
		t.Error("parseUptimeWindow(\"7d12h\") succeeded, want an error")
	}
}
