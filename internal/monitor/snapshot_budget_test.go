package monitor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/store"
)

// The storage cost of response capture is the whole reason it is bounded. A
// monitor checked every 60 seconds that stays broken for a week produces about
// ten thousand failures; storing 2 KiB for each would be 20 MB to record one
// fact. These tests pin the budget: the first few failures of a streak carry a
// snapshot, the rest do not, and a recovery starts the allowance over.

// failureWithBody builds the outcome of a failed check that captured a body.
func failureWithBody(m store.Monitor, body string) scheduler.Outcome {
	cm := toCheckerMonitor(m)
	return scheduler.Outcome{
		Monitor: cm,
		Result: checker.Result{
			OK:         false,
			StatusCode: 503,
			Kind:       checker.FailStatus,
			Error:      "status 503, expected 200-299",
			Latency:    5 * time.Millisecond,
			CheckedAt:  time.Now(),
			Response:   &checker.ResponseSnapshot{Body: body},
		},
	}
}

func storedSnapshots(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.Reader.QueryRowContext(context.Background(),
		"SELECT count(*) FROM heartbeat_responses").Scan(&n); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

func recordingRunner(t *testing.T, db *store.DB) *Runner {
	t.Helper()
	return New(Options{DB: db, Log: quietLogger(), AllowPrivateTargets: true})
}

func captureMonitorRow(t *testing.T, db *store.DB) store.Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "captures", Type: "http", Target: "https://example.com/health",
		Enabled: true, CaptureResponse: true, Retries: 2,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

// A long outage stores the first few responses and then stops. Without the
// bound this test would store one snapshot per failure.
func TestLongOutageStoresOnlyTheFirstFewResponses(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	const failures = 25
	for i := range failures {
		r.record(failureWithBody(m, fmt.Sprintf("failure %d", i)))
	}

	got := storedSnapshots(t, db)
	if got != maxSnapshotsPerIncident {
		t.Errorf("stored %d snapshots over %d failures, want %d",
			got, failures, maxSnapshotsPerIncident)
	}

	// Every failure is still a heartbeat; only the bodies are rationed.
	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 1000)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != failures {
		t.Errorf("recorded %d heartbeats, want %d — the bound dropped beats, not just bodies",
			len(hbs), failures)
	}
}

// The bound keeps the earliest failures, not the latest. The first failure is
// the one that says how the outage began, and it is what someone reading the
// incident in the morning opens first.
func TestTheStoredResponsesAreTheEarliestOnes(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	for i := range 10 {
		r.record(failureWithBody(m, fmt.Sprintf("failure %d", i)))
	}

	// Ordered by heartbeat id rather than by timestamp. Heartbeats are
	// stored at second resolution, so ten results recorded in a tight loop
	// all share one `ts` and any order derived from it is a coin toss. The
	// id is the insertion order, which is what "earliest" means here.
	rows, err := db.Reader.QueryContext(context.Background(),
		"SELECT body FROM heartbeat_responses ORDER BY heartbeat_id")
	if err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want := []string{"failure 0", "failure 1", "failure 2"}
	if len(bodies) != len(want) {
		t.Fatalf("stored bodies = %v, want %v", bodies, want)
	}
	for i := range want {
		if bodies[i] != want[i] {
			t.Errorf("stored body %d = %q, want %q", i, bodies[i], want[i])
		}
	}
}

// A recovery resets the allowance, so the next outage is captured too. Without
// this, one long outage early in a monitor's life would silence every capture
// after it.
func TestRecoveryRestoresTheSnapshotAllowance(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	for range 10 {
		r.record(failureWithBody(m, "first outage"))
	}
	first := storedSnapshots(t, db)

	// Recover, then fail again.
	r.record(outcomeFor(m, m.Target, true))
	r.record(failureWithBody(m, "second outage"))

	if got := storedSnapshots(t, db); got != first+1 {
		t.Errorf("snapshots after the second outage = %d, want %d — "+
			"the allowance did not reset on recovery", got, first+1)
	}
}

// A successful check never stores a body, even if the checker handed one over.
// Capture exists to explain failures; a green check has nothing to explain.
func TestSuccessNeverStoresAResponse(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	out := failureWithBody(m, "should not be stored")
	out.Result.OK = true
	out.Result.StatusCode = 200
	out.Result.Kind = ""
	out.Result.Error = ""
	r.record(out)

	if got := storedSnapshots(t, db); got != 0 {
		t.Errorf("stored %d snapshots for a successful check", got)
	}
}
