package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// LatencyPoint is one step of a monitor's latency history.
//
// A point exists only for a step that holds at least one check. Steps without
// any are left out instead of being reported as zero, because "nothing was
// measured here" and "it answered in 0ms" are different facts; the caller
// draws the first as a gap.
type LatencyPoint struct {
	// Start is the beginning of the step, aligned to a multiple of the step
	// size since the Unix epoch.
	Start time.Time
	// Samples is how many checks in the step carried a latency. Failed checks
	// usually carry none, so this can be lower than Checks.
	Samples int
	// Checks is every check recorded in the step, with or without latency.
	Checks int
	// Down is how many of those checks were assessed as confirmed down,
	// outside maintenance.
	Down int
	// AvgMS, MinMS and MaxMS are only meaningful when Samples > 0.
	AvgMS int
	MinMS int
	MaxMS int
}

type latencyAcc struct {
	sum      float64
	samples  int64
	checks   int64
	down     int64
	min, max int64
	hasRange bool
}

func (a *latencyAcc) addRange(minMS, maxMS int64) {
	if !a.hasRange {
		a.min, a.max, a.hasRange = minMS, maxMS, true
		return
	}
	a.min = min(a.min, minMS)
	a.max = max(a.max, maxMS)
}

// LatencySeries returns a monitor's latency history between since and until,
// grouped into steps of the given size.
//
// Like Uptime, it has to span the rollup boundary: raw heartbeats reach back
// only as far as raw retention, and older history lives in heartbeat_hourly.
// The two sources are disjoint, so a step that straddles the boundary simply
// merges both, weighting each average by its own sample count — averaging the
// averages would over-weight whichever side had fewer checks.
//
// An hourly bucket is placed in the step that contains its start. With a step
// shorter than an hour that puts a whole hour into one step and leaves the
// rest of that hour empty, which is the honest shape of data that no longer
// has finer resolution.
func (db *DB) LatencySeries(ctx context.Context, monitorID int64, since, until time.Time, step time.Duration) ([]LatencyPoint, error) {
	stepS := int64(step / time.Second)
	if stepS <= 0 {
		return nil, fmt.Errorf("latency series step must be at least one second, got %s", step)
	}
	from, to := since.Unix(), until.Unix()
	accs := make(map[int64]*latencyAcc)
	acc := func(slot int64) *latencyAcc {
		a, ok := accs[slot]
		if !ok {
			a = &latencyAcc{}
			accs[slot] = a
		}
		return a
	}

	// Both sources are read inside one transaction so they come from one
	// snapshot. A rollup that commits between two separate reads would move
	// checks from heartbeats into heartbeat_hourly after the first read and
	// before the second, and the series would count them twice.
	tx, err := db.Reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT ts - (ts % ?) AS slot,
		       count(*),
		       sum(maintenance = 0 AND assessment = 'down'),
		       count(latency_ms), sum(latency_ms), min(latency_ms), max(latency_ms)
		FROM heartbeats
		WHERE monitor_id = ? AND ts >= ? AND ts < ?
		GROUP BY slot`, stepS, monitorID, from, to)
	if err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}
	for rows.Next() {
		var (
			slot, checks, down, samples int64
			sum                         *float64
			lo, hi                      *int64
		)
		if err := rows.Scan(&slot, &checks, &down, &samples, &sum, &lo, &hi); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan latency series for monitor %d: %w", monitorID, err)
		}
		a := acc(slot)
		a.checks += checks
		a.down += down
		if samples > 0 && sum != nil && lo != nil && hi != nil {
			a.samples += samples
			a.sum += *sum
			a.addRange(*lo, *hi)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT bucket - (bucket % ?) AS slot,
		       sum(up_count + down_count),
		       sum(assessed_down),
		       sum(latency_count), sum(COALESCE(latency_avg, 0) * latency_count),
		       min(latency_min), max(latency_max)
		FROM heartbeat_hourly
		WHERE monitor_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY slot`, stepS, monitorID, from, to)
	if err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			slot, checks, down, samples int64
			sum                         *float64
			lo, hi                      *int64
		)
		if err := rows.Scan(&slot, &checks, &down, &samples, &sum, &lo, &hi); err != nil {
			return nil, fmt.Errorf("scan latency series for monitor %d: %w", monitorID, err)
		}
		a := acc(slot)
		a.checks += checks
		a.down += down
		if samples > 0 && sum != nil && lo != nil && hi != nil {
			a.samples += samples
			a.sum += *sum
			a.addRange(*lo, *hi)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latency series for monitor %d: %w", monitorID, err)
	}

	points := make([]LatencyPoint, 0, len(accs))
	for slot, a := range accs {
		p := LatencyPoint{
			Start:   time.Unix(slot, 0).UTC(),
			Samples: int(a.samples),
			Checks:  int(a.checks),
			Down:    int(a.down),
		}
		if a.samples > 0 {
			p.AvgMS = int(a.sum/float64(a.samples) + 0.5)
			p.MinMS = int(a.min)
			p.MaxMS = int(a.max)
		}
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Start.Before(points[j].Start) })
	return points, nil
}
