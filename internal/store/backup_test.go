package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openBackupCopy opens a backup file read-only, the way a restore would: a
// plain connection with no migrations, so the test sees exactly what landed on
// disk.
func openBackupCopy(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBackupToContainsData(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for i := range 25 {
		if _, err := db.Writer.ExecContext(ctx,
			"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
			fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i), time.Now().Unix(),
		); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
	}

	dest := filepath.Join(t.TempDir(), "backup.db")
	if size, err := db.BackupTo(ctx, dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	} else if size <= 0 {
		t.Errorf("BackupTo reported size %d, want a real file", size)
	}

	copyDB := openBackupCopy(t, dest)

	var n int
	if err := copyDB.QueryRowContext(ctx,
		"SELECT count(*) FROM settings WHERE key LIKE 'key-%'").Scan(&n); err != nil {
		t.Fatalf("count settings in backup: %v", err)
	}
	if n != 25 {
		t.Errorf("backup holds %d settings rows, want 25", n)
	}

	// The schema ledger has to travel with the data, or a restored file would
	// be re-migrated from scratch on top of tables that already exist.
	var migrations int
	if err := copyDB.QueryRowContext(ctx,
		"SELECT count(*) FROM schema_migrations").Scan(&migrations); err != nil {
		t.Fatalf("count schema_migrations in backup: %v", err)
	}
	if migrations == 0 {
		t.Error("backup has no schema_migrations rows")
	}

	// One file, not three: the point of VACUUM INTO over copying is that
	// there is no -wal alongside it holding the recent writes.
	if _, err := os.Stat(dest + "-wal"); !os.IsNotExist(err) {
		t.Errorf("backup left a -wal file beside it (stat err = %v)", err)
	}
}

// TestBackupIsConsistentUnderWrites is the whole reason this command exists.
// A file copy taken while SubGlance is writing can catch a half-applied
// transaction; VACUUM INTO runs in a read transaction and cannot.
//
// Commit one pair, then hold a second transaction open after its first row.
// The reader must capture the committed pair without exposing the pending half,
// while the writer connection remains occupied. No scheduler delay decides
// whether the fixture contains data or has a write transaction in progress.
func TestBackupIsConsistentUnderWrites(t *testing.T) {
	db := openTestDB(t)
	// A deadline bounds a regression that wrongly uses the occupied writer
	// pool; it is not a warm-up period or part of the successful ordering.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	now := time.Now().Unix()
	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES
		('pair-000000-a', 'a', ?), ('pair-000000-b', 'b', ?)
	`, now, now); err != nil {
		t.Fatalf("commit initial pair: %v", err)
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent write: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
		"pair-000001-a", "a", now); err != nil {
		t.Fatalf("write pending half: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "hot.db")
	if _, err := db.BackupTo(ctx, dest); err != nil {
		t.Fatalf("BackupTo while writing: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
		"pair-000001-b", "b", now); err != nil {
		t.Fatalf("finish pending pair: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit pending pair after backup: %v", err)
	}
	var liveRows int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM settings WHERE key LIKE 'pair-%'").Scan(&liveRows); err != nil {
		t.Fatalf("count live rows: %v", err)
	}
	if liveRows != 4 {
		t.Fatalf("live database has %d pair rows, want both committed pairs", liveRows)
	}

	copyDB := openBackupCopy(t, dest)

	var halves, pairs int
	if err := copyDB.QueryRowContext(ctx,
		"SELECT count(*) FROM settings WHERE key LIKE 'pair-%'").Scan(&halves); err != nil {
		t.Fatalf("count halves: %v", err)
	}
	if err := copyDB.QueryRowContext(ctx, `
		SELECT count(*) FROM settings a
		WHERE a.key LIKE 'pair-%-a'
		  AND EXISTS (SELECT 1 FROM settings b
		              WHERE b.key = replace(a.key, '-a', '-b'))
	`).Scan(&pairs); err != nil {
		t.Fatalf("count pairs: %v", err)
	}

	if halves == 0 {
		t.Fatal("backup lost the committed pair; the test proves nothing")
	}
	if halves != pairs*2 {
		t.Errorf("backup holds %d rows but only %d complete pairs: a transaction was torn", halves, pairs)
	}
	if halves != 2 {
		t.Errorf("backup holds %d pair rows, want only the pair committed before the snapshot", halves)
	}
}

func TestBackupRefusesExistingDestination(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	dest := filepath.Join(t.TempDir(), "taken.db")
	if err := os.WriteFile(dest, []byte("an older backup nobody wants to lose"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	if _, err := db.BackupTo(ctx, dest); err == nil {
		t.Fatal("BackupTo overwrote an existing file, want an error")
	}

	// The existing file has to be untouched: the failure mode being guarded
	// against is a cron job destroying the last good copy.
	b, err := os.ReadFile(dest) //nolint:gosec // path is built by the test
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != "an older backup nobody wants to lose" {
		t.Errorf("existing file was modified: %q", b)
	}
}

func TestBackupRejectsMissingDirectory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	dest := filepath.Join(t.TempDir(), "no-such-dir", "backup.db")
	_, err := db.BackupTo(ctx, dest)
	if err == nil {
		t.Fatal("BackupTo into a missing directory succeeded, want an error")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a file was left behind after a failed backup")
	}
}

func TestBackupRejectsEmptyDestination(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.BackupTo(context.Background(), ""); err == nil {
		t.Fatal("BackupTo(\"\") succeeded, want an error")
	}
}
