package main

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A taken port stops the server before it starts checking monitors or
// delivering alerts, with one error that says what to change and no earlier
// line claiming it was listening.
func TestServerRefusesATakenPortBeforeStarting(t *testing.T) {
	clearBackupEnv(t)
	taken, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	var logs lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"--data-dir", t.TempDir(), "--addr", taken.Addr().String()}, &logs)
	}()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server kept running on a port another process holds")
	}
	if !errors.Is(runErr, syscall.EADDRINUSE) {
		t.Fatalf("run: err = %v, want address already in use", runErr)
	}
	if !strings.Contains(runErr.Error(), "--addr") {
		t.Errorf("run: err = %q, want it to name --addr as the fix", runErr)
	}

	// Anything started before the bind would log after run returned and the
	// database closed; give it the moment it would need.
	time.Sleep(200 * time.Millisecond)
	out := logs.String()
	for _, line := range []string{"http server listening", "notifier started", "restored open incidents"} {
		if strings.Contains(out, line) {
			t.Errorf("logged %q before the bind failed:\n%s", line, out)
		}
	}
	if !strings.Contains(out, "database ready") {
		t.Errorf("the run did not get as far as the bind; the assertions above prove nothing:\n%s", out)
	}
}

// lockedBuffer is a log destination that goroutines may write to while the
// test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
