// Command statedemo proves the state engine end to end against real targets.
//
// It runs a short live session with a deliberately low interval and prints
// every transition, so the confirmation delay and incident lifecycle are
// visible rather than merely asserted in a test.
//
//	go run ./cmd/statedemo
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbPath := "/tmp/subglance-statedemo.db"
	_ = os.Remove(dbPath)

	db, err := store.Open(ctx, store.Options{Path: dbPath})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = db.Close() }()

	targets := []struct {
		name    string
		target  string
		retries int
	}{
		{"example.com (healthy)", "https://example.com", 2},
		{"404 page (patient)", "https://example.com/definitely-not-here", 3},
		{"expired cert (immediate)", "https://expired.badssl.com", 1},
	}

	for _, t := range targets {
		if _, err := db.CreateMonitor(ctx, store.Monitor{
			Name:      t.name,
			Type:      "http",
			Target:    t.target,
			IntervalS: 20,
			TimeoutS:  10,
			Retries:   t.retries,
			Enabled:   true,
		}); err != nil {
			return fmt.Errorf("create monitor %s: %w", t.name, err)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	alerts := 0
	r := monitor.New(monitor.Options{
		DB:             db,
		Log:            log,
		ReloadInterval: 30 * time.Second,
		Notify: func(a monitor.Alert) {
			alerts++
			fmt.Printf("\n  🔔 ALERT #%d  %s — %s\n     cause: %s\n     since: %s\n\n",
				alerts, a.Monitor.Name, a.Event, a.Incident.Cause,
				a.Incident.StartedAt.Format(time.RFC3339))
		},
	})

	fmt.Println("Running for 90 seconds. Watch the 404 monitor: it needs three")
	fmt.Println("consecutive failures before it alerts, while the expired cert")
	fmt.Println("alerts on the first.")
	fmt.Println()

	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(runCtx)
	}()
	<-done

	fmt.Println("\n─── summary ─────────────────────────────────")

	monitors, err := db.ListMonitors(context.Background())
	if err != nil {
		return err
	}

	for _, m := range monitors {
		stats, _ := db.Uptime(context.Background(), m.ID, time.Hour)
		incidents, _ := db.ListIncidents(context.Background(), m.ID, 10)

		fmt.Printf("\n%s\n", m.Name)
		fmt.Printf("  threshold: %d consecutive failures\n", m.Retries)
		fmt.Printf("  checks:    %d (%d up, %d down, %.1f%% uptime)\n",
			stats.Total, stats.Up, stats.Down, stats.Percentage)

		if len(incidents) == 0 {
			fmt.Println("  incidents: none")
			continue
		}
		for _, inc := range incidents {
			status := "open"
			if inc.Resolved() {
				status = "resolved"
			}
			confirmed := "unconfirmed (never alerted)"
			if inc.Confirmed() {
				delay := inc.ConfirmedAt.Sub(inc.StartedAt)
				confirmed = fmt.Sprintf("confirmed after %s", delay.Round(time.Second))
			}
			fmt.Printf("  incident:  %s, %s — %s\n", status, confirmed, inc.LastError)
		}
	}

	fmt.Printf("\ntotal alerts sent: %d\n", alerts)
	return nil
}
