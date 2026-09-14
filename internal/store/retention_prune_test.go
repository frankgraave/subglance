package store

import (
	"context"
	"testing"
	"time"
)

func countHourly(t *testing.T, db *DB, id int64) int {
	t.Helper()
	var n int
	if err := db.Reader.QueryRowContext(context.Background(),
		"SELECT count(*) FROM heartbeat_hourly WHERE monitor_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("count hourly: %v", err)
	}
	return n
}

func countIncidents(t *testing.T, db *DB, id int64) int {
	t.Helper()
	var n int
	if err := db.Reader.QueryRowContext(context.Background(),
		"SELECT count(*) FROM incidents WHERE monitor_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	return n
}

// insertBucket writes an hourly row directly. Going through a rollup would
// need thousands of raw beats to place a bucket at an arbitrary age.
func insertBucket(t *testing.T, db *DB, id int64, bucket time.Time) {
	t.Helper()
	if _, err := db.Writer.ExecContext(context.Background(), `
		INSERT INTO heartbeat_hourly
			(monitor_id, bucket, up_count, down_count, latency_count)
		VALUES (?, ?, 60, 0, 60)`, id, bucket.Unix()); err != nil {
		t.Fatalf("insert bucket: %v", err)
	}
}

func TestApplyRetentionPrunesOldHourlyBuckets(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "prune-buckets")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	policy := RetentionPolicy{Raw: 7 * 24 * time.Hour, Rollup: 30 * 24 * time.Hour}

	insertBucket(t, db, id, now.Add(-40*24*time.Hour).Truncate(time.Hour))
	insertBucket(t, db, id, now.Add(-31*24*time.Hour).Truncate(time.Hour))
	insertBucket(t, db, id, now.Add(-10*24*time.Hour).Truncate(time.Hour))

	res, err := db.applyRetentionAt(context.Background(), now, policy)
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.HourlyBuckets != 2 {
		t.Errorf("pruned %d buckets, want 2", res.HourlyBuckets)
	}
	if got := countHourly(t, db, id); got != 1 {
		t.Errorf("%d hourly buckets left, want 1 (the recent one)", got)
	}
}

func TestApplyRetentionKeepsEverythingWhenRollupRetentionIsZero(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "keep-forever")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	insertBucket(t, db, id, now.Add(-5*365*24*time.Hour).Truncate(time.Hour))

	res, err := db.applyRetentionAt(context.Background(), now,
		RetentionPolicy{Raw: 7 * 24 * time.Hour, Rollup: 0})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.HourlyBuckets != 0 {
		t.Errorf("pruned %d buckets, want 0: zero rollup retention means keep forever", res.HourlyBuckets)
	}
	if got := countHourly(t, db, id); got != 1 {
		t.Errorf("%d hourly buckets left, want 1", got)
	}
}

func TestApplyRetentionPrunesResolvedIncidentsButKeepsOpenOnes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	old := seedMonitor(t, db, "old-outage")
	still := seedMonitor(t, db, "still-down")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	ancient := now.Add(-100 * 24 * time.Hour)

	if _, err := db.OpenIncident(ctx, old, ancient, "timeout", "boom"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	if _, err := db.ResolveIncident(ctx, old, ancient.Add(time.Hour)); err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
	// An incident that started just as long ago but is still open describes
	// something that is wrong right now, so it must survive.
	if _, err := db.OpenIncident(ctx, still, ancient, "timeout", "boom"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	res, err := db.applyRetentionAt(ctx, now,
		RetentionPolicy{Raw: 7 * 24 * time.Hour, Rollup: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.Incidents != 1 {
		t.Errorf("pruned %d incidents, want 1", res.Incidents)
	}
	if got := countIncidents(t, db, old); got != 0 {
		t.Errorf("%d resolved incidents left, want 0", got)
	}
	if got := countIncidents(t, db, still); got != 1 {
		t.Errorf("open incident was deleted: %d left, want 1", got)
	}
}

func TestApplyRetentionStillRollsUpRawBeats(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "rollup-through-apply")

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	oldHour := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	beat(t, db, id, oldHour.Add(time.Minute), true, 100)
	beat(t, db, id, now.Add(-time.Hour), true, 50)

	res, err := db.applyRetentionAt(context.Background(), now,
		RetentionPolicy{Raw: 7 * 24 * time.Hour, Rollup: 365 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.Rollup.Heartbeats != 1 {
		t.Errorf("rolled up %d heartbeats, want 1", res.Rollup.Heartbeats)
	}
	if got := countRaw(t, db, id); got != 1 {
		t.Errorf("%d raw heartbeats left, want 1", got)
	}
}

// A database that has never had auto_vacuum turned on must be rebuilt, not
// silently left alone — that no-op was the whole reason the file never shrank.
func TestEnsureIncrementalVacuumRebuildsAnExistingDatabase(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var before int
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&before); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	if before != 0 {
		t.Fatalf("test database already has auto_vacuum = %d, want 0", before)
	}

	mode, err := db.EnsureIncrementalVacuum(ctx)
	if err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}
	if mode != VacuumRebuilt {
		t.Errorf("mode = %q, want %q", mode, VacuumRebuilt)
	}

	var after int
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&after); err != nil {
		t.Fatalf("re-read auto_vacuum: %v", err)
	}
	if after != 2 {
		t.Errorf("auto_vacuum = %d after the rebuild, want 2 (incremental)", after)
	}

	// A second call must recognise the mode and not rebuild again.
	mode, err = db.EnsureIncrementalVacuum(ctx)
	if err != nil {
		t.Fatalf("second EnsureIncrementalVacuum: %v", err)
	}
	if mode != VacuumIncremental {
		t.Errorf("second call mode = %q, want %q", mode, VacuumIncremental)
	}
}

// The point of the whole exercise: after a large delete the file must be able
// to shrink, which it only does once incremental vacuum has run.
func TestApplyRetentionReturnsPagesToTheFilesystem(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "reclaim")

	if _, err := db.EnsureIncrementalVacuum(ctx); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	oldHour := now.Add(-30 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 4000; i++ {
		beat(t, db, id, oldHour.Add(time.Duration(i)*time.Second), false, 0)
	}

	pagesBefore, err := db.sizeBytes(ctx)
	if err != nil {
		t.Fatalf("sizeBytes: %v", err)
	}

	res, err := db.applyRetentionAt(ctx, now,
		RetentionPolicy{Raw: 7 * 24 * time.Hour, Rollup: 365 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.ReclaimedPages == 0 {
		t.Error("no pages reclaimed after deleting 4000 heartbeats: the file would never shrink")
	}

	pagesAfter, err := db.sizeBytes(ctx)
	if err != nil {
		t.Fatalf("sizeBytes: %v", err)
	}
	if pagesAfter >= pagesBefore {
		t.Errorf("database is %d bytes after retention, was %d: it must get smaller", pagesAfter, pagesBefore)
	}
}
