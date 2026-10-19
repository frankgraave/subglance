package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

// countingRescheduler counts how often the API told the scheduler that the
// monitor set changed.
type countingRescheduler struct{ calls atomic.Int32 }

func (c *countingRescheduler) ReloadSoon() { c.calls.Add(1) }

// Every write that changes what the scheduler checks tells it once, after the
// write; a refused or read-only request tells it nothing. Before, nothing told
// it, and each of these waited up to 30 seconds for the reload tick: a new
// monitor's first check, a deleted or paused monitor's last.
func TestMonitorWritesTellTheScheduler(t *testing.T) {
	srv, db := testServerWithDB(t)
	rs := &countingRescheduler{}
	srv.WithRescheduler(rs)

	seed := func(t *testing.T) string {
		t.Helper()
		m, err := db.CreateMonitor(t.Context(), store.Monitor{
			Name: "seeded", Type: "tcp", Target: "db.example.com:5432",
			IntervalS: 60, TimeoutS: 10, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(m.ID, 10)
	}
	send := func(method, path, body, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, req)
		return rec
	}
	const importDoc = "version: 1\nmonitors:\n  - key: a\n    name: A\n    type: tcp\n    target: a.example.com:1\n" +
		"  - key: b\n    name: B\n    type: tcp\n    target: b.example.com:1\n"

	cases := []struct {
		name        string
		method      string
		path        func(id string) string
		body        string
		contentType string
		status      int
		want        int32
	}{
		{"create", http.MethodPost, func(string) string { return "/api/v1/monitors" },
			`{"name":"new","type":"tcp","target":"new.example.com:443","interval_s":60}`, "application/json", http.StatusCreated, 1},
		{"edit", http.MethodPatch, func(id string) string { return "/api/v1/monitors/" + id },
			`{"interval_s":120}`, "application/json", http.StatusOK, 1},
		{"rename", http.MethodPatch, func(id string) string { return "/api/v1/monitors/" + id },
			`{"name":"renamed"}`, "application/json", http.StatusOK, 1},
		{"pause", http.MethodPost, func(id string) string { return "/api/v1/monitors/" + id + "/pause" },
			"", "application/json", http.StatusOK, 1},
		{"resume", http.MethodPost, func(id string) string { return "/api/v1/monitors/" + id + "/resume" },
			"", "application/json", http.StatusOK, 1},
		{"delete", http.MethodDelete, func(id string) string { return "/api/v1/monitors/" + id },
			"", "application/json", http.StatusNoContent, 1},
		// One read for the whole file, however many monitors it writes.
		{"import", http.MethodPost, func(string) string { return "/api/v1/config/import" },
			importDoc, "application/yaml", http.StatusOK, 1},

		// Nothing written, nothing to tell.
		{"refused create", http.MethodPost, func(string) string { return "/api/v1/monitors" },
			`{"name":"bad","type":"tcp","target":"x:1","interval_s":5}`, "application/json", http.StatusBadRequest, 0},
		{"refused edit", http.MethodPatch, func(id string) string { return "/api/v1/monitors/" + id },
			`{"interval_s":5}`, "application/json", http.StatusBadRequest, 0},
		{"dry-run import", http.MethodPost, func(string) string { return "/api/v1/config/import?dry_run=true" },
			importDoc, "application/yaml", http.StatusOK, 0},
		{"read", http.MethodGet, func(id string) string { return "/api/v1/monitors/" + id },
			"", "application/json", http.StatusOK, 0},
		{"manual check", http.MethodPost, func(id string) string { return "/api/v1/monitors/" + id + "/check" },
			"", "application/json", http.StatusServiceUnavailable, 0},

		// Last: a reset also deletes the API token the others sign in with.
		{"reset", http.MethodPost, func(string) string { return "/api/v1/instance/reset" },
			`{"confirm":"` + resetPhrase + `"}`, "application/json", http.StatusOK, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := seed(t)
			before := rs.calls.Load()
			rec := send(tc.method, tc.path(id), tc.body, tc.contentType)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := rs.calls.Load() - before; got != tc.want {
				t.Errorf("scheduler told %d times, want %d", got, tc.want)
			}
		})
	}
}

// Without a rescheduler, as in tests and any assembly with no scheduler
// behind the API, a write still succeeds.
func TestMonitorWritesWithoutARescheduler(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"new","type":"tcp","target":"new.example.com:443","interval_s":60}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// End to end through the runner cmd/subglance wires in: a monitor created
// through the API is checked within seconds, not at the reload tick. The
// target is a local listener, so nothing leaves the host.
func TestCreatedMonitorIsCheckedWithinSeconds(t *testing.T) {
	// The first request for /fresh closes freshHit, so the wait below
	// returns the moment the check arrives instead of polling a counter.
	freshHit := make(chan struct{})
	var once sync.Once
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fresh" {
			once.Do(func() { close(freshHit) })
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	srv, db := testServerWithDB(t)
	runner := monitor.New(monitor.Options{
		DB: db, Log: testLogger(), AllowPrivateTargets: true,
		// The production tick. A first check inside it came from the API.
		ReloadInterval: 30 * time.Second,
	})
	srv.WithProber(runner).WithRescheduler(runner)
	// A monitor that is there at the start: once it is scheduled, Run has
	// done its first read, and the create below can only reach the
	// scheduler through the API's request or the tick.
	if _, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "seeded", Type: "http", Target: target.URL + "/seed",
		IntervalS: 3600, TimeoutS: 5, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	// The scheduler has no readiness signal, and adding one to the runner
	// for this test is not worth it: Size is polled, and the 5 s is only the
	// bound on a first read that takes milliseconds.
	waitUntil(t, 5*time.Second, func() bool { return runner.Size() == 1 })

	created := time.Now()
	rec := post(t, srv, "/api/v1/monitors", `{"name":"fresh","type":"http","target":"`+target.URL+`/fresh","interval_s":3600}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	// firstCheckDelay (2 s) plus up to 10% jitter of it, plus the settle
	// time: well under 5 s. The tick would take up to 30.
	select {
	case <-freshHit:
	case <-time.After(10 * time.Second):
		t.Fatal("the created monitor was not checked within 10 s")
	}
	if took := time.Since(created); took > 5*time.Second {
		t.Errorf("first check %v after the create, want within 5 s", took)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
