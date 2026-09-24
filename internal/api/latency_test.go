package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

type latencyBody struct {
	MonitorID int64          `json:"monitor_id"`
	Window    string         `json:"window"`
	StepS     int            `json:"step_s"`
	From      time.Time      `json:"from"`
	To        time.Time      `json:"to"`
	Points    []latencyPoint `json:"points"`
}

func getLatency(t *testing.T, srv *Server, id int64, query string) (int, latencyBody, string) {
	t.Helper()
	rec := getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(id, 10)+"/latency"+query)
	var body latencyBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, body, rec.Body.String()
}

func TestMonitorLatencyDefaultsToTheLastDay(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)

	now := time.Now()
	for _, hb := range []store.Heartbeat{
		{MonitorID: id, TS: now.Add(-2 * time.Minute), OK: true, Assessment: "up", LatencyMS: 120},
		{MonitorID: id, TS: now.Add(-2 * time.Minute), OK: true, Assessment: "up", LatencyMS: 80},
		// Two days old: outside the default window.
		{MonitorID: id, TS: now.Add(-48 * time.Hour), OK: true, Assessment: "up", LatencyMS: 5000},
	} {
		if err := db.RecordHeartbeat(t.Context(), hb); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}
	}

	code, body, raw := getLatency(t, srv, id, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", code, raw)
	}
	if body.MonitorID != id || body.Window != "24h" || body.StepS != 900 {
		t.Fatalf("header = %+v, want monitor %d, window 24h, step 900s", body, id)
	}
	if got := body.To.Sub(body.From); got != 24*time.Hour {
		t.Errorf("range spans %s, want 24h", got)
	}
	if !body.To.After(now) {
		t.Errorf("range ends at %s, before now (%s): the newest step is cut off", body.To, now)
	}
	if len(body.Points) != 1 {
		t.Fatalf("points = %+v, want exactly the recent step", body.Points)
	}
	p := body.Points[0]
	if p.Checks != 2 || p.Samples != 2 || p.AvgMS == nil || *p.AvgMS != 100 || *p.MinMS != 80 || *p.MaxMS != 120 {
		t.Fatalf("point = %+v, want 2 checks averaging 100ms (80–120)", p)
	}
}

// A step whose checks all failed carries no latency, and says so with null
// rather than a 0 that would draw as the fastest answer ever given.
func TestMonitorLatencyReportsFailedStepsAsNull(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	if err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
		MonitorID: id, TS: time.Now().Add(-time.Minute), Assessment: "down",
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	rec := getUptime(t, srv, "/api/v1/monitors/"+strconv.FormatInt(id, 10)+"/latency?window=7d")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		StepS  int              `json:"step_s"`
		Points []map[string]any `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.StepS != 3600 {
		t.Errorf("7d step = %ds, want 3600", body.StepS)
	}
	if len(body.Points) != 1 {
		t.Fatalf("points = %v", body.Points)
	}
	p := body.Points[0]
	for _, key := range []string{"avg_ms", "min_ms", "max_ms"} {
		v, present := p[key]
		if !present || v != nil {
			t.Errorf("%s = %v (present %v), want an explicit null", key, v, present)
		}
	}
	if p["down"] != float64(1) || p["checks"] != float64(1) {
		t.Errorf("point = %v, want one down check", p)
	}
}

func TestMonitorLatencyRejectsUnknownWindows(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	for _, w := range []string{"1h", "7d12h", "365d", "abc"} {
		code, _, raw := getLatency(t, srv, id, "?window="+w)
		if code != http.StatusBadRequest {
			t.Errorf("window=%s: status = %d, want 400: %s", w, code, raw)
		}
	}
}

// Every window stays under the point budget, and every step divides a day so
// steps line up with calendar hours and days.
func TestLatencyStepKeepsEveryWindowUnderTheBudget(t *testing.T) {
	for spec, window := range latencyWindows {
		step := latencyStep(window)
		if n := window / step; n > maxLatencyPoints {
			t.Errorf("%s at %s is %d points, over %d", spec, step, n, maxLatencyPoints)
		}
		if (24*time.Hour)%step != 0 {
			t.Errorf("%s step %s does not divide a day", spec, step)
		}
	}
	if got := latencyStep(24 * time.Hour); got != 15*time.Minute {
		t.Errorf("24h step = %s, want 15m: the finest rung under budget", got)
	}
}
