package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// failedBeat records one failed heartbeat carrying snap, and returns nothing:
// the point of every test here is what comes back out of a read.
func failedBeat(t *testing.T, db *DB, monitorID int64, at time.Time, snap *ResponseSnapshot) {
	t.Helper()
	err := db.RecordHeartbeat(context.Background(), Heartbeat{
		MonitorID:  monitorID,
		TS:         at,
		OK:         false,
		LatencyMS:  12,
		StatusCode: 503,
		Error:      "status 503, expected 200-299",
		Response:   snap,
	})
	if err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
}

func TestSnapshotSurvivesTheRoundTrip(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "snapshot")
	at := time.Unix(1_700_000_000, 0).UTC()

	failedBeat(t, db, m.ID, at, &ResponseSnapshot{
		Body: `{"detail":"upstream database timeout"}`,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Retry-After":  "120",
		},
		Truncated: true,
	})

	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(hbs))
	}
	got := hbs[0].Response
	if got == nil {
		t.Fatal("the snapshot did not come back with its heartbeat")
	}
	if got.Body != `{"detail":"upstream database timeout"}` {
		t.Errorf("body = %q", got.Body)
	}
	if got.Headers["Retry-After"] != "120" {
		t.Errorf("headers = %v, want Retry-After 120", got.Headers)
	}
	if !got.Truncated {
		t.Error("truncated was not preserved")
	}
}

// A heartbeat with no snapshot must read back with none, rather than an empty
// struct a UI would render as a captured-but-blank response.
func TestHeartbeatWithoutSnapshotStaysNil(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "plain")

	failedBeat(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), nil)

	hbs, err := db.ListHeartbeats(context.Background(), m.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if hbs[0].Response != nil {
		t.Fatalf("invented a snapshot: %+v", hbs[0].Response)
	}
}

// Snapshots must land on the heartbeat they belong to. A join keyed on the
// wrong column would still return the right count, so the beats are given
// distinguishable bodies and checked individually.
func TestSnapshotsAttachToTheRightHeartbeats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mine := newTestMonitor(t, db, "mine")
	theirs := newTestMonitor(t, db, "theirs")
	base := time.Unix(1_700_000_000, 0).UTC()

	failedBeat(t, db, mine.ID, base, &ResponseSnapshot{Body: "first of mine"})
	failedBeat(t, db, theirs.ID, base.Add(time.Second), &ResponseSnapshot{Body: "not mine at all"})
	failedBeat(t, db, mine.ID, base.Add(2*time.Second), &ResponseSnapshot{Body: "second of mine"})

	hbs, err := db.ListHeartbeats(ctx, mine.ID, 10)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(hbs) != 2 {
		t.Fatalf("got %d heartbeats, want 2", len(hbs))
	}
	// Newest first.
	if hbs[0].Response == nil || hbs[0].Response.Body != "second of mine" {
		t.Errorf("newest beat carries %+v", hbs[0].Response)
	}
	if hbs[1].Response == nil || hbs[1].Response.Body != "first of mine" {
		t.Errorf("older beat carries %+v", hbs[1].Response)
	}
}

// Deleting a monitor must take its snapshots with it. Without the cascade they
// would outlive the monitor and the target's response would sit in the
// database with nothing left to explain where it came from.
func TestDeletingAMonitorRemovesItsSnapshots(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := newTestMonitor(t, db, "doomed")

	failedBeat(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(),
		&ResponseSnapshot{Body: "should not outlive its monitor"})

	if err := db.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	var n int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM heartbeat_responses").Scan(&n); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if n != 0 {
		t.Errorf("%d snapshots outlived their monitor", n)
	}
}

// The rollup deletes raw heartbeats once they age out. Snapshots must go with
// them: a retention policy that covers heartbeats but leaks their bodies is
// worse than no policy, because it looks like one.
func TestRollupTakesSnapshotsWithIt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := newTestMonitor(t, db, "aging")

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	failedBeat(t, db, m.ID, old, &ResponseSnapshot{Body: "an answer from a month ago"})
	failedBeat(t, db, m.ID, now, &ResponseSnapshot{Body: "an answer from just now"})

	if _, err := db.RollupHeartbeats(ctx, testRawWindow); err != nil {
		t.Fatalf("RollupHeartbeats: %v", err)
	}

	var bodies []string
	rows, err := db.Reader.QueryContext(ctx, "SELECT body FROM heartbeat_responses")
	if err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	defer func() { _ = rows.Close() }()
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

	if len(bodies) != 1 {
		t.Fatalf("snapshots after rollup = %v, want only the recent one", bodies)
	}
	if !strings.Contains(bodies[0], "just now") {
		t.Errorf("the surviving snapshot is %q, want the recent one", bodies[0])
	}
}

// capture_response is stored as given, exactly like enabled: the store keeps
// what the caller states and the API supplies the product default. Two places
// deciding the same default is how they end up disagreeing.
//
// The column's own DEFAULT 1 is not dead weight — it is what backfills
// monitors that already existed when the migration ran, so an upgrade turns
// the feature on for them rather than silently leaving it off.
func TestCaptureResponseIsStoredAsGiven(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	on, err := db.CreateMonitor(ctx, Monitor{
		Name: "on", Type: "http", Target: "https://on.example.com",
		Enabled: true, CaptureResponse: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	read, err := db.GetMonitor(ctx, on.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !read.CaptureResponse {
		t.Error("capture_response did not survive a create and read")
	}

	off, err := db.CreateMonitor(ctx, Monitor{
		Name: "off", Type: "http", Target: "https://off.example.com",
		Enabled: true, CaptureResponse: false,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	read, err = db.GetMonitor(ctx, off.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if read.CaptureResponse {
		t.Error("a monitor created with capture off came back with it on")
	}
}

// An existing monitor must come out of the migration with capture on, not off.
// A feature that silently stays dark for every monitor that predates it is one
// nobody discovers until the night they needed it.
func TestMigrationBackfillsCaptureOnForExistingMonitors(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Insert without naming the column, which is what a row written before
	// the migration looks like once the column has been added.
	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('legacy', 'http', 'https://legacy.example.com', 0, 0)`); err != nil {
		t.Fatalf("insert legacy monitor: %v", err)
	}

	var capture bool
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT capture_response FROM monitors WHERE name = 'legacy'").Scan(&capture); err != nil {
		t.Fatalf("read capture_response: %v", err)
	}
	if !capture {
		t.Error("a row that does not name the column got capture off; the backfill default is wrong")
	}
}

// Turning capture off must survive an update. This is the switch an operator
// uses to keep a target's responses out of the database, so a write path that
// quietly resets it to the default would be a privacy failure, not a bug in a
// display field.
func TestCaptureResponseSurvivesAnUpdate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := newTestMonitor(t, db, "sensitive")

	m.CaptureResponse = true
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor turning capture on: %v", err)
	}

	m.CaptureResponse = false
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	read, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if read.CaptureResponse {
		t.Error("capture was switched back on by an unrelated update")
	}
}
