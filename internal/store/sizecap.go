package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// SizeCapResult is what the database size limit did on one pass.
//
// It is kept apart from what the retention windows did, because the two mean
// different things to an operator: the windows removed what they were asked
// to, the limit removed more than that, and the second is the one that has to
// be visible.
type SizeCapResult struct {
	// Limit is the size, in bytes, the pass worked towards.
	Limit int64 `json:"limit_bytes"`
	// Before and After are the size of the data when the limit was checked
	// and when it was done: pages in use, not the file, which only shrinks
	// once the freed pages are handed back.
	Before int64 `json:"before_bytes"`
	After  int64 `json:"after_bytes"`
	// Heartbeats is the number of raw heartbeats folded into hourly buckets
	// beyond the raw window. They are summarised, not lost.
	Heartbeats int64 `json:"heartbeats"`
	// HourlyBuckets is the number of hourly rows deleted beyond the rollup
	// window.
	HourlyBuckets int64 `json:"hourly_buckets"`
	// RawSince is the start of the raw heartbeats that remain when the limit
	// moved it, and zero when it did not.
	RawSince time.Time `json:"raw_since,omitzero"`
	// HourlySince is the same for hourly buckets.
	HourlySince time.Time `json:"hourly_since,omitzero"`
	// AtFloor is set when the data is still over the limit with nothing
	// left that the limit may remove: raw heartbeats down to the last
	// MinRawRetention, no older summaries, and incidents, which it never
	// touches.
	AtFloor bool `json:"at_floor,omitempty"`
}

// Intervened reports whether the limit removed anything the windows did not.
func (r SizeCapResult) Intervened() bool { return r.Heartbeats > 0 || r.HourlyBuckets > 0 }

// sizeCapRounds bounds how many times one pass re-measures and cuts again. A
// cut is sized from an estimate, and a round that falls short learns the real
// cost per row from what it freed, so two or three rounds are the norm; the
// bound is for a database whose rows free no whole pages at all.
//
// A variable only so a test can make the bound bite.
var sizeCapRounds = 12

// Fallback sizes, in bytes on disk per row including indexes, for when the
// dbstat table is unavailable. Measured on the demo database; see
// DefaultRawRetention and DefaultRollupRetention.
const (
	rawRowBytes    = 75
	hourlyRowBytes = 24
)

// enforceSizeCapAt brings the data under limit bytes by removing history in
// the order that costs least: first it folds the oldest raw heartbeats into
// hourly buckets, which gives up per-check detail but no history, and only
// when raw heartbeats are down to the last MinRawRetention does it delete the
// oldest hourly buckets. Incidents are never removed by it: they are small,
// and they are what an operator comes back to look for.
//
// A limit that cannot be met without going below those floors stops at them
// and says so in AtFloor, rather than removing anything else.
//
// The size measured is the pages in use, not the file. Deleted rows free
// pages at once, but the file only shrinks when they are handed back, which
// the incremental vacuum after this step does; on a database that is not in
// incremental auto-vacuum mode the freed pages are reused instead, so the
// file stops growing at the limit rather than shrinking to it.
func (db *DB) enforceSizeCapAt(ctx context.Context, now time.Time, limit int64) (SizeCapResult, error) {
	res := SizeCapResult{Limit: limit}
	used, err := db.usedBytes(ctx)
	if err != nil {
		return res, err
	}
	res.Before, res.After = used, used
	if used <= limit {
		return res, nil
	}

	floor := now.Add(-MinRawRetention).Truncate(bucketSize)
	sizes, sizeErr := db.tableBytes(ctx)

	raw := capStep{
		table: "heartbeats", floor: floor,
		measure: `SELECT min(ts), count(*) FROM heartbeats WHERE ts < ?`,
		perRow:  db.rowCost(ctx, sizes, sizeErr, rawRowBytes, "heartbeats", "heartbeat_responses"),
		cut: func(ctx context.Context, cutoff time.Time) (int64, error) {
			r, err := db.rollupBefore(ctx, cutoff)
			return r.Heartbeats, err
		},
	}
	var rawAtFloor bool
	if res.Heartbeats, res.RawSince, rawAtFloor, err = db.capTable(ctx, &res, raw); err != nil {
		return res, err
	}
	// Summaries only go once raw heartbeats are at their floor. A raw step
	// that ran out of rounds first still has raw detail older than the
	// floor, and deleting summaries now would leave hours whose raw beats
	// remain while the hours before them are gone. The next pass carries on
	// from where this one stopped.
	if res.After <= limit || !rawAtFloor {
		return res, nil
	}

	// Raw heartbeats are at their floor, so every hourly bucket older than
	// it summarises hours that have no raw beats left. Those are what goes
	// next, oldest first; the buckets from the floor onwards cover hours
	// the raw heartbeats still hold.
	hourly := capStep{
		table: "heartbeat_hourly", floor: floor,
		measure: `SELECT min(bucket), count(*) FROM heartbeat_hourly WHERE bucket < ?`,
		perRow:  db.rowCost(ctx, sizes, sizeErr, hourlyRowBytes, "heartbeat_hourly"),
		cut: func(ctx context.Context, cutoff time.Time) (int64, error) {
			del, err := db.Writer.ExecContext(ctx,
				`DELETE FROM heartbeat_hourly WHERE bucket < ?`, cutoff.Unix())
			if err != nil {
				return 0, fmt.Errorf("prune hourly buckets over the size limit: %w", err)
			}
			n, _ := del.RowsAffected()
			return n, nil
		},
	}
	var hourlyAtFloor bool
	if res.HourlyBuckets, res.HourlySince, hourlyAtFloor, err = db.capTable(ctx, &res, hourly); err != nil {
		return res, err
	}
	res.AtFloor = res.After > limit && hourlyAtFloor
	return res, nil
}

