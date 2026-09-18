package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/monitor"
)

// MetricsSource supplies the operational counters served on /metrics.
//
// An interface rather than *monitor.Runner for the same reason as Prober and
// PushRecorder: the handler is testable without a scheduler behind it, and a
// build wired without a checker pipeline degrades to a clear 503.
type MetricsSource interface {
	Metrics() monitor.Metrics
}

// WithMetrics attaches a metrics source, enabling GET /metrics.
func (s *Server) WithMetrics(m MetricsSource) *Server {
	s.metrics = m
	return s
}

// handleMetrics serves the counters in Prometheus text exposition format.
//
// # Why this is hand-rolled
//
// The format is six lines of text per counter and is specified as such. A
// client library would bring a dependency, a global registry and a collector
// abstraction to produce the same bytes, into a product whose distribution
// promise is one static binary. There is nothing here that needs a library: no
// histograms, no labels beyond a fixed set, no exemplars.
//
// # What these counters are for
//
// The failure this endpoint exists for is the one where SubGlance keeps
// answering every probe while doing nothing. The disk fills; every heartbeat
// write fails; /health returns 200 because the process is alive, /ready
// returns 200 because a read-only SQLite still answers a ping, and the only
// other signal is error lines in a log nobody is grepping.
//
// subglance_heartbeat_write_failures_total is that failure, as a number an
// operator can alert on. Beside it, subglance_checks_recorded_total is the
// evidence of normal operation: during the full-disk failure it is flat while
// the failure counter climbs, which distinguishes "cannot write" from "the
// scheduler wedged", where both are flat.
//
// The queue readings answer the other half of the ticket: skipped checks and
// queue depth both rise when the worker pool is saturated, which is what
// happens during a broad outage and is invisible from the outside otherwise.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeProblem(w, http.StatusServiceUnavailable, bodyProblem(
			"no checker pipeline is attached to this instance, so there are no metrics to report"))
		return
	}

	m := s.metrics.Metrics()

	var b strings.Builder
	counter(&b, "subglance_checks_recorded_total",
		"Checks whose heartbeat was written to the database.", m.ChecksRecorded)
	counter(&b, "subglance_heartbeat_write_failures_total",
		"Heartbeats that could not be written, for example because the disk is full.",
		m.HeartbeatWriteFailures)
	counter(&b, "subglance_rollup_failures_total",
		"Retention passes that failed.", m.RollupFailures)
	counter(&b, "subglance_checks_skipped_total",
		"Checks skipped because the previous run of that monitor had not finished.",
		m.SkippedChecks)
	gauge(&b, "subglance_check_queue_depth",
		"Dispatched checks waiting for a free worker.", uint64(m.QueueDepth))
	gauge(&b, "subglance_check_workers",
		"Size of the check worker pool.", uint64(m.Workers))
	gauge(&b, "subglance_monitors_scheduled",
		"Monitors currently on the schedule.", uint64(m.Scheduled))

	// Not application/json, so writeJSON is the wrong helper here. The
	// version parameter is what Prometheus itself sends and expects back.
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// counter and gauge write one metric in the exposition format: a HELP line, a
// TYPE line, then the sample. They are two functions rather than one with a
// type argument because the type is the only difference and naming it at the
// call site is what makes the choice visible in the handler above.
func counter(b *strings.Builder, name, help string, v uint64) {
	writeMetric(b, name, help, "counter", v)
}

func gauge(b *strings.Builder, name, help string, v uint64) {
	writeMetric(b, name, help, "gauge", v)
}

func writeMetric(b *strings.Builder, name, help, kind string, v uint64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s %s\n", name, kind)
	fmt.Fprintf(b, "%s %d\n", name, v)
}
