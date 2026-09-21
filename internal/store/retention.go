package store

import (
	"context"
	"fmt"
	"time"
)

// DefaultRawRetention is how long individual heartbeats are kept before they
// are rolled up into hourly buckets.
//
// Seven days is chosen to match the longest window the dashboard draws from
// raw beats. Anything shorter would make a recent view lose resolution while
// the user is still looking at it; anything longer only costs disk, since a
// monitor checked every 60s produces ~1440 rows a day.
const DefaultRawRetention = 7 * 24 * time.Hour

// DefaultRollupRetention is how long hourly buckets and resolved incidents are
// kept after the raw beats behind them are gone.
//
// A year is long enough that every "how did this look last quarter" question
// can still be answered, and it bounds the one part of the database that would
// otherwise grow forever: 200 monitors produce ~4800 hourly rows a day, which
// is tens of megabytes a year that nothing ever reclaimed before.
//
// Zero means keep forever, for anyone who wants the old unbounded behaviour.
const DefaultRollupRetention = 365 * 24 * time.Hour

// bucketSize is the rollup granularity. One hour keeps a year of history for
// one monitor in 8760 rows, which is small enough that uptime queries over
// long windows stay a single indexed scan.
const bucketSize = time.Hour

// RetentionPolicy is the full set of windows a maintenance pass honours.
type RetentionPolicy struct {
	// Raw is how long individual heartbeats survive before being rolled up.
	// Zero or negative falls back to DefaultRawRetention.
	Raw time.Duration
	// Rollup is how long hourly buckets and resolved incidents survive.
	// Zero or negative means keep them forever.
	Rollup time.Duration
}

// RollupResult reports what a rollup pass did.
type RollupResult struct {
	// Buckets is the number of hourly rows inserted or updated.
	Buckets int64
	// Heartbeats is the number of raw rows folded in and then deleted.
	Heartbeats int64
	// Cutoff is the timestamp before which raw heartbeats were rolled up.
	Cutoff time.Time
}

// RetentionResult reports what a full maintenance pass did.
type RetentionResult struct {
	// Rollup is the outcome of the raw-heartbeat rollup step.
	Rollup RollupResult
	// HourlyBuckets is the number of hourly rows deleted as too old.
	HourlyBuckets int64
	// Incidents is the number of resolved incidents deleted as too old.
	Incidents int64
	// ReclaimedPages is the number of database pages handed back to the
	// filesystem by the incremental vacuum step. Zero when the database is
	// not in incremental auto-vacuum mode.
	ReclaimedPages int64
	// RollupCutoff is the timestamp before which hourly buckets and resolved
	// incidents were deleted. Zero when rollup retention is off.
	RollupCutoff time.Time
}

// RollupHeartbeats folds raw heartbeats older than retention into hourly
// buckets and deletes the raw rows.
//
// The cutoff is truncated down to an hour boundary, so a bucket is only ever
// rolled up once it can no longer receive new beats. Without that, a partially
// rolled-up hour would keep receiving heartbeats that the next pass would have
// to merge into an already-summarised bucket — correct only as long as the
// merge arithmetic is, and needless risk for at most one hour of delay.
//
// Aggregation and deletion share one transaction. A crash between them would
// otherwise either lose beats (delete committed, insert not) or double-count
// them on the next pass (insert committed, delete not).
func (db *DB) RollupHeartbeats(ctx context.Context, retention time.Duration) (RollupResult, error) {
	return db.rollupAt(ctx, time.Now(), retention)
}

