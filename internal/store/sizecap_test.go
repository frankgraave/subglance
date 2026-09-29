package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// capNow is the fixed clock the size-limit tests age their data against.
var capNow = time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)

// capFloor is where the size limit stops folding raw heartbeats at capNow.
var capFloor = capNow.Add(-MinRawRetention).Truncate(time.Hour)

// fillRaw writes one raw heartbeat per step for id from from up to to, in
// one statement: going through RecordHeartbeat would make a database of a
// few megabytes take most of a minute. Each row carries an error text, so a
// row costs about what a failing check's row does.
func fillRaw(t *testing.T, db *DB, id int64, from, to time.Time, step time.Duration) int64 {
	t.Helper()
	res, err := db.Writer.ExecContext(context.Background(), `
		WITH RECURSIVE s(ts) AS (SELECT ? UNION ALL SELECT ts + ? FROM s WHERE ts + ? < ?)
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, error, assessment)
		SELECT ?, ts, 1, 42, ?, 'up' FROM s`,
		from.Unix(), int64(step/time.Second), int64(step/time.Second), to.Unix(),
		id, strings.Repeat("x", 80))
	if err != nil {
		t.Fatalf("fill raw heartbeats: %v", err)
	}
	n, _ := res.RowsAffected()
	return n
}

// fillHourly writes one hourly bucket per hour for id from from up to to.
func fillHourly(t *testing.T, db *DB, id int64, from, to time.Time) {
	t.Helper()
	if _, err := db.Writer.ExecContext(context.Background(), `
		WITH RECURSIVE s(b) AS (SELECT ? UNION ALL SELECT b + 3600 FROM s WHERE b + 3600 < ?)
		INSERT INTO heartbeat_hourly (monitor_id, bucket, up_count, down_count, latency_min, latency_max, latency_avg, latency_count)
		SELECT ?, b, 60, 0, 40, 90, 55, 60 FROM s`,
		from.Truncate(time.Hour).Unix(), to.Unix(), id); err != nil {
		t.Fatalf("fill hourly buckets: %v", err)
	}
}

func insertIncident(t *testing.T, db *DB, id int64, started time.Time, resolved *time.Time) {
	t.Helper()
	var res any
	if resolved != nil {
		res = resolved.Unix()
	}
	if _, err := db.Writer.ExecContext(context.Background(),
		`INSERT INTO incidents (monitor_id, started_at, confirmed_at, resolved_at) VALUES (?, ?, ?, ?)`,
		id, started.Unix(), started.Unix(), res); err != nil {
		t.Fatalf("insert incident: %v", err)
	}
}

func countWhere(t *testing.T, db *DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Reader.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func used(t *testing.T, db *DB) int64 {
	t.Helper()
	n, err := db.usedBytes(context.Background())
	if err != nil {
		t.Fatalf("usedBytes: %v", err)
	}
	return n
}

func TestSizeLimitFoldsTheOldestRawHeartbeatsFirst(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "busy")
	old := seedMonitor(t, db, "history")

	fillRaw(t, db, id, capNow.Add(-10*24*time.Hour), capNow, 30*time.Second)
	fillHourly(t, db, old, capNow.Add(-400*24*time.Hour), capNow.Add(-11*24*time.Hour))
	resolved := capNow.Add(-300 * 24 * time.Hour)
	insertIncident(t, db, old, resolved.Add(-time.Hour), &resolved)

	recent := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix())
	summaries := countHourly(t, db, old)
	limit := used(t, db) - 1<<20

	res, err := db.enforceSizeCapAt(ctx, capNow, limit)
	if err != nil {
		t.Fatalf("enforceSizeCapAt: %v", err)
	}
	if res.After > limit {
		t.Errorf("after = %d bytes, want at most the limit %d", res.After, limit)
	}
	if res.AtFloor {
		t.Error("AtFloor is set, but raw heartbeats alone could meet the limit")
	}
	if res.Heartbeats == 0 || !res.Intervened() {
		t.Fatalf("folded %d heartbeats, want some", res.Heartbeats)
	}

	// Summaries come after raw detail: none may go while raw heartbeats
	// older than the floor are still there to fold.
	if res.HourlyBuckets != 0 || countHourly(t, db, old) != summaries {
		t.Errorf("deleted %d hourly buckets, want 0: raw heartbeats go first", res.HourlyBuckets)
	}
	if got := countIncidents(t, db, old); got != 1 {
		t.Errorf("%d incidents left, want 1", got)
	}

	// Folded, not lost: every heartbeat that went is counted in a bucket.
	if got := countWhere(t, db, `SELECT COALESCE(sum(up_count + down_count), 0) FROM heartbeat_hourly WHERE monitor_id = ?`, id); got != res.Heartbeats {
		t.Errorf("hourly buckets hold %d checks, want the %d folded heartbeats", got, res.Heartbeats)
	}

	// Oldest first, and only as far as needed: the cut is a single point in
	// time, the last day is untouched, and raw detail older than the floor
	// is still there because the limit did not need it.
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts < ?`, res.RawSince.Unix()); got != 0 {
		t.Errorf("%d heartbeats older than the cut %s remain", got, res.RawSince)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix()); got != recent {
		t.Errorf("%d heartbeats from the last day remain, want all %d", got, recent)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts < ?`, capFloor.Unix()); got == 0 {
		t.Error("every heartbeat older than a day was folded, but a much smaller cut met the limit")
	}
	if !res.RawSince.Before(capFloor) || res.RawSince.Truncate(time.Hour) != res.RawSince {
		t.Errorf("cut at %s, want an hour boundary before the floor %s", res.RawSince, capFloor)
	}
}

