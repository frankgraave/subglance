package monitor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A flapping monitor is the case the streak-shaped budget cannot see.
//
// Every recovery resolves the incident and clears the streak, so every
// following failure starts at one — permanently inside the budget. Before the
// flapping rule, a monitor oscillating once a minute wrote a snapshot on every
// single failed check, forever, which is the disk growth this ticket is about.

// flappingRunner builds a runner whose engine calls a monitor flapping after
// two confirmed flips, so a test can reach that state in a handful of checks
// instead of ten.
func flappingRunner(t *testing.T, db *store.DB) *Runner {
	t.Helper()
	return New(Options{
		DB:                  db,
		Log:                 quietLogger(),
		AllowPrivateTargets: true,
		FlapWindow:          time.Hour,
		FlapThreshold:       2,
	})
}

func TestAFlappingMonitorStopsStoringResponses(t *testing.T) {
	db := testDB(t)
	r := flappingRunner(t, db)

	// Retries 1 so a single failure confirms the incident, which is what
	// counts as a flip.
	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "oscillator", Type: "http", Target: "https://example.com/health",
		Enabled: true, CaptureResponse: true, Retries: 1,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// Oscillate: fail, recover, fail, recover, ... Each failure is its own
	// incident with a streak of exactly one.
	const cycles = 20
	for i := range cycles {
		r.record(failureWithBody(m, fmt.Sprintf("flap %d", i)))
		r.record(outcomeFor(m, m.Target, true))
	}

	got := storedSnapshots(t, db)

	// Some snapshots are expected: the monitor is not yet known to be
	// flapping on its first failures, and that is correct — those are the
	// ones that explain what is going on. What must not happen is one per
	// failure.
	if got >= cycles {
		t.Errorf("stored %d snapshots over %d failure/recovery cycles: "+
			"the budget resets on every recovery, so flapping is unbounded", got, cycles)
	}
	if got == 0 {
		t.Error("stored no snapshots at all: the first failures of an oscillation " +
			"are exactly the ones worth keeping")
	}

	// Every cycle is still two heartbeats. Only the bodies are rationed.
	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 1000)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != cycles*2 {
		t.Errorf("recorded %d heartbeats, want %d — the rule dropped beats, not just bodies",
			len(hbs), cycles*2)
	}
}

// Once the oscillation stops, capture has to come back. A rule that silenced a
// monitor permanently after one bad afternoon would be worse than the leak.
func TestCaptureResumesAfterTheFlappingSettles(t *testing.T) {
	db := testDB(t)

	// A short flap window so the recorded flips can genuinely expire inside
	// the test rather than being simulated away with a second runner.
	const window = 100 * time.Millisecond
	r := New(Options{
		DB:                  db,
		Log:                 quietLogger(),
		AllowPrivateTargets: true,
		FlapWindow:          window,
		FlapThreshold:       2,
	})

	m, err := db.CreateMonitor(context.Background(), store.Monitor{
		Name: "settles", Type: "http", Target: "https://example.com/health",
		Enabled: true, CaptureResponse: true, Retries: 1,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	for i := range 10 {
		r.record(failureWithBody(m, fmt.Sprintf("flap %d", i)))
		r.record(outcomeFor(m, m.Target, true))
	}
	if !r.engine.Flapping(m.ID) {
		t.Fatal("precondition: the monitor should be flapping after ten flips")
	}
	before := storedSnapshots(t, db)

	// Let every recorded flip fall out of the window, which is what a
	// monitor that settled down looks like to the engine.
	time.Sleep(3 * window)

	r.record(failureWithBody(m, "after settling"))

	if got := storedSnapshots(t, db); got != before+1 {
		t.Errorf("snapshots after settling = %d, want %d: a monitor that stopped "+
			"flapping must be captured again", got, before+1)
	}
}

// The other way one outage spends its budget more than once: a restart.
//
// The budget lives on the in-memory failure streak, so before this fix a
// restart during a long outage handed the same incident a fresh allowance. An
// outage that outlives a few container restarts — a routine thing on a
// self-hosted box — therefore wrote the same error page again on every boot.
func TestARestartDoesNotRefillTheSnapshotBudget(t *testing.T) {
	db := testDB(t)
	first := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	// A long outage on the first process. It spends its whole budget.
	for i := range 10 {
		first.record(failureWithBody(m, fmt.Sprintf("before restart %d", i)))
	}
	spent := storedSnapshots(t, db)
	if spent != maxSnapshotsPerIncident {
		t.Fatalf("precondition: stored %d snapshots, want %d", spent, maxSnapshotsPerIncident)
	}

	// Restart: a brand new runner with an empty engine, seeded from the
	// incident the first one left open.
	second := recordingRunner(t, db)
	if err := second.restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// The same outage continues.
	for i := range 10 {
		second.record(failureWithBody(m, fmt.Sprintf("after restart %d", i)))
	}

	if got := storedSnapshots(t, db); got != spent {
		t.Errorf("stored %d snapshots after the restart, want %d: the restart "+
			"refilled a budget this outage had already spent", got, spent)
	}
}
