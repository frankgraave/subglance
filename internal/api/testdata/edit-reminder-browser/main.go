// This executable is a loopback-only fixture for built-browser integration.
// It exercises the real API, SQLite store, checker and embedded frontend.
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
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "subglance-edit-browser-")
	must(err)
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "fixture.db")})
	must(err)
	defer func() { _ = db.Close() }()
	user, err := db.CreateUser(ctx, "operator@example.invalid", "temporary-browser-fixture-password", store.RoleAdmin)
	must(err)
	token, _, err := db.CreateAPIToken(ctx, user.ID, "browser fixture", nil)
	must(err)
	var probes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stats" {
			w.Header().Set("Content-Type", "application/json")
			must(json.NewEncoder(w).Encode(map[string]any{"probes": probes.Load()}))
			return
		}
		probes.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "maintenance")
	}))
	defer target.Close()
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "Real API monitor", Type: "http", Target: target.URL + "/old",
		Enabled: true, IntervalS: 86400, TimeoutS: 5, Retries: 1,
		Method: "POST", ExpectedStatus: "200", FollowRedirects: false,
		Headers: map[string]string{"X-Check": "retain-me"}, Body: "retained-body",
		RepeatAfterS: 900, SSLWarnDays: 14,
	})
	must(err)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus(64)
	runner := monitor.New(monitor.Options{DB: db, Log: log, Bus: bus, AllowPrivateTargets: true})
	// No scheduler loop: only explicit test actions may record history.
	_, err = runner.CheckNow(ctx, m)
	must(err)
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	must(err)
	must(db.RecordReminder(ctx, inc.ID, time.Now().UTC().Add(-2*time.Minute)))
	must(db.RecordReminder(ctx, inc.ID, time.Now().UTC().Add(-time.Minute)))
	server := httptest.NewServer(api.New(log, db).WithProber(runner).WithPushRecorder(runner).WithBus(bus).Handler())
	defer server.Close()
	// The short-lived test token travels only to the test process over stdout;
	// never log this bootstrap object or persist it with screenshot evidence.
	bootstrap := map[string]any{"url": server.URL, "token": token, "monitor_id": m.ID, "incident_id": inc.ID, "target": target.URL}
	must(json.NewEncoder(os.Stdout).Encode(bootstrap))
	_, _ = fmt.Fscanln(bufio.NewReader(os.Stdin), new(string))
}
