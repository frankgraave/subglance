package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// fakeProber stands in for the real runner. The API must not need a network,
// a scheduler or a database write path to serve this endpoint — if it did,
// the short suite could not stay hermetic.
type fakeProber struct {
	mu       sync.Mutex
	calls    []store.Monitor
	result   checker.Result
	err      error
	recorded []int64
}

func (p *fakeProber) CheckNow(_ context.Context, m store.Monitor) (checker.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, m)
	if m.Enabled {
		p.recorded = append(p.recorded, m.ID)
	}
	if p.err != nil {
		return checker.Result{}, p.err
	}
	return p.result, nil
}

func (p *fakeProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// seedCheckMonitor inserts a monitor directly, bypassing the API, so a test
// can choose its enabled state without a second request.
func seedCheckMonitor(t *testing.T, db *store.DB, name string, enabled bool) store.Monitor {
	t.Helper()
	return seedMonitor(t, db, store.Monitor{
		Name:      name,
		Type:      "http",
		Target:    "https://example.com/",
		IntervalS: 60,
		TimeoutS:  10,
		Retries:   1,
		Enabled:   enabled,
	})
}

func checkNow(t *testing.T, srv *Server, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/monitors/"+strconv.FormatInt(id, 10)+"/check", nil)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

func TestCheckNowReturnsResultAndRecordsForEnabledMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)
	p := &fakeProber{result: checker.Result{
		OK:         true,
		Latency:    42 * time.Millisecond,
		StatusCode: 200,
		CheckedAt:  time.Now(),
	}}
	srv.WithProber(p)

	m := seedCheckMonitor(t, db, "live", true)

	rec := checkNow(t, srv, m.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got checkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || got.StatusCode != 200 || got.LatencyMS != 42 {
		t.Errorf("unexpected result: %+v", got)
	}
	if got.MonitorID != m.ID {
		t.Errorf("monitor_id = %d, want %d", got.MonitorID, m.ID)
	}
	// An enabled monitor's manual check counts. If it did not, the button
	// would report green while the dashboard stayed red, which is the exact
	// confusion this endpoint exists to remove.
	if !got.Recorded {
		t.Error("recorded = false for an enabled monitor; the result must count")
	}
	if len(p.recorded) != 1 {
		t.Errorf("prober recorded %d results, want 1", len(p.recorded))
	}
}

func TestCheckNowDoesNotRecordForPausedMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)
	p := &fakeProber{result: checker.Result{OK: true, CheckedAt: time.Now()}}
	srv.WithProber(p)

	m := seedCheckMonitor(t, db, "paused", false)

	rec := checkNow(t, srv, m.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got checkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A paused monitor is one the monitor promised not to watch. Probing it on
	// request is fine; silently writing that probe into its history is not,
	// because it would present an unmonitored gap as a monitored one.
	if got.Recorded {
		t.Error("recorded = true for a paused monitor; history must stay untouched")
	}
	if len(p.recorded) != 0 {
		t.Errorf("prober recorded %d results for a paused monitor, want 0", len(p.recorded))
	}
	if p.callCount() != 1 {
		t.Errorf("prober called %d times, want 1: a paused monitor should still be probed", p.callCount())
	}
}

func TestCheckNowRateLimitsPerMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)
	p := &fakeProber{result: checker.Result{OK: true, CheckedAt: time.Now()}}
	srv.WithProber(p)

	a := seedCheckMonitor(t, db, "a", true)
	b := seedCheckMonitor(t, db, "b", true)

	if rec := checkNow(t, srv, a.ID); rec.Code != http.StatusOK {
		t.Fatalf("first check status = %d, want 200", rec.Code)
	}

	rec := checkNow(t, srv, a.ID)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second check status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	// Without Retry-After a client can only guess, and guessing wrong means
	// hammering the endpoint it was just told to back off from.
	ra := rec.Header().Get("Retry-After")
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", ra)
	}

	// The limit is per monitor: one busy monitor must not block the others.
	if rec := checkNow(t, srv, b.ID); rec.Code != http.StatusOK {
		t.Errorf("check of a different monitor status = %d, want 200", rec.Code)
	}

	if p.callCount() != 2 {
		t.Errorf("prober called %d times, want 2: the throttled request must not probe", p.callCount())
	}
}

func TestCheckNowRejectsUnknownMonitorWithoutProbing(t *testing.T) {
	srv, _ := testServerWithDB(t)
	p := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(p)

	rec := checkNow(t, srv, 9999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if p.callCount() != 0 {
		t.Errorf("prober called %d times for an unknown monitor, want 0", p.callCount())
	}
}

func TestCheckNowUnavailableWithoutProber(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedCheckMonitor(t, db, "no-prober", true)

	rec := checkNow(t, srv, m.ID)
	// 503 rather than 404: the route exists, this deployment just has no
	// checker behind it. A 404 would send someone hunting for a typo.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestCheckNowRequiresWriteRole(t *testing.T) {
	srv, db := testServerWithDB(t)
	p := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(p)
	m := seedCheckMonitor(t, db, "guarded", true)

	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/monitors/"+strconv.FormatInt(m.ID, 10)+"/check", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	// A manual check causes outbound traffic and writes to history, so it is
	// a write even though it changes no configuration.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a viewer: %s", rec.Code, rec.Body.String())
	}
	if p.callCount() != 0 {
		t.Errorf("prober called %d times for a viewer, want 0", p.callCount())
	}
}

func TestCooldownIsPerKeyAndExpires(t *testing.T) {
	var c cooldown
	base := time.Now()

	if _, ok := c.reserve(1, base, time.Second); !ok {
		t.Fatal("first reserve was refused")
	}
	wait, ok := c.reserve(1, base.Add(200*time.Millisecond), time.Second)
	if ok {
		t.Fatal("second reserve inside the window was allowed")
	}
	if wait != 800*time.Millisecond {
		t.Errorf("wait = %v, want 800ms", wait)
	}
	if _, ok := c.reserve(1, base.Add(time.Second), time.Second); !ok {
		t.Error("reserve at exactly the window boundary was refused")
	}
	if _, ok := c.reserve(2, base, time.Second); !ok {
		t.Error("a different key was blocked by the first key's cooldown")
	}
}
