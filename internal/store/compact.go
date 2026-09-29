package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Compacting the database: a full VACUUM that an admin starts by hand.
//
// Incremental auto-vacuum (see EnsureIncrementalVacuum) hands freed pages back
// to the filesystem after every retention pass, but only on a database that is
// already in that mode, and switching an existing database into it means
// rewriting the whole file. Startup does that rewrite for databases up to
// autoVacuumRebuildLimit and leaves anything larger alone, because a
// multi-gigabyte rewrite is a pause nobody chose. Until now the only way out
// was the advice to "run VACUUM manually", which the shipped image cannot
// follow: it is distroless and has no sqlite3 binary.
//
// Compact is that manual VACUUM, run by the process that already owns the
// file, at a moment the operator picks.

// ErrNotEnoughDisk is returned by Compact when the filesystem cannot hold the
// copies a VACUUM writes. Errors carrying it are *CompactDiskError, which names
// the directory and the numbers.
var ErrNotEnoughDisk = errors.New("store: not enough free disk space to compact the database")

// ErrCompactRunning is returned by Compact when another compaction has not
// finished yet.
var ErrCompactRunning = errors.New("store: the database is already being compacted")

// CompactDiskError says where a compaction would run out of space.
type CompactDiskError struct {
	// Dir is the directory whose filesystem is too full.
	Dir string
	// NeedBytes is what the compaction may write there.
	NeedBytes int64
	// FreeBytes is what the filesystem has available.
	FreeBytes int64
}

func (e *CompactDiskError) Error() string {
	return fmt.Sprintf("%s: compacting may write %d bytes to %s, which has %d bytes free",
		ErrNotEnoughDisk.Error(), e.NeedBytes, e.Dir, e.FreeBytes)
}

// Is makes errors.Is(err, ErrNotEnoughDisk) hold.
func (e *CompactDiskError) Is(target error) bool { return target == ErrNotEnoughDisk }

// compactFreeShare is the share of empty pages above which a compaction is
// worth offering on a database that already returns space incrementally.
//
// Incremental vacuum only runs at the end of a retention pass, so in between
// a large delete (a reset, a removed monitor with months of history) can leave
// much of the file empty. A quarter is where the file is visibly bigger than
// its contents; below that the rewrite costs more than it returns.
const compactFreeShare = 0.25

// compactBytesPerSecond is the rewrite speed the duration estimate assumes.
//
// Measured: a 103 MiB database took 3.05 s to VACUUM (34 MiB/s) on a two-core
// VPS with this pure-Go SQLite build. Half of that allows for SD cards and
// small ARM boards, where an estimate that is too long is harmless and one
// that is too short makes the operator think the process hung.
const compactBytesPerSecond = 16 << 20

// Test hooks. They are variables so the tests can reach the paths that need a
// large database or a full disk without producing either.
var (
	// rebuildLimit is the size above which startup leaves an existing
	// database out of incremental auto-vacuum mode.
	rebuildLimit int64 = autoVacuumRebuildLimit
	// diskFree reports the bytes this process may still write on the
	// filesystem holding dir.
	diskFree = availableBytes
	// sameFilesystem reports whether two directories share a filesystem.
	sameFilesystem = onSameFilesystem
	// compactTempDir is where SQLite writes its temporary copy.
	compactTempDir = sqliteTempDir
)

// CompactPlan describes the database as a compaction would find it.
type CompactPlan struct {
	// SizeBytes is the logical size of the database.
	SizeBytes int64
	// FreeBytes is the part of SizeBytes held by empty pages.
	FreeBytes int64
	// AutoVacuum is "none", "full" or "incremental".
	AutoVacuum string
	// Recommended is true when a compaction would change something: the
	// database is in no auto-vacuum mode, so it never shrinks, or more than
	// a quarter of it is empty.
	Recommended bool
	// Estimate is roughly how long the rewrite takes. Writes wait for it.
	Estimate time.Duration
	// Disk is nil when there is room, and says where there is not otherwise.
	Disk *CompactDiskError
}

// CompactResult reports what a compaction did.
type CompactResult struct {
	// BeforeBytes and AfterBytes are the logical sizes around the rewrite.
	BeforeBytes int64
	AfterBytes  int64
	// Duration is how long the rewrite held the writer.
	Duration time.Duration
	// AutoVacuum is the mode the database is in afterwards.
	AutoVacuum string
	// ShrinkPending is true when a reader still held a snapshot from before
	// the rewrite, so the checkpoint could not empty the write-ahead log. The
	// rewrite itself is done and AfterBytes is what the file will be, but it
	// keeps its old length until the next checkpoint gets through.
	ShrinkPending bool
}

// pageState is what the pragmas say about the file.
type pageState struct {
	mode, pages, free, pageSize int64
}

func (db *DB) pageState(ctx context.Context) (pageState, error) {
	var s pageState
	for _, q := range []struct {
		name string
		dst  *int64
	}{
		{"auto_vacuum", &s.mode},
		{"page_count", &s.pages},
		{"freelist_count", &s.free},
		{"page_size", &s.pageSize},
	} {
		if err := db.Writer.QueryRowContext(ctx, "PRAGMA "+q.name).Scan(q.dst); err != nil {
			return pageState{}, fmt.Errorf("read %s: %w", q.name, err)
		}
	}
	return s, nil
}

