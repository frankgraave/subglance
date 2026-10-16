package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/frankgraave/subglance/internal/store"
)

// seededDataDir is a data directory holding a database the server created,
// with one account in it, which is what a backup is taken of.
func seededDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "subglance.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(ctx, "owner@example.com", "a-password-of-mine", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunBackupWritesRestorableFile(t *testing.T) {
	dataDir := seededDataDir(t)
	dest := filepath.Join(t.TempDir(), "subglance-backup.db")

	var out bytes.Buffer
	if err := runBackup([]string{dest, "--data-dir", dataDir}, &out); err != nil {
		t.Fatalf("runBackup: %v", err)
	}
	if !strings.Contains(out.String(), dest) {
		t.Errorf("output does not name the file written: %q", out.String())
	}

	// The destination flag must be honoured even though it follows the
	// positional path, which is the ordering a person actually types.
	copyDB, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = copyDB.Close() }()

	var n int
	if err := copyDB.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if n == 0 {
		t.Error("backup contains no applied migrations")
	}
	var email string
	if err := copyDB.QueryRow("SELECT email FROM users").Scan(&email); err != nil {
		t.Fatalf("query backup users: %v", err)
	}
	if email != "owner@example.com" {
		t.Errorf("backup holds account %q, want the one in the data directory", email)
	}
}

// A data directory without a database is a mistyped --data-dir or an unset
// SUBGLANCE_DATA_DIR. Backing it up used to create an empty database there,
// copy it and exit 0, so a nightly job succeeded every night and kept
// nothing. It must refuse, name what it looked for, and leave both the
// directory and the destination untouched.
func TestRunBackupRefusesADataDirWithoutADatabase(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "subglance-backup.db")

	var out bytes.Buffer
	err := runBackup([]string{dest, "--data-dir", dataDir}, &out)
	if err == nil {
		t.Fatalf("runBackup of an empty data directory succeeded (%q), want a refusal", out.String())
	}
	dbPath := filepath.Join(dataDir, "subglance.db")
	for _, want := range []string{dbPath, "--data-dir", "SUBGLANCE_DATA_DIR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a database was created in the wrong directory: %v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a backup was written of a database that does not exist: %v", err)
	}
}

func TestRunBackupNeedsAPath(t *testing.T) {
	var out bytes.Buffer
	err := runBackup([]string{"--data-dir", t.TempDir()}, &out)
	if err == nil {
		t.Fatal("runBackup without a path succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error does not show usage: %v", err)
	}
}

func TestRunBackupRefusesExistingFile(t *testing.T) {
	dataDir := seededDataDir(t)
	dest := filepath.Join(t.TempDir(), "already-there.db")
	if err := os.WriteFile(dest, []byte("previous backup"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	if err := runBackup([]string{dest, "--data-dir", dataDir}, &out); err == nil {
		t.Fatal("runBackup overwrote an existing backup, want an error")
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "previous backup" {
		t.Errorf("existing backup changed: %q, %v", got, err)
	}
}
