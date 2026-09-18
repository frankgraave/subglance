package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The full disk, simulated honestly.
//
// What this exercises: a real Runner with a real state engine, recording real
// outcomes, against a database whose writes fail. The failure is produced by
// closing the store rather than by a hand-written double that returns an
// error — a double would only prove that the code counts what the double
// says. A closed SQLite pool fails RecordHeartbeat exactly where a full disk
// fails it: inside the write, after everything up to it has succeeded.
//
// What it must show: the liveness counter stops moving. That is the whole
// defect. It used to be incremented before the write, so on a full disk it
// kept climbing, the watchdog kept pinging "alive", /health and /ready both
// stayed 200, and SubGlance recorded nothing for hours while looking perfectly
// healthy to every probe it exposes.
func TestFailedWritesStopFeedingTheWatchdog(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})

	// Healthy first, so there is a baseline that is demonstrably moving.
	r.record(outcomeFor(m, "https://example.com", true))
	healthy := r.ChecksCompleted()
	if healthy != 1 {
		t.Fatalf("ChecksCompleted after one recorded check = %d, want 1", healthy)
	}
	if got := r.Metrics().HeartbeatWriteFailures; got != 0 {
		t.Fatalf("HeartbeatWriteFailures = %d before any failure, want 0", got)
	}

	// Now the disk is full: every write fails from here.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for range 5 {
		r.record(outcomeFor(m, "https://example.com", true))
	}

	if got := r.ChecksCompleted(); got != healthy {
		t.Errorf("ChecksCompleted = %d after 5 failed writes, want it frozen at %d — "+
			"the watchdog is being fed by checks that were never recorded",
			got, healthy)
	}

	mt := r.Metrics()
	if mt.HeartbeatWriteFailures != 5 {
		t.Errorf("HeartbeatWriteFailures = %d, want 5 — a full disk is not visible on /metrics",
			mt.HeartbeatWriteFailures)
	}
	if mt.ChecksRecorded != healthy {
		t.Errorf("ChecksRecorded = %d, want %d", mt.ChecksRecorded, healthy)
	}
}

// recordOutcome still reports the write failure to its caller. The push
// endpoint answers a job with it, so a counter must not have replaced an
// error.
func TestFailedWriteIsStillReportedToTheCaller(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = r.recordOutcome(outcomeFor(m, "https://example.com", true))
	if err == nil {
		t.Fatal("recordOutcome reported success against a closed database")
	}
	if !strings.Contains(err.Error(), "record heartbeat") {
		t.Errorf("error does not say the heartbeat write failed: %v", err)
	}
}

// The counter must move on every successful write, not only the first: a
// liveness signal that stalls on a healthy instance is a false alarm, which is
// how a dead man's switch gets turned off.
func TestRecordedChecksKeepCounting(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 5, Retries: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	r := New(Options{DB: db, Log: quietLogger()})
	for i := range 4 {
		r.record(at(outcomeFor(m, "https://example.com", true),
			time.Now().Add(time.Duration(i)*time.Second)))
	}

	if got := r.ChecksCompleted(); got != 4 {
		t.Errorf("ChecksCompleted = %d after 4 successful writes, want 4", got)
	}
}
