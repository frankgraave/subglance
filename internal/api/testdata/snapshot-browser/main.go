// snapshot-browser serves the real API and embedded UI over a temporary store
// for the response-history browser test. Nothing is mocked at the API boundary.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: os.Args[1]})
	if err != nil {
		return err
	}
	defer db.Close()
	// Generated just for this disposable account, never typed or printed.
	password, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	user, err := db.CreateUser(ctx, "snapshot-test@example.com", password, store.RoleAdmin)
	if err != nil {
		return err
	}
	session, err := db.CreateSession(ctx, user.ID, "snapshot browser test", "127.0.0.1")
	if err != nil {
		return err
	}

	var healthy atomic.Bool
	body := `<script>window.snapshotExecuted = true</script><img src=x onerror="window.snapshotExecuted = true">[link](javascript:alert(1))` + strings.Repeat("x", checker.MaxSnapshotBytes)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Retry-After", "120")
		w.Header().Set("X-Request-Id", "<b>request-1</b>")
		w.Header().Set("Set-Cookie", "session=must-not-store")
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(body))
	}))
	defer target.Close()
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "Response diagnostics", Type: "http", Target: target.URL,
		Enabled: true, CaptureResponse: true, Retries: 1,
	})
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus(32)
	runner := monitor.New(monitor.Options{DB: db, Log: log, Bus: bus, AllowPrivateTargets: true, FlapThreshold: 2, FlapWindow: time.Hour})
	// First response is captured; then genuine confirmed flips suppress the
	// next failure. Changing today's setting cannot rewrite either old reason.
	if _, err := runner.CheckNow(ctx, m); err != nil {
		return err
	}
	healthy.Store(true)
	if _, err := runner.CheckNow(ctx, m); err != nil {
		return err
	}
	healthy.Store(false)
	if _, err := runner.CheckNow(ctx, m); err != nil {
		return err
	}
	m.CaptureResponse = false
	m, err = db.UpdateMonitor(ctx, m)
	if err != nil {
		return err
	}
	if _, err := runner.CheckNow(ctx, m); err != nil {
		return err
	}
	// An earlier row with no recorded decision stays unknown.
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: m.ID, TS: time.Now().Add(-time.Hour), Error: "older failure"}); err != nil {
		return err
	}

	server := httptest.NewServer(api.New(log, db).WithBus(bus).WithProber(runner).Handler())
	defer server.Close()
	// A separate history for polling identity tests, with truly identical
	// snapshots and second-resolution timestamps. Only the store IDs differ.
	identityMonitor, err := db.CreateMonitor(ctx, store.Monitor{Name: "Polling identity", Type: "http", Target: target.URL, Enabled: true})
	if err != nil {
		return err
	}
	identityBeat := store.Heartbeat{MonitorID: identityMonitor.ID, TS: time.Unix(1_700_000_000, 0), StatusCode: 503,
		Response: &store.ResponseSnapshot{Body: strings.Repeat("diagnostic line\n", 100)}}
	for range 2 {
		if err := db.RecordHeartbeat(ctx, identityBeat); err != nil {
			return err
		}
	}
	// This record travels only through a pipe to the test, never the report.
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"url": server.URL, "session": session, "monitor_id": m.ID, "identity_monitor_id": identityMonitor.ID}); err != nil {
		return err
	}
	// The test's private stdin pipe is the only mutation control; no test-only
	// HTTP routes or unauthenticated write surface are added to the server.
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		var command struct {
			TS time.Time `json:"ts"`
		}
		if err := json.Unmarshal(commands.Bytes(), &command); err != nil {
			return err
		}
		identityBeat.TS = command.TS
		if err := db.RecordHeartbeat(ctx, identityBeat); err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]bool{"recorded": true}); err != nil {
			return err
		}
	}
	return commands.Err()
}
