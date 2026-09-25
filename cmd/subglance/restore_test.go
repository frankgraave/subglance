package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
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

	err = runRestore([]string{"--data-dir", t.TempDir(), "--addr", ln.Addr().String()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Fatalf("err = %v, want a refusal because the server is running", err)
	}
}
