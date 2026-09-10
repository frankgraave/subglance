package store

import (
	"context"
	"testing"
	"time"
)

// seedMonitor creates a monitor to hang heartbeats off. Heartbeats have a
// foreign key, so tests cannot use an arbitrary id.
func seedMonitor(t *testing.T, db *DB, name string) int64 {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), Monitor{
		Name: name, Type: "http", Target: "https://example.com",
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m.ID
}

func beat(t *testing.T, db *DB, id int64, ts time.Time, ok bool, latency int) {
	t.Helper()
	if err := db.RecordHeartbeat(context.Background(), Heartbeat{
		MonitorID: id, TS: ts, OK: ok, LatencyMS: latency,
	}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
}

type bucketRow struct {
	upCount, downCount   int
	latMin, latMax       int
	latAvg, latencyCount int
}

func readBucket(t *testing.T, db *DB, id int64, bucket time.Time) bucketRow {
	t.Helper()
	var b bucketRow
	err := db.Reader.QueryRowContext(context.Background(), `
		SELECT up_count, down_count,
		       COALESCE(latency_min, 0), COALESCE(latency_max, 0),
		       COALESCE(latency_avg, 0), latency_count
		FROM heartbeat_hourly WHERE monitor_id = ? AND bucket = ?`,
		id, bucket.Unix(),
	).Scan(&b.upCount, &b.downCount, &b.latMin, &b.latMax, &b.latAvg, &b.latencyCount)
	if err != nil {
		t.Fatalf("read bucket %s: %v", bucket.UTC(), err)
	}
	return b
}

func countRaw(t *testing.T, db *DB, id int64) int {
	t.Helper()
	var n int
	if err := db.Reader.QueryRowContext(context.Background(),
		"SELECT count(*) FROM heartbeats WHERE monitor_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	return n
}

func TestRollupAggregatesAndDeletesOldBeats(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "rollup")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	oldHour := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour)

	beat(t, db, id, oldHour.Add(1*time.Minute), true, 100)
	beat(t, db, id, oldHour.Add(2*time.Minute), true, 300)
	beat(t, db, id, oldHour.Add(3*time.Minute), false, 0)

	// Recent beats must survive untouched.
	beat(t, db, id, now.Add(-time.Hour), true, 50)

	res, err := db.rollupAt(context.Background(), now, DefaultRawRetention)
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if res.Heartbeats != 3 {
		t.Errorf("rolled up %d heartbeats, want 3", res.Heartbeats)
	}
	if got := countRaw(t, db, id); got != 1 {
		t.Errorf("%d raw heartbeats left, want 1 (the recent one)", got)
	}

	b := readBucket(t, db, id, oldHour)
	if b.upCount != 2 || b.downCount != 1 {
		t.Errorf("bucket counts = up %d down %d, want up 2 down 1", b.upCount, b.downCount)
	}
	// A failed check records no latency, so it must not drag the average to
	// (100+300+0)/3 = 133.
	if b.latAvg != 200 {
		t.Errorf("latency_avg = %d, want 200 (failed check excluded)", b.latAvg)
	}
	if b.latencyCount != 2 {
		t.Errorf("latency_count = %d, want 2", b.latencyCount)
	}
	if b.latMin != 100 || b.latMax != 300 {
		t.Errorf("latency range = [%d,%d], want [100,300]", b.latMin, b.latMax)
	}
}

// A second pass over the same hour must merge, not overwrite. This is the case
// that a naive INSERT OR REPLACE silently loses, and it happens whenever a
// heartbeat lands in an hour that has already been rolled up.
func TestRollupMergesIntoExistingBucket(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "merge")

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	oldHour := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	ctx := context.Background()

	beat(t, db, id, oldHour.Add(time.Minute), true, 100)
	if _, err := db.rollupAt(ctx, now, DefaultRawRetention); err != nil {
		t.Fatalf("first rollup: %v", err)
	}

	// A late beat for the same, already-summarised hour.
	beat(t, db, id, oldHour.Add(2*time.Minute), false, 0)
	beat(t, db, id, oldHour.Add(3*time.Minute), true, 400)
	if _, err := db.rollupAt(ctx, now, DefaultRawRetention); err != nil {
		t.Fatalf("second rollup: %v", err)
	}

	b := readBucket(t, db, id, oldHour)
	if b.upCount != 2 || b.downCount != 1 {
		t.Errorf("merged counts = up %d down %d, want up 2 down 1", b.upCount, b.downCount)
	}
	if b.latencyCount != 2 {
		t.Errorf("merged latency_count = %d, want 2", b.latencyCount)
	}
	// Weighted across both passes: (100 + 400) / 2.
	if b.latAvg != 250 {
		t.Errorf("merged latency_avg = %d, want 250", b.latAvg)
	}
	if b.latMin != 100 || b.latMax != 400 {
		t.Errorf("merged latency range = [%d,%d], want [100,400]", b.latMin, b.latMax)
	}
}

