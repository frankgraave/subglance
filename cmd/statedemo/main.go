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
		name     string
		typ      string
		target   string
		retries  int
		warnDays int
	}{
		{name: "HTTP: example.com", typ: "http", target: "https://example.com", retries: 2},
		{name: "HTTP: 404 (patient)", typ: "http", target: "https://example.com/definitely-not-here", retries: 3},
		{name: "TCP: cloudflare DNS:53", typ: "tcp", target: "1.1.1.1:53", retries: 2},
		{name: "TCP: closed port", typ: "tcp", target: "example.com:9999", retries: 1},
		{name: "PING: cloudflare", typ: "ping", target: "1.1.1.1", retries: 2},
		{name: "PING: unknown host", typ: "ping", target: "no-such-host.invalid", retries: 1},
		{name: "SSL: example.com", typ: "ssl", target: "example.com", retries: 2, warnDays: 14},
		{name: "SSL: expired cert", typ: "ssl", target: "expired.badssl.com", retries: 1, warnDays: 14},
		{name: "SSL: wrong host", typ: "ssl", target: "wrong.host.badssl.com", retries: 1, warnDays: 14},
		{name: "SSL: self-signed", typ: "ssl", target: "self-signed.badssl.com", retries: 1, warnDays: 14},
	}

	for _, t := range targets {
		if _, err := db.CreateMonitor(ctx, store.Monitor{
			Name:        t.name,
			Type:        t.typ,
			Target:      t.target,
			IntervalS:   20,
			TimeoutS:    10,
			Retries:     t.retries,
			SSLWarnDays: t.warnDays,
			Enabled:     true,
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

	fmt.Println("Running all four check types for 90 seconds against real endpoints.")
	fmt.Println("Watch the patient monitors: they need several consecutive failures")
	fmt.Println("before they alert, while the immediate ones alert on the first.")
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
		fmt.Printf("  type: %-4s  threshold: %d consecutive failures\n", m.Type, m.Retries)
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
