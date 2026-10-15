//go:build unix

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A database an older version left readable by others is made owner-only
// when the server starts, and the log says so. The server is stopped at the
// bind by a taken port, which comes after the database is opened.
func TestServerNarrowsAReadableDatabaseAtStartup(t *testing.T) {
	clearBackupEnv(t)
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "subglance.db")
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}

	taken, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	var logs lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"--data-dir", dataDir, "--addr", taken.Addr().String()}, &logs)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server kept running on a port another process holds")
	}

	out := logs.String()
	if !strings.Contains(out, "database ready") {
		t.Fatalf("the run did not open the database; the check below proves nothing:\n%s", out)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("database mode after startup = %v, want -rw-------", got)
	}
	if !strings.Contains(out, "made the database files readable by their owner only") || !strings.Contains(out, dbPath) {
		t.Errorf("the log does not say which file was narrowed:\n%s", out)
	}
}
