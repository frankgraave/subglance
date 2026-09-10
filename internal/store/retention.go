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

// bucketSize is the rollup granularity. One hour keeps a year of history for
// one monitor in 8760 rows, which is small enough that uptime queries over
// long windows stay a single indexed scan.
const bucketSize = time.Hour

// RollupResult reports what a rollup pass did.
type RollupResult struct {
	// Buckets is the number of hourly rows inserted or updated.
	Buckets int64
	// Heartbeats is the number of raw rows folded in and then deleted.
	Heartbeats int64
	// Cutoff is the timestamp before which raw heartbeats were rolled up.
	Cutoff time.Time
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
			monitor_id, bucket, up_count, down_count,
			latency_min, latency_max, latency_avg, latency_count
		)
		SELECT
			monitor_id,
			ts - (ts % ?),
			sum(ok),
			sum(1 - ok),
			min(latency_ms),
			max(latency_ms),
			CAST(avg(latency_ms) AS INTEGER),
			count(latency_ms)
		FROM heartbeats
		WHERE ts < ?
		GROUP BY monitor_id, ts - (ts % ?)
		ON CONFLICT (monitor_id, bucket) DO UPDATE SET
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