// capStep is one table the size limit may cut into, oldest rows first.
type capStep struct {
	table string
	// measure returns the oldest timestamp and the row count below its one
	// argument, the floor.
	measure string
	// floor is the timestamp the cut never passes.
	floor time.Time
	// perRow is the first estimate of what one row costs on disk.
	perRow float64
	// cut removes every row older than cutoff and reports how many.
	cut func(ctx context.Context, cutoff time.Time) (int64, error)
}

// capTable cuts into one table until the data is under res.Limit or the
// table has nothing older than its floor. It returns the rows removed, the
// last cutoff that completed, and whether the table is down to its floor, and
// keeps res.After current. It stops short of the floor, and says so, when the
// rounds run out first.
//
// The cut is placed by assuming rows are spread evenly in time, which a
// monitor writing on a fixed interval nearly does. That needs a count and a
// minimum, not a sort, so it costs no temporary space on the disk that is
// running out. Where the assumption is off, the next round measures what the
// last one actually freed per row and corrects.
func (db *DB) capTable(ctx context.Context, res *SizeCapResult, s capStep) (int64, time.Time, bool, error) {
	var removed int64
	var since time.Time
	atFloor := false
	perRow := max(s.perRow, 1)
	measure := func() (sql.NullInt64, int64, error) {
		var oldest sql.NullInt64
		var rows int64
		if err := db.Writer.QueryRowContext(ctx, s.measure, s.floor.Unix()).Scan(&oldest, &rows); err != nil {
			return oldest, 0, fmt.Errorf("measure %s for the size limit: %w", s.table, err)
		}
		return oldest, rows, nil
	}
	for range sizeCapRounds {
		excess := res.After - res.Limit
		if excess <= 0 {
			break
		}
		oldest, rows, err := measure()
		if err != nil {
			return removed, since, false, err
		}
		if rows == 0 || !oldest.Valid {
			atFloor = true
			break
		}

		want := math.Ceil(float64(excess) / perRow)
		span := s.floor.Sub(time.Unix(oldest.Int64, 0))
		cutoff := s.floor
		if want < float64(rows) {
			step := time.Duration(float64(span) * want / float64(rows))
			// Up to the next hour: a cut is always at least an hour, so
			// every round makes progress, and it stays on the boundary a
			// rollup requires.
			cutoff = time.Unix(oldest.Int64, 0).Add(step).Truncate(bucketSize).Add(bucketSize)
			if cutoff.After(s.floor) {
				cutoff = s.floor
			}
		}

		n, err := s.cut(ctx, cutoff)
		removed += n
		if err != nil {
			// since stays at the last cut that completed: a failed one
			// may have removed nothing.
			return removed, since, false, err
		}
		since = cutoff
		atFloor = cutoff.Equal(s.floor)
		used, err := db.usedBytes(ctx)
		if err != nil {
			return removed, since, atFloor, err
		}
		freed := res.After - used
		res.After = used
		switch {
		case n > 0 && freed > 0:
			perRow = float64(freed) / float64(n)
		default:
			// Rows went but no whole page did. Aim for twice as many
			// next time rather than repeat a cut that frees nothing.
			perRow /= 2
		}
		perRow = max(perRow, 1)
	}
	// On sparse data the last round's cut can take every row below the
	// floor with a cutoff short of it. Rounds that ran out with the limit
	// still unmet measure once more, so a table with nothing left below its
	// floor is reported as at it and the next step may run.
	if !atFloor && res.After > res.Limit {
		oldest, rows, err := measure()
		if err != nil {
			return removed, since, false, err
		}
		atFloor = rows == 0 || !oldest.Valid
	}
	return removed, since, atFloor, nil
}