// rollupAt is RollupHeartbeats with an injectable clock, so tests can age data
// without sleeping.
func (db *DB) rollupAt(ctx context.Context, now time.Time, retention time.Duration) (RollupResult, error) {
	if retention <= 0 {
		retention = DefaultRawRetention
	}
	cutoff := now.Add(-retention).Truncate(bucketSize)
	res := RollupResult{Cutoff: cutoff}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin rollup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The upsert merges rather than replaces. A bucket may already exist from
	// an earlier pass (a monitor whose beats arrived late) and its counts must
	// survive; latency_avg is re-derived from both sample sets weighted by
	// their counts, which is the only combination that equals what a single
	// pass over all the beats would have produced.
	agg, err := tx.ExecContext(ctx, `
		INSERT INTO heartbeat_hourly (
			monitor_id, bucket, up_count, down_count, assessed_up, assessed_down, warning_count, maintenance_count,
			latency_min, latency_max, latency_avg, latency_count
		)
		SELECT
			monitor_id,
			ts - (ts % ?),
			sum(ok),
			sum(1 - ok),
			sum(maintenance = 0 AND assessment = 'up'), sum(maintenance = 0 AND assessment = 'down'), sum(maintenance = 0 AND assessment = 'warning'), sum(maintenance),
			min(latency_ms),
			max(latency_ms),
			CAST(avg(latency_ms) AS INTEGER),
			count(latency_ms)
		FROM heartbeats
		WHERE ts < ?
		GROUP BY monitor_id, ts - (ts % ?)
		ON CONFLICT (monitor_id, bucket) DO UPDATE SET
			maintenance_count = heartbeat_hourly.maintenance_count + excluded.maintenance_count,
			assessed_up = heartbeat_hourly.assessed_up + excluded.assessed_up,
			assessed_down = heartbeat_hourly.assessed_down + excluded.assessed_down,
			warning_count = heartbeat_hourly.warning_count + excluded.warning_count,
			up_count   = heartbeat_hourly.up_count   + excluded.up_count,
			down_count = heartbeat_hourly.down_count + excluded.down_count,
			latency_min = min(
				COALESCE(heartbeat_hourly.latency_min, excluded.latency_min),
				COALESCE(excluded.latency_min, heartbeat_hourly.latency_min)
			),
			latency_max = max(
				COALESCE(heartbeat_hourly.latency_max, excluded.latency_max),
				COALESCE(excluded.latency_max, heartbeat_hourly.latency_max)
			),
			latency_avg = CASE
				WHEN heartbeat_hourly.latency_count + excluded.latency_count = 0
					THEN NULL
				ELSE CAST(
					(COALESCE(heartbeat_hourly.latency_avg, 0) * heartbeat_hourly.latency_count
					 + COALESCE(excluded.latency_avg, 0) * excluded.latency_count)
					/ (heartbeat_hourly.latency_count + excluded.latency_count)
				AS INTEGER)
			END,
			latency_count = heartbeat_hourly.latency_count + excluded.latency_count`,
		int64(bucketSize.Seconds()), cutoff.Unix(), int64(bucketSize.Seconds()),
	)
	if err != nil {
		return res, fmt.Errorf("aggregate heartbeats: %w", err)
	}
	res.Buckets, _ = agg.RowsAffected()

	del, err := tx.ExecContext(ctx, `DELETE FROM heartbeats WHERE ts < ?`, cutoff.Unix())
	if err != nil {
		return res, fmt.Errorf("delete rolled-up heartbeats: %w", err)
	}
	res.Heartbeats, _ = del.RowsAffected()

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit rollup: %w", err)
	}
	return res, nil
}

// VacuumMode reports what EnsureIncrementalVacuum found or did.
type VacuumMode string

const (
	// VacuumIncremental means the database is in incremental auto-vacuum
	// mode, so ApplyRetention can hand freed pages back to the filesystem.
	VacuumIncremental VacuumMode = "incremental"
	// VacuumRebuilt means incremental auto-vacuum was switched on and the
	// database was rebuilt to make it take effect.
	VacuumRebuilt VacuumMode = "rebuilt"
	// VacuumFull means the database is in full auto-vacuum mode. That
	// already returns space on every commit, so nothing is changed.
	VacuumFull VacuumMode = "full"
	// VacuumNeedsRebuild means the database is too large to rebuild
	// unattended at startup and still has auto-vacuum off. The caller must
	// warn: without an operator-run VACUUM the file will never shrink.
	VacuumNeedsRebuild VacuumMode = "needs-rebuild"
)

// autoVacuumRebuildLimit is the largest database EnsureIncrementalVacuum will
// rebuild on its own.
//
// A VACUUM rewrites the whole file, which on this size of database is a
// sub-second pause but on a multi-gigabyte one can take minutes of blocked
// writes. 256 MiB is well past the ~200 MB steady state of a busy instance,
// so in practice everybody gets the automatic path and only a genuinely
// outsized database is handed back to its operator to schedule.
const autoVacuumRebuildLimit = 256 << 20

