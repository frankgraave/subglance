package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// compactNow is the clock the compaction tests age their data against.
var compactNow = time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)

// bloat writes n raw heartbeats a month old, each carrying a 100-byte error,
// then rolls them up: the raw rows are removed and their pages are left on
// the freelist, which is the state a compaction exists to fix. It returns how
// many hourly buckets the rollup produced.
func bloat(t *testing.T, db *DB, n int) int {
	t.Helper()
	ctx := context.Background()
	id := seedMonitor(t, db, "bloat")
	start := compactNow.Add(-30 * 24 * time.Hour).Truncate(time.Hour).Unix()
	if _, err := db.Writer.ExecContext(ctx, `
		WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM seq WHERE i < ? - 1)
		INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error)
		SELECT ?, ? + i, 0, 120, 503, hex(randomblob(50)) FROM seq`, n, id, start); err != nil {
		t.Fatalf("insert heartbeats: %v", err)
	}
	if _, err := db.rollupAt(ctx, compactNow, 7*24*time.Hour); err != nil {
		t.Fatalf("rollupAt: %v", err)
	}
	return countHourly(t, db, id)
}

// withHooks swaps the disk probes for the duration of one test.
func withHooks(t *testing.T, free func(string) (int64, error), same func(a, b string) bool, temp func() string) {
	t.Helper()
	oldFree, oldSame, oldTemp := diskFree, sameFilesystem, compactTempDir
	t.Cleanup(func() { diskFree, sameFilesystem, compactTempDir = oldFree, oldSame, oldTemp })
	if free != nil {
		diskFree = free
	}
	if same != nil {
		sameFilesystem = same
	}
	if temp != nil {
		compactTempDir = temp
	}
}

func autoVacuumMode(t *testing.T, db *DB) int {
	t.Helper()
	var mode int
	if err := db.Writer.QueryRowContext(context.Background(), "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	return mode
}

// The case the action exists for: a database startup refused to rebuild
// because it was over the limit. The limit is lowered through the test hook
// rather than by writing 256 MiB.
func TestCompactShrinksADatabaseTooLargeForTheStartupRebuild(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	old := rebuildLimit
	rebuildLimit = 256 << 10
	t.Cleanup(func() { rebuildLimit = old })

	buckets := bloat(t, db, 20000)

	if mode, err := db.EnsureIncrementalVacuum(ctx); err != nil || mode != VacuumNeedsRebuild {
		t.Fatalf("EnsureIncrementalVacuum = %q, %v; want %q (the database must be over the startup limit)",
			mode, err, VacuumNeedsRebuild)
	}

	plan, err := db.PlanCompact(ctx)
	if err != nil {
		t.Fatalf("PlanCompact: %v", err)
	}
	if !plan.Recommended || plan.AutoVacuum != "none" || plan.FreeBytes == 0 || plan.Disk != nil {
		t.Fatalf("plan = %+v; want recommended, mode none, free pages, room on disk", plan)
	}

	res, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.BeforeBytes != plan.SizeBytes {
		t.Errorf("BeforeBytes = %d, want the planned %d", res.BeforeBytes, plan.SizeBytes)
	}
	// Incremental mode adds pointer-map pages, so the result lands a page or
	// two above "size minus free"; nine tenths of the empty space returned
	// still tells a rewrite from a no-op.
	if freed := res.BeforeBytes - res.AfterBytes; freed < plan.FreeBytes*9/10 {
		t.Errorf("compacting freed %d of %d empty bytes (%d -> %d)",
			freed, plan.FreeBytes, res.BeforeBytes, res.AfterBytes)
	}
	if res.AutoVacuum != "incremental" || autoVacuumMode(t, db) != 2 {
		t.Errorf("auto_vacuum after compacting = %q, want incremental", res.AutoVacuum)
	}
	if res.ShrinkPending {
		t.Error("ShrinkPending = true with no reader open")
	}

	// The file on disk, not only the page count, must have shrunk: that is
	// what the checkpoint after the VACUUM is for.
	fi, err := os.Stat(db.Path())
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if fi.Size() != res.AfterBytes {
		t.Errorf("database file is %d bytes, want %d: the rewrite is still in the write-ahead log",
			fi.Size(), res.AfterBytes)
	}
	if wal, err := os.Stat(db.Path() + "-wal"); err == nil && wal.Size() != 0 {
		t.Errorf("write-ahead log is %d bytes after compacting, want 0", wal.Size())
	}

	if got := countHourly(t, db, 1); got != buckets {
		t.Errorf("%d hourly buckets after compacting, want the %d that were there", got, buckets)
	}
	if mode, err := db.EnsureIncrementalVacuum(ctx); err != nil || mode != VacuumIncremental {
		t.Errorf("next startup: EnsureIncrementalVacuum = %q, %v; want %q", mode, err, VacuumIncremental)
	}
	if after, err := db.PlanCompact(ctx); err != nil || after.Recommended {
		t.Errorf("plan after compacting = %+v, %v; want nothing left to recommend", after, err)
	}
}

// A reader holding a snapshot from before the rewrite stops the checkpoint
// from emptying the write-ahead log. SQLite reports that in the result row,
// not as an error, so Compact has to read the row: the rewrite is done, but
// the file has not shrunk yet, and the result must say so instead of claiming
// the space is back.
func TestCompactReportsAShrinkAReaderHeldBack(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	bloat(t, db, 20000)

	// The checkpoint waits busy_timeout for the reader to let go; a short one
	// lets the test see it give up without waiting five seconds.
	if _, err := db.Writer.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	tx, err := db.Reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM heartbeat_hourly").Scan(&n); err != nil {
		t.Fatalf("read inside the snapshot: %v", err)
	}

	res, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact with a reader open: %v", err)
	}
	if !res.ShrinkPending {
		t.Error("ShrinkPending = false while a reader held the old snapshot")
	}
	fi, err := os.Stat(db.Path())
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if fi.Size() <= res.AfterBytes {
		t.Fatalf("database file is already %d bytes (AfterBytes %d): the reader did not hold the checkpoint back",
			fi.Size(), res.AfterBytes)
	}

	// Once the reader lets go, an ordinary checkpoint, the kind SQLite runs
	// on its own, finishes the shrink that was reported as pending.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("end read: %v", err)
	}
	var busy, logFrames, done int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").
		Scan(&busy, &logFrames, &done); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if busy != 0 || done != logFrames {
		t.Fatalf("checkpoint after the reader closed: busy %d, %d of %d frames", busy, done, logFrames)
	}
	if fi, err = os.Stat(db.Path()); err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if fi.Size() != res.AfterBytes {
		t.Errorf("database file is %d bytes after the checkpoint, want the reported %d", fi.Size(), res.AfterBytes)
	}
}