// countRows are the row counts rowCost divides by, one fixed query per table
// the size limit cuts into.
var countRows = map[string]string{
	"heartbeats":       `SELECT count(*) FROM heartbeats`,
	"heartbeat_hourly": `SELECT count(*) FROM heartbeat_hourly`,
}

// rowCost estimates the bytes one row of table costs on disk, indexes
// included, adding the tables in extra whose rows go with it (a raw
// heartbeat's stored response goes when the heartbeat does). It falls back to
// def when the page statistics are unavailable or the table is empty.
func (db *DB) rowCost(ctx context.Context, sizes map[string]int64, sizeErr error, def float64, table string, extra ...string) float64 {
	if sizeErr != nil {
		return def
	}
	query, ok := countRows[table]
	if !ok {
		return def
	}
	var rows int64
	if err := db.Writer.QueryRowContext(ctx, query).Scan(&rows); err != nil || rows == 0 {
		return def
	}
	bytes := sizes[table]
	for _, t := range extra {
		bytes += sizes[t]
	}
	if bytes <= 0 {
		return def
	}
	return float64(bytes) / float64(rows)
}

// usedBytes is the space the data occupies: the database's pages minus the
// free ones. It is what the file shrinks to once the free pages are handed
// back, and it moves the moment rows are deleted, which the file does not.
func (db *DB) usedBytes(ctx context.Context) (int64, error) {
	var pages, free, pageSize int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, fmt.Errorf("read page_count: %w", err)
	}
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return 0, fmt.Errorf("read freelist_count: %w", err)
	}
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read page_size: %w", err)
	}
	return (pages - free) * pageSize, nil
}

// MinMaxDatabaseSize is the smallest size limit accepted.
//
// It is a guard against a slip of the unit, not a policy: a limit of "2" or
// "2MB" where "2GB" was meant would fold every raw heartbeat older than a day
// and then delete every hourly summary, on the first pass, with nothing to
// undo it. An empty database is well under a megabyte, so 32 MiB still lets
// someone on a very small disk keep a few days of history.
const MinMaxDatabaseSize int64 = 32 << 20

// settingMaxDatabaseSize holds the size limit chosen on the settings page, in
// bytes. "0" means no limit; absent means the default, which is also none.
const settingMaxDatabaseSize = "retention.max_database_bytes"

// ErrMaxDatabaseSize wraps every reason a size limit is refused.
var ErrMaxDatabaseSize = errors.New("invalid database size limit")

// byteUnits are the suffixes ParseByteSize reads, decimal and binary, keyed
// in lower case.
var byteUnits = map[string]int64{
	"b":  1,
	"kb": 1000, "mb": 1000 * 1000, "gb": 1000 * 1000 * 1000, "tb": 1000 * 1000 * 1000 * 1000,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
}

