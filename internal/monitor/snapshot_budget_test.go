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

// Only a snapshot that reached the disk may spend the budget. Several kinds of
// failure store nothing at all — the response was never captured, the monitor
// is flapping, the write failed — and charging those meant an outage could run
// out of allowance before it had stored a single body. Each case below drains
// the budget the old way, then asserts that a genuinely eligible failure in the
// same incident still gets stored.
func TestOnlyAStoredSnapshotSpendsTheBudget(t *testing.T) {
	const window = time.Hour

	cases := []struct {
		name  string
		setup func(t *testing.T, db *store.DB) (*Runner, store.Monitor)
		// drain records failures that must store nothing, and returns an
		// outcome that is eligible and has to be stored.
		drain func(t *testing.T, db *store.DB, r *Runner, m store.Monitor) scheduler.Outcome
	}{
		{
			// The checker hands over no body — a connection refused, a
			// timeout, a monitor with capture switched off.
			name: "a failure with no response body",
			setup: func(t *testing.T, db *store.DB) (*Runner, store.Monitor) {
				return recordingRunner(t, db), captureMonitorRow(t, db)
			},
			drain: func(t *testing.T, db *store.DB, r *Runner, m store.Monitor) scheduler.Outcome {
				for range maxSnapshotsPerIncident + 2 {
					r.record(outcomeFor(m, m.Target, false))
				}
				if got := storedSnapshots(t, db); got != 0 {
					t.Fatalf("stored %d snapshots for failures without a body, want 0", got)
				}
				return failureWithBody(m, "the first body of this outage")
			},
		},
		{
			// The heartbeat and its response are written in one
			// transaction, so a write error stored neither.
			name: "a failure whose heartbeat write fails",
			setup: func(t *testing.T, db *store.DB) (*Runner, store.Monitor) {
				return recordingRunner(t, db), captureMonitorRow(t, db)
			},
			drain: func(t *testing.T, db *store.DB, r *Runner, m store.Monitor) scheduler.Outcome {
				exec(t, db, "ALTER TABLE heartbeat_responses RENAME TO heartbeat_responses_away")
				for range maxSnapshotsPerIncident + 2 {
					r.record(failureWithBody(m, "lost to a failed write"))
				}
				exec(t, db, "ALTER TABLE heartbeat_responses_away RENAME TO heartbeat_responses")
				if got := storedSnapshots(t, db); got != 0 {
					t.Fatalf("stored %d snapshots while writes failed, want 0", got)
				}
				return failureWithBody(m, "the first body that could be written")
			},
		},
		{
			// Suppression while flapping ends when the recorded flips age
			// out, and that can happen part-way through an outage that
			// never recovered. The failures suppressed before then must
			// not have spent the budget the rest of the outage needs.
			name: "failures suppressed while the monitor flaps",
			setup: func(t *testing.T, db *store.DB) (*Runner, store.Monitor) {
				r := New(Options{
					DB: db, Log: quietLogger(), AllowPrivateTargets: true,
					FlapWindow: window, FlapThreshold: 2,
				})
				// One retry, so every failure confirms and every recovery
				// resolves: that is what a flip is.
				m, err := db.CreateMonitor(context.Background(), store.Monitor{
					Name: "flaps", Type: "http", Target: "https://example.com/health",
					Enabled: true, CaptureResponse: true, Retries: 1,
				})
				if err != nil {
					t.Fatalf("CreateMonitor: %v", err)
				}
				return r, m
			},
			drain: func(t *testing.T, db *store.DB, r *Runner, m store.Monitor) scheduler.Outcome {
				clock := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
				tick := func() time.Time {
					clock = clock.Add(time.Second)
					return clock
				}
				for i := range 10 {
					r.record(at(failureWithBody(m, fmt.Sprintf("flap %d", i)), tick()))
					r.record(at(outcomeFor(m, m.Target, true), tick()))
				}
				if !r.engine.Flapping(m.ID) {
					t.Fatalf("precondition: the monitor should be flapping after ten flips")
				}

				// The outage that stays. Every one of these is suppressed.
				before := storedSnapshots(t, db)
				for range maxSnapshotsPerIncident + 2 {
					r.record(at(failureWithBody(m, "suppressed"), tick()))
				}
				if got := storedSnapshots(t, db); got != before {
					t.Fatalf("stored %d snapshots while flapping, want %d", got, before)
				}

				// The flips age out, but the incident never closed.
				clock = clock.Add(3 * window)
				return at(failureWithBody(m, "still down after the flapping settled"), tick())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			r, m := tc.setup(t, db)

			eligible := tc.drain(t, db, r, m)

			before := storedSnapshots(t, db)
			r.record(eligible)

			if got := storedSnapshots(t, db); got != before+1 {
				t.Errorf("stored %d snapshots, want %d: failures that stored "+
					"nothing spent the budget this one needed", got, before+1)
			}
		})
	}
}

func exec(t *testing.T, db *store.DB, q string) {
	t.Helper()
	if _, err := db.Writer.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
