package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunBackupWritesRestorableFile(t *testing.T) {
	dataDir := t.TempDir()
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
	dataDir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "already-there.db")
	if err := os.WriteFile(dest, []byte("previous backup"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	if err := runBackup([]string{dest, "--data-dir", dataDir}, &out); err == nil {
		t.Fatal("runBackup overwrote an existing backup, want an error")
	}
}
