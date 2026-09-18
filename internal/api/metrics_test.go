package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/monitor"
)

// fakeMetrics is the only double in these tests, and it doubles the *source*
// rather than the failure: whether a failed write is counted is proved in
// internal/monitor against a real database (see liveness_test.go). What is
// proved here is that whatever the runner counted reaches the wire in a form
// Prometheus can scrape, and that the endpoint is not readable anonymously.
type fakeMetrics struct{ m monitor.Metrics }

func (f fakeMetrics) Metrics() monitor.Metrics { return f.m }

func TestMetricsExposesTheOperationalCounters(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithMetrics(fakeMetrics{m: monitor.Metrics{
		ChecksRecorded:         1482,
		HeartbeatWriteFailures: 37,
		RollupFailures:         2,
		SkippedChecks:          9,
		QueueDepth:             16,
		Workers:                50,
		Scheduled:              200,
	}})

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want Prometheus text format", ct)
	}

	body := rec.Body.String()

	// Every counter the ticket names, with its value. A HELP and TYPE line
	// each, because a series without them is not a well-formed exposition.
	want := map[string]string{
		"subglance_heartbeat_write_failures_total": "37",
		"subglance_checks_recorded_total":          "1482",
		"subglance_rollup_failures_total":          "2",
		"subglance_checks_skipped_total":           "9",
		"subglance_check_queue_depth":              "16",
		"subglance_check_workers":                  "50",
		"subglance_monitors_scheduled":             "200",
	}
	for name, value := range want {
		if !strings.Contains(body, "# HELP "+name+" ") {
			t.Errorf("%s has no HELP line:\n%s", name, body)
		}
		if !strings.Contains(body, "# TYPE "+name+" ") {
			t.Errorf("%s has no TYPE line:\n%s", name, body)
		}
		if !strings.Contains(body, "\n"+name+" "+value+"\n") &&
			!strings.HasPrefix(body, name+" "+value+"\n") {
			t.Errorf("%s is not reported as %s:\n%s", name, value, body)
		}
	}

	// Counters are counters and gauges are gauges. A depth reported as a
	// counter makes rate() produce nonsense in every dashboard built on it.
	if !strings.Contains(body, "# TYPE subglance_heartbeat_write_failures_total counter") {
		t.Error("the write-failure metric is not typed as a counter")
	}
	if !strings.Contains(body, "# TYPE subglance_check_queue_depth gauge") {
		t.Error("queue depth is not typed as a gauge")
	}
}

// The full disk as an operator sees it: a non-zero write-failure counter while
// /health and /ready both still answer 200. That combination is the entire
// point of the endpoint, so it is asserted together rather than separately.
func TestMetricsShowsTheFailureThatProbesMiss(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithMetrics(fakeMetrics{m: monitor.Metrics{
		ChecksRecorded:         100,
		HeartbeatWriteFailures: 512,
	}})
	h := authedHandler(srv)

	for _, path := range []string{"/health", "/api/v1/ready"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d; this test assumes the probes stay green", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "subglance_heartbeat_write_failures_total 512") {
		t.Error("the probes are green and /metrics reports nothing; " +
			"a full disk is still invisible to everything the instance exposes")
	}
}

// /metrics requires credentials, unlike /health and /ready beside it.
//
// The counters name how many monitors an instance watches and when its writes
// are failing — a fleet inventory, and a live signal of when the operator is
// least able to notice anything, to anyone who can reach the port.
func TestMetricsIsNotPublic(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithMetrics(fakeMetrics{m: monitor.Metrics{Scheduled: 200}})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — /metrics is readable without credentials", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "subglance_monitors_scheduled") {
		t.Error("an anonymous request was served the counters")
	}
}

// An API assembled without a checker pipeline says so, rather than panicking
// on a nil source — the same degradation the stream, prober and push endpoints
// already have.
func TestMetricsWithoutACheckerPipeline(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