// EnsureIncrementalVacuum puts the database into incremental auto-vacuum mode
// so that deleted pages can be returned to the filesystem.
//
// This exists because retention deletes were previously invisible from the
// outside: SQLite moves the pages of a deleted row onto its freelist and
// reuses them, but never shortens the file. An instance in perfect steady
// state therefore showed a database that only ever grew, and an operator
// watching disk had no way to make it shrink.
//
// Switching auto_vacuum on is not free on an existing database. SQLite stores
// the mode in the file header and can only change it by rewriting the file, so
// `PRAGMA auto_vacuum` alone is a silent no-op unless a VACUUM follows. The
// deliberate choice here is to do that VACUUM, rather than to quietly limit
// the feature to databases created from now on: an existing installation is
// exactly the one with a bloated file, and leaving it out would fix the
// problem only for people who do not have it yet. The rebuild is bounded by
// autoVacuumRebuildLimit, and anything larger is reported back so the caller
// can tell its operator to run VACUUM when a pause suits them.
func (db *DB) EnsureIncrementalVacuum(ctx context.Context) (VacuumMode, error) {
	var mode int
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return "", fmt.Errorf("read auto_vacuum: %w", err)
	}
	switch mode {
	case 1:
		return VacuumFull, nil
	case 2:
		return VacuumIncremental, nil
	}

	size, err := db.sizeBytes(ctx)
	if err != nil {
		return "", err
	}
	if size > autoVacuumRebuildLimit {
		return VacuumNeedsRebuild, nil
	}

	// The pragma only records the intent; the VACUUM is what rewrites the
	// header and builds the pointer map that incremental vacuum needs.
	if _, err := db.Writer.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return "", fmt.Errorf("set auto_vacuum: %w", err)
	}
	if _, err := db.Writer.ExecContext(ctx, "VACUUM"); err != nil {
		return "", fmt.Errorf("vacuum: %w", err)
	}

	if err := db.Writer.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return "", fmt.Errorf("re-read auto_vacuum: %w", err)
	}
	if mode != 2 {
		return "", fmt.Errorf("store: auto_vacuum is %d after rebuild, want 2", mode)
	}
	return VacuumRebuilt, nil
}

// sizeBytes is the logical database size. It is read from the pragmas rather
// than from os.Stat so that it also works for the in-memory database used by
// tests, which has no file at all.
func (db *DB) sizeBytes(ctx context.Context) (int64, error) {
	var pages, pageSize int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, fmt.Errorf("read page_count: %w", err)
	}
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read page_size: %w", err)
	}
	return pages * pageSize, nil
}

// ApplyRetention runs a full maintenance pass: roll raw heartbeats up, drop
// hourly buckets and resolved incidents past the rollup window, then hand the
// freed pages back to the filesystem.
//
// The steps are separate statements on purpose. Each one is independently
// correct, so a failure part-way leaves the database smaller than it was and
// the next pass finishes the job; wrapping them together would only widen the
// window in which the single writer connection is held.
func (db *DB) ApplyRetention(ctx context.Context, p RetentionPolicy) (RetentionResult, error) {
	return db.applyRetentionAt(ctx, time.Now(), p)
}

// applyRetentionAt is ApplyRetention with an injectable clock.
func (db *DB) applyRetentionAt(ctx context.Context, now time.Time, p RetentionPolicy) (RetentionResult, error) {
	var res RetentionResult

	rollup, err := db.rollupAt(ctx, now, p.Raw)
	res.Rollup = rollup
	if err != nil {
		return res, err
	}

	if p.Rollup > 0 {
		cutoff := now.Add(-p.Rollup).Truncate(bucketSize)
		res.RollupCutoff = cutoff

		hourly, err := db.Writer.ExecContext(ctx,
			`DELETE FROM heartbeat_hourly WHERE bucket < ?`, cutoff.Unix())
		if err != nil {
			return res, fmt.Errorf("prune hourly buckets: %w", err)
		}
		res.HourlyBuckets, _ = hourly.RowsAffected()

		// Only resolved incidents are eligible. An incident that is still
		// open describes something that is wrong right now, however long ago
		// it started, and deleting it would take the outage off the dashboard
		// while it is still happening.
		inc, err := db.Writer.ExecContext(ctx,
			`DELETE FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at < ?`,
			cutoff.Unix())
		if err != nil {
			return res, fmt.Errorf("prune incidents: %w", err)
		}
		res.Incidents, _ = inc.RowsAffected()
	}

	pages, err := db.incrementalVacuum(ctx)
	if err != nil {
		return res, err
	}
	res.ReclaimedPages = pages
	return res, nil
}

// incrementalVacuum returns free pages to the filesystem and reports how many
// it gave back. It is a no-op on a database that is not in incremental
// auto-vacuum mode, which is why EnsureIncrementalVacuum runs at startup.
func (db *DB) incrementalVacuum(ctx context.Context) (int64, error) {
	var before int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&before); err != nil {
		return 0, fmt.Errorf("read freelist_count: %w", err)
	}
	if before == 0 {
		return 0, nil
	}
	if _, err := db.Writer.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return 0, fmt.Errorf("incremental vacuum: %w", err)
	}
	var after int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&after); err != nil {
		return 0, fmt.Errorf("re-read freelist_count: %w", err)
	}
	if after > before {
		return 0, nil
	}
	return before - after, nil
}