func TestCompactRefusesWhenTheDiskCannotHoldTheCopy(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	bloat(t, db, 5000)

	withHooks(t,
		func(string) (int64, error) { return 4096, nil },
		func(string, string) bool { return true },
		nil)

	before, err := db.pageState(ctx)
	if err != nil {
		t.Fatalf("pageState: %v", err)
	}
	size := before.pages * before.pageSize

	_, err = db.Compact(ctx)
	if !errors.Is(err, ErrNotEnoughDisk) {
		t.Fatalf("Compact error = %v, want ErrNotEnoughDisk", err)
	}
	var disk *CompactDiskError
	if !errors.As(err, &disk) {
		t.Fatalf("Compact error %T does not say where the space ran out", err)
	}
	if disk.NeedBytes != 2*size || disk.FreeBytes != 4096 || disk.Dir != db.dataDir() {
		t.Errorf("refusal = %+v; want %d bytes needed in %s (copy plus log on one filesystem)",
			disk, 2*size, db.dataDir())
	}

	after, err := db.pageState(ctx)
	if err != nil {
		t.Fatalf("pageState: %v", err)
	}
	if after != before {
		t.Errorf("database changed although compacting was refused: %+v, was %+v", after, before)
	}

	plan, err := db.PlanCompact(ctx)
	if err != nil {
		t.Fatalf("PlanCompact: %v", err)
	}
	if plan.Disk == nil || plan.Disk.NeedBytes != 2*size {
		t.Errorf("plan.Disk = %+v, want the same refusal the action gives", plan.Disk)
	}
}