func TestSizeLimitDeletesTheOldestSummariesOnlyAtTheRawFloor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "recent")
	ids := []int64{id}
	for _, name := range []string{"a", "b", "c"} {
		ids = append(ids, seedMonitor(t, db, name))
	}

	fillRaw(t, db, id, capNow.Add(-3*24*time.Hour), capNow, time.Minute)
	for _, m := range ids {
		fillHourly(t, db, m, capNow.Add(-3*365*24*time.Hour), capNow.Add(-3*24*time.Hour))
	}
	resolved := capNow.Add(-2 * 365 * 24 * time.Hour)
	insertIncident(t, db, ids[1], resolved.Add(-time.Hour), &resolved)
	insertIncident(t, db, ids[2], capNow.Add(-2*time.Hour), nil)

	recent := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix())
	limit := used(t, db) - 3<<19

	res, err := db.enforceSizeCapAt(ctx, capNow, limit)
	if err != nil {
		t.Fatalf("enforceSizeCapAt: %v", err)
	}
	if res.After > limit || res.AtFloor {
		t.Fatalf("after = %d, at floor = %v; want under %d without reaching the floor", res.After, res.AtFloor, limit)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts < ?`, capFloor.Unix()); got != 0 {
		t.Errorf("%d raw heartbeats older than a day remain, but summaries were deleted before them", got)
	}
	if res.RawSince != capFloor {
		t.Errorf("raw cut at %s, want the floor %s", res.RawSince, capFloor)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix()); got != recent {
		t.Errorf("%d heartbeats from the last day remain, want all %d", got, recent)
	}
	if res.HourlyBuckets == 0 {
		t.Fatal("no hourly buckets deleted, but raw heartbeats alone could not meet the limit")
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeat_hourly WHERE bucket < ?`, res.HourlySince.Unix()); got != 0 {
		t.Errorf("%d buckets older than the cut %s remain", got, res.HourlySince)
	}
	// The newest summaries stay: the cut is well short of the floor.
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeat_hourly WHERE bucket < ? AND bucket >= ?`,
		capFloor.Unix(), capNow.Add(-30*24*time.Hour).Unix()); got == 0 {
		t.Error("the last month of summaries is gone, but a smaller cut met the limit")
	}
	if got := countWhere(t, db, `SELECT count(*) FROM incidents`); got != 2 {
		t.Errorf("%d incidents left, want both: the size limit never deletes incidents", got)
	}
}

func TestSizeLimitStopsAtTheFloorAndSaysSo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "recent")

	fillRaw(t, db, id, capNow.Add(-3*24*time.Hour), capNow, time.Minute)
	fillHourly(t, db, id, capNow.Add(-365*24*time.Hour), capNow.Add(-3*24*time.Hour))
	resolved := capNow.Add(-200 * 24 * time.Hour)
	insertIncident(t, db, id, resolved.Add(-time.Hour), &resolved)

	recent := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix())
	const limit = 64 << 10

	res, err := db.enforceSizeCapAt(ctx, capNow, limit)
	if err != nil {
		t.Fatalf("enforceSizeCapAt: %v", err)
	}
	if !res.AtFloor {
		t.Fatalf("AtFloor not set, with %d bytes left over a %d-byte limit", res.After, limit)
	}
	if res.After <= limit {
		t.Fatalf("after = %d, under the limit: the test data is too small to prove the floor", res.After)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts >= ?`, capFloor.Unix()); got != recent {
		t.Errorf("%d heartbeats from the last day remain, want all %d: the floor is not negotiable", got, recent)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeat_hourly WHERE bucket < ?`, capFloor.Unix()); got != 0 {
		t.Errorf("%d summaries older than the floor remain, but the limit is still not met", got)
	}
	if got := countIncidents(t, db, id); got != 1 {
		t.Errorf("%d incidents left, want 1", got)
	}
}

func TestSizeLimitDoesNothingUnderTheLimit(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "small")
	n := fillRaw(t, db, id, capNow.Add(-5*24*time.Hour), capNow, 5*time.Minute)

	res, err := db.enforceSizeCapAt(context.Background(), capNow, 1<<30)
	if err != nil {
		t.Fatalf("enforceSizeCapAt: %v", err)
	}
	if res.Intervened() || res.AtFloor || res.Before != res.After {
		t.Errorf("result = %+v, want nothing done", res)
	}
	if got := countRaw(t, db, id); int64(got) != n {
		t.Errorf("%d heartbeats left, want all %d", got, n)
	}
}

// TestSizeLimitKeepsSummariesWhileRawDetailRemains runs the raw step out of
// rounds before it reaches its floor. One stray heartbeat two months back
// makes the even-spread estimate cut far too little in the first round, and
// with one round allowed, raw heartbeats older than the floor are still there
// when the pass ends. Summaries must not go while they are.
func TestSizeLimitKeepsSummariesWhileRawDetailRemains(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "busy")
	old := seedMonitor(t, db, "history")

	prev := sizeCapRounds
	sizeCapRounds = 1
	t.Cleanup(func() { sizeCapRounds = prev })

	fillRaw(t, db, id, capNow.Add(-60*24*time.Hour), capNow.Add(-60*24*time.Hour+time.Second), time.Minute)
	fillRaw(t, db, id, capNow.Add(-3*24*time.Hour), capNow, time.Minute)
	fillHourly(t, db, old, capNow.Add(-90*24*time.Hour), capNow.Add(-61*24*time.Hour))
	summaries := countHourly(t, db, old)

	olderRaw := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts < ?`, capFloor.Unix())
	limit := used(t, db) - olderRaw*rawRowBytes/2

	res, err := db.enforceSizeCapAt(ctx, capNow, limit)
	if err != nil {
		t.Fatalf("enforceSizeCapAt: %v", err)
	}
	if res.After <= limit {
		t.Fatalf("after = %d, under the limit %d: the test needs a raw step that runs out of rounds", res.After, limit)
	}
	if got := countWhere(t, db, `SELECT count(*) FROM heartbeats WHERE ts < ?`, capFloor.Unix()); got == 0 {
		t.Fatal("raw heartbeats reached the floor in one round: the test proves nothing")
	}
	if res.HourlyBuckets != 0 || countHourly(t, db, old) != summaries {
		t.Errorf("deleted %d hourly buckets while raw heartbeats older than the floor remain", res.HourlyBuckets)
	}
	if res.AtFloor {
		t.Error("AtFloor is set, but raw heartbeats older than the floor remain")
	}
}

