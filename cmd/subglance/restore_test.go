package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/datalock"
)

func TestSplitRestoreFlags(t *testing.T) {
	own, rest := splitRestoreFlags([]string{
		"--from", "subglance-20260925T030000Z.db.gz", "--data-dir", "/data", "-force", "--log-level=debug",
	})
	if got := strings.Join(own, " "); got != "--from subglance-20260925T030000Z.db.gz -force" {
		t.Errorf("own = %q", got)
	}
	if got := strings.Join(rest, " "); got != "--data-dir /data --log-level=debug" {
		t.Errorf("rest = %q", got)
	}
}

func TestRestoreNeedsATarget(t *testing.T) {
	// Hermetic even in a shell that exports the backup settings: otherwise
	// this would dial a real bucket.
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "")
	err := runRestore([]string{"--data-dir", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "SUBGLANCE_BACKUP_TARGET") {
		t.Fatalf("err = %v, want one naming SUBGLANCE_BACKUP_TARGET", err)
	}
}

// Replacing the database under a running server is the one way a restore
// loses data, so a listener on --addr stops it before anything is downloaded.
func TestRestoreRefusesWhileTheServerAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://bucket")
	t.Setenv("SUBGLANCE_BACKUP_ACCESS_KEY_ID", "key-id")
	t.Setenv("SUBGLANCE_BACKUP_SECRET_ACCESS_KEY", "secret")

	dataDir := t.TempDir()
	err = runRestore([]string{"--data-dir", dataDir, "--addr", ln.Addr().String()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Fatalf("err = %v, want a refusal because the server is running", err)
	}

	// The restore took the data directory lock before it looked at the
	// address; refusing must give it back, or the server could not start.
	l, err := datalock.Acquire(dataDir)
	if err != nil {
		t.Fatalf("data directory still locked after the restore gave up: %v", err)
	}
	_ = l.Release()
}

// The lock is the guard the address check cannot be: `docker compose run`
// dials its own loopback and finds nothing, while the server runs on the same
// volume in another container. So a held lock refuses the restore even with
// --force and nothing listening, before any byte is downloaded.
func TestRestoreRefusesWhileAServerHoldsTheDataDir(t *testing.T) {
	var requests int
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s3.Close()

	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://bucket")
	t.Setenv("SUBGLANCE_BACKUP_ENDPOINT", s3.URL)
	t.Setenv("SUBGLANCE_BACKUP_ACCESS_KEY_ID", "key-id")
	t.Setenv("SUBGLANCE_BACKUP_SECRET_ACCESS_KEY", "secret")

	dataDir := t.TempDir()
	server, err := datalock.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Release() }()

	err = runRestore([]string{"--data-dir", dataDir, "--addr", freeAddr(t), "--force"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stop it first") || !strings.Contains(err.Error(), datalock.Path(dataDir)) {
		t.Fatalf("err = %v, want a refusal naming the lock file", err)
	}
	if requests != 0 {
		t.Errorf("the restore reached the bucket %d times while the data directory was locked", requests)
	}
}

// A second server on one data directory refuses to start, and says which
// file it found held.
func TestServerRefusesADataDirAnotherServerHolds(t *testing.T) {
	dataDir := t.TempDir()
	first, err := datalock.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	t.Setenv("SUBGLANCE_BACKUP_TARGET", "")
	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() { done <- run([]string{"--data-dir", dataDir, "--addr", addr}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), datalock.Path(dataDir)) {
			t.Fatalf("run: err = %v, want a refusal naming the lock file", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a second server started on a data directory that was already locked")
	}
}

// freeAddr is a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