// The cutoff is truncated to an hour boundary so a bucket is never rolled up
// while it can still receive beats.
//
// The beat that matters is the one in the gap between the raw cutoff and the
// hour boundary below it: with truncation it stays raw, without truncation it
// is folded into a bucket that the next pass will have to merge into again.
func TestRollupLeavesCurrentBucketAlone(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "boundary")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	retention := time.Hour
	// Raw cutoff is 11:30; truncated to the hour it becomes 11:00.

	// 11:15 sits in the gap: older than the raw cutoff, but in the 11:00
	// bucket which is still open. It must survive.
	beat(t, db, id, now.Add(-75*time.Minute), true, 10)
	// 11:45 is newer than the cutoff either way.
	beat(t, db, id, now.Add(-45*time.Minute), true, 15)
	// 10:45 is in a bucket that is safely closed.
	beat(t, db, id, now.Add(-105*time.Minute), true, 20)

	res, err := db.rollupAt(context.Background(), now, retention)
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if res.Heartbeats != 1 {
		t.Fatalf("rolled up %d heartbeats, want 1 (only the closed 10:00 bucket)", res.Heartbeats)
	}
	if got := countRaw(t, db, id); got != 2 {
		t.Errorf("%d raw heartbeats left, want 2 (11:15 and 11:45)", got)
	}
}

func TestRollupIsIdempotentOnEmptyRange(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "empty")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	beat(t, db, id, now.Add(-time.Minute), true, 10)

	for i := range 2 {
		res, err := db.rollupAt(context.Background(), now, DefaultRawRetention)
		if err != nil {
			t.Fatalf("rollup %d: %v", i, err)
		}
		if res.Heartbeats != 0 {
			t.Errorf("pass %d rolled up %d heartbeats, want 0", i, res.Heartbeats)
		}
	}
	if got := countRaw(t, db, id); got != 1 {
		t.Errorf("%d raw heartbeats left, want 1", got)
	}
}

// Uptime must report the same figures before and after a rollup. If it does
// not, history visibly changes shape the moment a background task runs, which
// is exactly the kind of quiet lie a monitoring tool cannot afford.
func TestUptimeSpansRollupBoundary(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "boundary-uptime")
	ctx := context.Background()

	// Beats spread over 20 days, all inside a 30-day window.
	base := time.Now().Add(-20 * 24 * time.Hour).Truncate(time.Hour)
	for i := range 20 {
		ts := base.Add(time.Duration(i) * 24 * time.Hour)
		ok := i%5 != 0 // 4 down out of 20
		lat := 0
		if ok {
			lat = 100
		}
		beat(t, db, id, ts, ok, lat)
	}

	before, err := db.Uptime(ctx, id, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Uptime before: %v", err)
	}
	if before.Total != 20 || before.Up != 16 {
		t.Fatalf("before rollup: total %d up %d, want 20/16", before.Total, before.Up)
	}

	if _, err := db.RollupHeartbeats(ctx, DefaultRawRetention); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if countRaw(t, db, id) == 20 {
		t.Fatal("rollup moved nothing; test would prove nothing")
	}

	after, err := db.Uptime(ctx, id, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Uptime after: %v", err)
	}
	if after.Total != before.Total || after.Up != before.Up || after.Down != before.Down {
		t.Errorf("uptime changed across rollup: before %+v, after %+v", before, after)
	}
	if after.AvgLatency != before.AvgLatency {
		t.Errorf("avg latency changed across rollup: %d -> %d", before.AvgLatency, after.AvgLatency)
	}
	if after.Percentage != before.Percentage {
		t.Errorf("uptime %% changed across rollup: %.2f -> %.2f", before.Percentage, after.Percentage)
	}
}