// TestSizeLimitReportsOnlyACompletedCut makes the cut fail and checks the
// step does not report the cutoff it attempted as one it reached.
func TestSizeLimitReportsOnlyACompletedCut(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "busy")
	fillRaw(t, db, id, capNow.Add(-3*24*time.Hour), capNow, time.Minute)

	failed := errors.New("cut failed")
	step := capStep{
		table: "heartbeats", floor: capFloor,
		measure: `SELECT min(ts), count(*) FROM heartbeats WHERE ts < ?`,
		perRow:  rawRowBytes,
		cut: func(context.Context, time.Time) (int64, error) {
			return 0, failed
		},
	}
	res := SizeCapResult{Limit: 1, After: 1 << 30}
	removed, since, atFloor, err := db.capTable(context.Background(), &res, step)
	if !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the cut's error", err)
	}
	if removed != 0 || !since.IsZero() || atFloor {
		t.Errorf("removed %d, since %s, at floor %v; want nothing reported for a cut that failed", removed, since, atFloor)
	}
}

// TestSizeLimitSeesATableEmptiedShortOfItsFloor gives the last round a cut
// that takes every row below the floor while its cutoff stays short of it:
// the older rows sit in one half hour two months back, and the even-spread
// estimate places the cutoff about a month later. Nothing below the floor is
// left, so the step must say it is at its floor, or the summaries step after
// it never runs.
func TestSizeLimitSeesATableEmptiedShortOfItsFloor(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "sparse")

	prev := sizeCapRounds
	sizeCapRounds = 1
	t.Cleanup(func() { sizeCapRounds = prev })

	stray := capNow.Add(-60 * 24 * time.Hour)
	n := fillRaw(t, db, id, stray, stray.Add(30*time.Minute), time.Minute)
	fillRaw(t, db, id, capFloor, capNow, time.Minute)

	step := capStep{
		table: "heartbeats", floor: capFloor,
		measure: `SELECT min(ts), count(*) FROM heartbeats WHERE ts < ?`,
		perRow:  rawRowBytes,
		cut: func(ctx context.Context, cutoff time.Time) (int64, error) {
			r, err := db.rollupBefore(ctx, cutoff)
			return r.Heartbeats, err
		},
	}
	// Half the older rows' worth of excess makes the estimate cut halfway
	// to the floor, past every one of them. A one-byte limit stays out of
	// reach, so the round ends with it unmet.
	res := SizeCapResult{Limit: 1, After: 1 + n*rawRowBytes/2}
	removed, since, atFloor, err := db.capTable(context.Background(), &res, step)
	if err != nil {
		t.Fatalf("capTable: %v", err)
	}
	if removed != n {
		t.Fatalf("removed %d rows, want all %d older than the floor", removed, n)
	}
	if !since.Before(capFloor) {
		t.Fatalf("cutoff %s reached the floor %s: the test proves nothing", since, capFloor)
	}
	if !atFloor {
		t.Error("at floor is false, but no rows older than the floor are left")
	}
}