// With the temporary copy on another filesystem each side needs one database
// size, not two: a check that always asked for double would refuse a
// compaction that fits.
func TestCompactCountsATemporaryDirectoryOnAnotherFilesystemSeparately(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	bloat(t, db, 2000)
	s, err := db.pageState(ctx)
	if err != nil {
		t.Fatalf("pageState: %v", err)
	}
	size := s.pages * s.pageSize
	const tmp = "/elsewhere/tmp"

	free := map[string]int64{db.dataDir(): size, tmp: size - 1}
	withHooks(t,
		func(dir string) (int64, error) { return free[dir], nil },
		func(string, string) bool { return false },
		func() string { return tmp })

	plan, err := db.PlanCompact(ctx)
	if err != nil {
		t.Fatalf("PlanCompact: %v", err)
	}
	if plan.Disk == nil || plan.Disk.Dir != tmp || plan.Disk.NeedBytes != size {
		t.Fatalf("plan.Disk = %+v; want %d bytes needed in %s", plan.Disk, size, tmp)
	}

	free[tmp] = size
	plan, err = db.PlanCompact(ctx)
	if err != nil {
		t.Fatalf("PlanCompact: %v", err)
	}
	if plan.Disk != nil {
		t.Errorf("plan.Disk = %+v; one database size on each filesystem is enough", plan.Disk)
	}
}

func TestCompactRunsOneAtATime(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	db.compactMu.Lock()
	_, err := db.Compact(ctx)
	db.compactMu.Unlock()
	if !errors.Is(err, ErrCompactRunning) {
		t.Fatalf("Compact during another compaction = %v, want ErrCompactRunning", err)
	}
	if autoVacuumMode(t, db) != 0 {
		t.Error("the refused compaction changed the auto_vacuum mode")
	}

	if _, err := db.Compact(ctx); err != nil {
		t.Fatalf("Compact after the first finished: %v", err)
	}
}

// Offered only when it would change something: a database that never shrinks,
// or one that is more than a quarter empty.
func TestPlanCompactRecommendsItOnlyWhenItFreesSomething(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	plan, err := db.PlanCompact(ctx)
	if err != nil {
		t.Fatalf("PlanCompact: %v", err)
	}
	if !plan.Recommended || plan.AutoVacuum != "none" {
		t.Errorf("new database without auto-vacuum: plan = %+v, want recommended", plan)
	}

	if _, err := db.EnsureIncrementalVacuum(ctx); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}
	if plan, err = db.PlanCompact(ctx); err != nil || plan.Recommended || plan.AutoVacuum != "incremental" {
		t.Errorf("incremental database with no empty pages: plan = %+v, %v; want not recommended", plan, err)
	}

	// A rollup deletes raw rows without the incremental vacuum that ends a
	// full retention pass, so the pages stay empty until the next one.
	bloat(t, db, 20000)
	if plan, err = db.PlanCompact(ctx); err != nil || !plan.Recommended {
		t.Errorf("incremental database that is mostly empty: plan = %+v, %v; want recommended", plan, err)
	}
	if plan.FreeBytes*4 <= plan.SizeBytes {
		t.Fatalf("test setup: only %d of %d bytes free, want over a quarter", plan.FreeBytes, plan.SizeBytes)
	}
}

func TestCompactEstimateNeverReadsAsInstant(t *testing.T) {
	for _, c := range []struct {
		size int64
		want time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{16 << 20, time.Second},
		{16<<20 + 1, 2 * time.Second},
		{103 << 20, 7 * time.Second},
	} {
		if got := compactEstimate(c.size); got != c.want {
			t.Errorf("compactEstimate(%d) = %v, want %v", c.size, got, c.want)
		}
	}
}

// The directory is the one SQLite itself would pick, so the free-space check
// looks where the temporary copy is actually written.
func TestSQLiteTempDirFollowsSQLitesOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows uses the system temporary directory")
	}
	sqliteDir, tmpDir := t.TempDir(), t.TempDir()

	t.Setenv("SQLITE_TMPDIR", "")
	t.Setenv("TMPDIR", tmpDir)
	if got := sqliteTempDir(); got != tmpDir {
		t.Errorf("with TMPDIR set: %q, want %q", got, tmpDir)
	}

	t.Setenv("SQLITE_TMPDIR", sqliteDir)
	if got := sqliteTempDir(); got != sqliteDir {
		t.Errorf("with SQLITE_TMPDIR set: %q, want %q", got, sqliteDir)
	}

	t.Setenv("SQLITE_TMPDIR", filepath.Join(sqliteDir, "missing"))
	if got := sqliteTempDir(); got != tmpDir {
		t.Errorf("with SQLITE_TMPDIR missing: %q, want the next candidate %q", got, tmpDir)
	}
}

func TestAvailableBytesReadsTheRealFilesystem(t *testing.T) {
	free, err := availableBytes(t.TempDir())
	if err != nil {
		t.Fatalf("availableBytes: %v", err)
	}
	if free <= 0 {
		t.Errorf("availableBytes = %d, want a positive number on a writable directory", free)
	}
	if _, err := availableBytes(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("availableBytes on a missing directory returned no error")
	}
}