// PlanCompact reports whether a compaction is worth running, how long it is
// expected to take, and whether the disk has room for it. It changes nothing.
func (db *DB) PlanCompact(ctx context.Context) (CompactPlan, error) {
	s, err := db.pageState(ctx)
	if err != nil {
		return CompactPlan{}, err
	}
	size := s.pages * s.pageSize
	p := CompactPlan{
		SizeBytes:   size,
		FreeBytes:   s.free * s.pageSize,
		AutoVacuum:  autoVacuumName(s.mode),
		Recommended: s.mode == 0 || float64(s.free) > compactFreeShare*float64(s.pages),
		Estimate:    compactEstimate(size),
	}
	if p.Disk, err = db.compactDiskShortfall(size); err != nil {
		return CompactPlan{}, err
	}
	return p, nil
}

// Compact rewrites the database with VACUUM and leaves it in incremental
// auto-vacuum mode, so the file shrinks to its contents now and later
// retention passes keep it that way.
//
// It refuses before writing anything when the disk cannot hold the copies the
// rewrite makes (ErrNotEnoughDisk), and when a compaction is already running
// (ErrCompactRunning). While it runs, every other write waits for the single
// writer connection: checks keep running, and their results are recorded once
// it finishes.
func (db *DB) Compact(ctx context.Context) (CompactResult, error) {
	if !db.compactMu.TryLock() {
		return CompactResult{}, ErrCompactRunning
	}
	defer db.compactMu.Unlock()

	plan, err := db.PlanCompact(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	if plan.Disk != nil {
		return CompactResult{}, plan.Disk
	}

	start := time.Now()
	// Full mode already returns space on every commit and is left alone;
	// only a database with no auto-vacuum is moved to the mode retention
	// expects. The pragma records the intent, the VACUUM applies it.
	if plan.AutoVacuum == "none" {
		if _, err := db.Writer.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
			return CompactResult{}, fmt.Errorf("set auto_vacuum: %w", err)
		}
	}
	if _, err := db.Writer.ExecContext(ctx, "VACUUM"); err != nil {
		return CompactResult{}, fmt.Errorf("vacuum: %w", err)
	}
	// In WAL mode the rewritten pages land in the write-ahead log first, so
	// the database file keeps its old length until a checkpoint. The
	// TRUNCATE checkpoint shortens it now and empties the log as well. A
	// reader still holding an old snapshot blocks that, and SQLite reports
	// "busy" in the row rather than as an error. The rewrite has happened by
	// then, so failing here would send the operator to run it again for
	// nothing; the result says the shrink is pending instead, and the next
	// automatic checkpoint finishes it.
	var busy, logFrames, done int64
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &logFrames, &done); err != nil {
		return CompactResult{}, fmt.Errorf("checkpoint after vacuum: %w", err)
	}
	elapsed := time.Since(start)

	after, err := db.pageState(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{
		BeforeBytes:   plan.SizeBytes,
		AfterBytes:    after.pages * after.pageSize,
		Duration:      elapsed,
		AutoVacuum:    autoVacuumName(after.mode),
		ShrinkPending: busy != 0,
	}, nil
}

// compactDiskShortfall checks that a VACUUM of a database this size fits.
//
// SQLite builds the compacted copy in a temporary file, then writes every page
// of it through the write-ahead log beside the database. Each can be as large
// as the database, which is why SQLite asks for up to twice the database size
// in free space. The two places are usually one filesystem, but need not be (a
// container's /tmp and its /data volume), so they are checked together when
// they share one and separately when they do not.
func (db *DB) compactDiskShortfall(size int64) (*CompactDiskError, error) {
	type need struct {
		dir   string
		bytes int64
	}
	var needs []need
	dataDir := db.dataDir()
	tempDir := compactTempDir()
	switch {
	case dataDir == "":
		// The in-memory test database has no directory of its own.
		needs = append(needs, need{tempDir, size})
	case sameFilesystem(dataDir, tempDir):
		needs = append(needs, need{dataDir, 2 * size})
	default:
		needs = append(needs, need{dataDir, size}, need{tempDir, size})
	}
	for _, n := range needs {
		free, err := diskFree(n.dir)
		if err != nil {
			return nil, fmt.Errorf("check free disk space in %s: %w", n.dir, err)
		}
		if free < n.bytes {
			return &CompactDiskError{Dir: n.dir, NeedBytes: n.bytes, FreeBytes: free}, nil
		}
	}
	return nil, nil
}

// dataDir is the directory holding the database file, or "" for the in-memory
// database.
func (db *DB) dataDir() string {
	if strings.Contains(db.path, "mode=memory") {
		return ""
	}
	p := strings.TrimPrefix(db.Path(), "file:")
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Dir(p)
}

// sqliteTempDir is the directory SQLite picks for a temporary file, in the
// order its unix VFS tries them: SQLITE_TMPDIR, TMPDIR, then fixed paths,
// taking the first that is a writable directory. On Windows it is the system
// temporary directory, as os.TempDir reports it.
func sqliteTempDir() string {
	if runtime.GOOS == "windows" {
		return os.TempDir()
	}
	for _, dir := range []string{
		os.Getenv("SQLITE_TMPDIR"), os.Getenv("TMPDIR"),
		"/var/tmp", "/usr/tmp", "/tmp", ".",
	} {
		if dir != "" && writableDir(dir) {
			return dir
		}
	}
	return "."
}

// compactEstimate rounds the expected rewrite time up to whole seconds, and to
// at least one, so that no estimate reads as instant.
func compactEstimate(size int64) time.Duration {
	secs := (size + compactBytesPerSecond - 1) / compactBytesPerSecond
	return time.Duration(max(secs, 1)) * time.Second
}

func autoVacuumName(mode int64) string {
	switch mode {
	case 1:
		return "full"
	case 2:
		return "incremental"
	default:
		return "none"
	}
}