func TestApplyRetentionEnforcesTheSizeLimitAfterTheWindows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureIncrementalVacuum(ctx); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}
	id := seedMonitor(t, db, "busy")
	fillRaw(t, db, id, capNow.Add(-40*24*time.Hour), capNow, 2*time.Minute)

	// The raw window alone takes the ten days past thirty; the limit
	// is set so that is not enough.
	limit := used(t, db) / 2
	res, err := db.applyRetentionAt(ctx, capNow, RetentionPolicy{Raw: 30 * 24 * time.Hour, MaxBytes: limit})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.Rollup.Heartbeats == 0 {
		t.Error("the raw window folded nothing")
	}
	if res.SizeCap == nil || res.SizeCap.Heartbeats == 0 {
		t.Fatalf("size limit result = %+v, want heartbeats folded beyond the window", res.SizeCap)
	}
	// Counted apart, so the log and the card can say what the limit did.
	window := countWhere(t, db, `SELECT count(*) FROM heartbeat_hourly WHERE monitor_id = ? AND bucket < ?`,
		id, res.Rollup.Cutoff.Unix())
	if window == 0 || res.SizeCap.RawSince.Before(res.Rollup.Cutoff) {
		t.Errorf("limit cut at %s, before the window's %s", res.SizeCap.RawSince, res.Rollup.Cutoff)
	}

	// In incremental auto-vacuum mode the file itself ends under the limit,
	// which is what an operator with a small disk is measuring.
	size, err := db.sizeBytes(ctx)
	if err != nil {
		t.Fatalf("sizeBytes: %v", err)
	}
	if size > limit {
		t.Errorf("database file is %d bytes after the pass, want at most the limit %d", size, limit)
	}
	if res.ReclaimedBytes == 0 {
		t.Error("no space handed back to the filesystem")
	}

	// No limit, no size-limit result.
	res, err = db.applyRetentionAt(ctx, capNow, RetentionPolicy{Raw: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("applyRetentionAt without a limit: %v", err)
	}
	if res.SizeCap != nil {
		t.Errorf("SizeCap = %+v without a limit, want nil", res.SizeCap)
	}
}