// ParseByteSize reads a size such as "2GB", "500 MiB" or "1.5GB" as bytes,
// or "0" for no limit. Decimal units are powers of 1000 and binary ones powers
// of 1024, as their names say; the unit is not case-sensitive.
//
// Any other size must name its unit. A bare number is refused because it is
// most likely a unit left off, and read as bytes it would be a limit no
// database can meet.
func ParseByteSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "0" {
		return 0, nil
	}
	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i <= 0 {
		return 0, fmt.Errorf("%w: %q: want a number and a unit, such as 2GB or 500MiB, or 0 for none", ErrMaxDatabaseSize, s)
	}
	num, unit := t[:i], strings.ToLower(strings.TrimSpace(t[i:]))
	mult, ok := byteUnits[unit]
	if !ok {
		return 0, fmt.Errorf("%w: %q: unknown unit %q, want B, KB, MB, GB, TB, KiB, MiB, GiB or TiB", ErrMaxDatabaseSize, s, t[i:])
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil || n < 0 || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%w: %q: %q is not a size", ErrMaxDatabaseSize, s, num)
	}
	bytes := n * float64(mult)
	if bytes >= math.MaxInt64 {
		return 0, fmt.Errorf("%w: %q is too large", ErrMaxDatabaseSize, s)
	}
	return int64(bytes), nil
}

// FormatByteSize renders a size the way ParseByteSize reads it back, in the
// largest binary unit that keeps it at one or more, to one decimal.
func FormatByteSize(n int64) string {
	if n == 0 {
		return "0"
	}
	units := []string{"TiB", "GiB", "MiB", "KiB"}
	for i, u := range units {
		size := int64(1) << (10 * (len(units) - i))
		if n >= size {
			v := float64(n) / float64(size)
			if v == math.Trunc(v) {
				return strconv.FormatFloat(v, 'f', 0, 64) + u
			}
			return strconv.FormatFloat(v, 'f', 1, 64) + u
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

// ValidateMaxDatabaseSize reports why a size limit cannot be used: zero is
// no limit, anything else must be at least MinMaxDatabaseSize.
func ValidateMaxDatabaseSize(n int64) error {
	if n < 0 {
		return fmt.Errorf("%w: must not be negative", ErrMaxDatabaseSize)
	}
	if n > 0 && n < MinMaxDatabaseSize {
		return fmt.Errorf("%w: %s is below the minimum of %s, or 0 for no limit",
			ErrMaxDatabaseSize, FormatByteSize(n), FormatByteSize(MinMaxDatabaseSize))
	}
	return nil
}

// MaxDatabaseSizePin is a size limit fixed from a flag or environment
// variable.
type MaxDatabaseSizePin struct {
	// Value is the limit in bytes; zero means none.
	Value int64
	// By names the flag or variable, as the operator would type it.
	By string
}

// MaxDatabaseSize is the resolved size limit and where it came from.
type MaxDatabaseSize struct {
	// Value is the limit in bytes; zero means none.
	Value int64
	// Source is RetentionSourceDefault, RetentionSourceDatabase or
	// RetentionSourcePinned, as for the windows.
	Source string
	// PinnedBy names the flag or variable when Source is pinned.
	PinnedBy string
}

// ResolveMaxDatabaseSize works out the size limit a pass applies: a pinned
// one wins, then the one saved on the settings page, then none.
func (db *DB) ResolveMaxDatabaseSize(ctx context.Context, pin *MaxDatabaseSizePin) (MaxDatabaseSize, error) {
	if pin != nil {
		return MaxDatabaseSize{Value: pin.Value, Source: RetentionSourcePinned, PinnedBy: pin.By}, nil
	}
	v, err := readCount(ctx, db.Reader, settingMaxDatabaseSize)
	if err != nil {
		return MaxDatabaseSize{}, err
	}
	if v == nil {
		return MaxDatabaseSize{Source: RetentionSourceDefault}, nil
	}
	if err := ValidateMaxDatabaseSize(*v); err != nil {
		// A row this code did not write. Refused rather than used: a limit
		// below the minimum is the one value that deletes history.
		return MaxDatabaseSize{}, fmt.Errorf("setting %s: %w", settingMaxDatabaseSize, err)
	}
	return MaxDatabaseSize{Value: *v, Source: RetentionSourceDatabase}, nil
}

// SetMaxDatabaseSize stores the size limit chosen on the settings page, in
// bytes; zero removes it.
func (db *DB) SetMaxDatabaseSize(ctx context.Context, n int64) error {
	if err := ValidateMaxDatabaseSize(n); err != nil {
		return err
	}
	return db.putSetting(ctx, settingMaxDatabaseSize, strconv.FormatInt(n, 10))
}
