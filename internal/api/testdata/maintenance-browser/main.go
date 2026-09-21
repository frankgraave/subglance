// maintenance-browser exercises the real checker, runner, store, API and stream.
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
	"strconv"
	"sync/atomic"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/auth"
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
	password, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	user, err := db.CreateUser(ctx, "maintenance@example.invalid", password, store.RoleAdmin)
	if err != nil {
		return err
	}
	session, err := db.CreateSession(ctx, user.ID, "maintenance fixture", "127.0.0.1")
	if err != nil {
		return err
	}
	var healthy [8]atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, _ := strconv.Atoi(r.URL.Path[1:])
		if i >= 0 && i < len(healthy) && healthy[i].Load() {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte("temporary upstream failure"))
	}))
	defer target.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus(64)
	var alerts atomic.Int64
	runner := monitor.New(monitor.Options{DB: db, Log: log, Bus: bus, AllowPrivateTargets: true, Notify: func(monitor.Alert) { alerts.Add(1) }})
	ids := []int64{}
	for i := range 8 {
		m, err := db.CreateMonitor(ctx, store.Monitor{Name: fmt.Sprintf("Service %d", i), Type: "http", Target: fmt.Sprintf("%s/%d", target.URL, i), Enabled: true, Retries: 2, IntervalS: 300, CaptureResponse: true, Tags: map[string]string{"env": "prod"}})
		if err != nil {
			return err
		}
		ids = append(ids, m.ID)
	}
	if alerts.Load() != 0 {
		return fmt.Errorf("warnings notified")
	}
	server := httptest.NewServer(api.New(log, db).WithBus(bus).WithProber(runner).Handler())
	defer server.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"url": server.URL, "session": session, "ids": ids}); err != nil {
		return err
	}
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		var c struct {
			Index      int  `json:"index"`
			Healthy    bool `json:"healthy"`
			Disconnect bool `json:"disconnect"`
		}
		if err := json.Unmarshal(commands.Bytes(), &c); err != nil {
			return err
		}
		if c.Disconnect {
			server.CloseClientConnections()
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true}); err != nil {
				return err
			}
			continue
		}
		if c.Index < 0 || c.Index >= 8 {
			return fmt.Errorf("invalid monitor")
		}
		before := alerts.Load()
		healthy[c.Index].Store(c.Healthy)
		m, err := db.GetMonitor(ctx, ids[c.Index])
		if err != nil {
			return err
		}
		if _, err := runner.CheckNow(ctx, m); err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "added_alerts": alerts.Load() - before}); err != nil {
			return err
		}
	}
	return commands.Err()
}