func TestParseByteSize(t *testing.T) {
	good := map[string]int64{
		"0":        0,
		"2GB":      2_000_000_000,
		"2 GiB":    2 << 30,
		"500mb":    500_000_000,
		"1.5GiB":   3 << 29,
		" 64MiB ":  64 << 20,
		"1TB":      1_000_000_000_000,
		"4096B":    4096,
		"750 KiB":  750 << 10,
		"0.5 TiB":  1 << 39,
		"100000kb": 100_000_000,
	}
	for in, want := range good {
		got, err := ParseByteSize(in)
		if err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "2", "1024", "GB", "2 GB extra", "-1GB", "2XB", "1e3MB", "..GB", "99999999999TB"} {
		if _, err := ParseByteSize(in); !errors.Is(err, ErrMaxDatabaseSize) {
			t.Errorf("ParseByteSize(%q) error = %v, want ErrMaxDatabaseSize", in, err)
		}
	}
}

func TestFormatByteSizeReadsBack(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 32 << 20: "32MiB", 2 << 30: "2GiB", 3 << 29: "1.5GiB", 512: "512B"} {
		if got := FormatByteSize(n); got != want {
			t.Errorf("FormatByteSize(%d) = %q, want %q", n, got, want)
		}
	}
	for _, n := range []int64{32 << 20, 2 << 30, 5 << 40} {
		if back, err := ParseByteSize(FormatByteSize(n)); err != nil || back != n {
			t.Errorf("ParseByteSize(FormatByteSize(%d)) = %d, %v", n, back, err)
		}
	}
}

func TestMaxDatabaseSizeResolvesPinThenPageThenNone(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	got, err := db.ResolveMaxDatabaseSize(ctx, nil)
	if err != nil || got.Value != 0 || got.Source != RetentionSourceDefault {
		t.Fatalf("default = %+v, %v; want no limit from the default", got, err)
	}

	if err := db.SetMaxDatabaseSize(ctx, 2<<30); err != nil {
		t.Fatalf("SetMaxDatabaseSize: %v", err)
	}
	got, err = db.ResolveMaxDatabaseSize(ctx, nil)
	if err != nil || got.Value != 2<<30 || got.Source != RetentionSourceDatabase {
		t.Errorf("stored = %+v, %v; want 2GiB from the database", got, err)
	}

	pin := &MaxDatabaseSizePin{Value: 1 << 30, By: "--max-database-size"}
	got, err = db.ResolveMaxDatabaseSize(ctx, pin)
	if err != nil || got.Value != 1<<30 || got.Source != RetentionSourcePinned || got.PinnedBy != "--max-database-size" {
		t.Errorf("pinned = %+v, %v; want 1GiB pinned by the flag", got, err)
	}

	// Zero on the page removes the limit; it is not the default coming back.
	if err := db.SetMaxDatabaseSize(ctx, 0); err != nil {
		t.Fatalf("SetMaxDatabaseSize(0): %v", err)
	}
	got, err = db.ResolveMaxDatabaseSize(ctx, nil)
	if err != nil || got.Value != 0 || got.Source != RetentionSourceDatabase {
		t.Errorf("cleared = %+v, %v; want no limit, saved on the page", got, err)
	}
}

func TestMaxDatabaseSizeRefusesALimitBelowTheMinimum(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, n := range []int64{-1, 1, MinMaxDatabaseSize - 1} {
		if err := db.SetMaxDatabaseSize(ctx, n); !errors.Is(err, ErrMaxDatabaseSize) {
			t.Errorf("SetMaxDatabaseSize(%d) = %v, want ErrMaxDatabaseSize", n, err)
		}
	}
	if err := db.SetMaxDatabaseSize(ctx, MinMaxDatabaseSize); err != nil {
		t.Errorf("SetMaxDatabaseSize(minimum) = %v", err)
	}

	// A row nobody here wrote is refused, not applied.
	if err := db.putSetting(ctx, settingMaxDatabaseSize, "1024"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResolveMaxDatabaseSize(ctx, nil); !errors.Is(err, ErrMaxDatabaseSize) {
		t.Errorf("resolve with a 1 KiB row = %v, want ErrMaxDatabaseSize", err)
	}
}
