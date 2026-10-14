package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every entry point reads its settings through config.Load, so a stray word
// is refused by the server and by each subcommand alike. These cases put the
// word after the flags it would otherwise have made the process ignore: before
// the refusal, each of them ran.

func TestServerRefusesAStrayArgument(t *testing.T) {
	clearBackupEnv(t)
	dir := filepath.Join(t.TempDir(), "never-created")

	var logs lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"--data-dir", dir, "--addr", "127.0.0.1:0", "serve"}, &logs)
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server started with a stray argument instead of refusing it")
	}
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "serve"`) {
		t.Fatalf("run: err = %v, want it to name the stray argument", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("the data directory was created before the refusal (stat: %v)", statErr)
	}
	if out := logs.String(); out != "" {
		t.Errorf("logged before refusing:\n%s", out)
	}
}

func TestHealthcheckRefusesAStrayArgument(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ready","database":"up"}`))
	}))
	defer srv.Close()

	// The server is ready, so only the refusal can make this fail.
	err := runHealthcheck([]string{"--addr", listenAddr(t, srv.URL), "now"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "now"`) {
		t.Fatalf("runHealthcheck: err = %v, want it to name the stray argument", err)
	}
}

// For a restore the stakes are the database: `restore latest --data-dir X`
// used to read /data, not X, and put the backup there.
func TestRestoreRefusesAStrayArgument(t *testing.T) {
	clearBackupEnv(t)
	err := runRestore([]string{"latest", "--data-dir", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "latest"`) {
		t.Fatalf("runRestore: err = %v, want it to name the stray argument", err)
	}
}
