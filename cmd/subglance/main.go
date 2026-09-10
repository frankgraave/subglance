// Command subglance runs the SubGlance monitoring server.
//
//	subglance --addr :8080 --data-dir /data
//
// See docs/ARCHITECTURE.md for how the pieces fit together.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/buildinfo"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/logging"
	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // the user asked for usage; not a failure
		}
		fmt.Fprintf(os.Stderr, "subglance: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}

	log := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	log.Info("starting subglance",
		"version", buildinfo.Short(),
		"addr", cfg.Addr,
		"data_dir", cfg.DataDir,
	)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}

	// Give startup its own bounded context: a database that hangs on open
	// should fail loudly rather than leave the process wedged before it ever
	// serves a request.
	openCtx, cancelOpen := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelOpen()

	db, err := store.Open(openCtx, store.Options{Path: cfg.DBPath()})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database", "error", err)
		}
	}()
	log.Info("database ready", "path", db.Path())

	// Tell the operator plainly when the instance has no account yet. There is
	// no seeded user and no default password to change — an instance exposed
	// before setup has no credentials to guess — but that only helps if the
	// person running it knows to go and finish setup.
	if n, err := db.CountUsers(openCtx); err != nil {
		log.Error("could not count users", "error", err)
	} else if n == 0 {
		log.Warn("no accounts yet: open the web interface to create the first administrator",
			"setup_url", "http://"+displayAddr(cfg.Addr)+"/")
	}

	// One bus, shared by the checker pipeline (publisher) and the API
	// (subscriber). Created before both so neither has to know about the
	// other's lifetime.
	bus := events.NewBus(0)

	runner := monitor.New(monitor.Options{
		DB:                  db,
		Log:                 log,
		AllowPrivateTargets: cfg.AllowPrivateTargets,
		Workers:             cfg.CheckWorkers,
		Bus:                 bus,
	})

	srv := &http.Server{
		Addr: cfg.Addr,
		// The runner doubles as the API's prober, so a manual check uses
		// the same checkers, the same SSRF guard and the same recording
		// path as a scheduled one.
		Handler: api.New(log, db).WithBus(bus).WithProber(runner).Handler(),

		// Bounded timeouts: an unbounded server is a resource leak waiting
		// for one slow client. ReadHeaderTimeout in particular defends
		// against Slowloris.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,

		// No WriteTimeout: it is an absolute deadline on the whole response,
		// so any value would sever every SSE connection that lived longer
		// than it — a dashboard that silently dies after a minute. Slow
		// clients are bounded by IdleTimeout and by the stream's own
		// non-blocking fan-out instead.
		IdleTimeout: 120 * time.Second,
	}

	// Shut down cleanly on SIGINT/SIGTERM so that in-flight requests finish
	// and (later) the database closes without corrupting the WAL.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		// Run returns ctx.Err() on shutdown, which is expected rather than a
		// failure worth reporting.
		if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("scheduler stopped unexpectedly", "error", err)
		}
	}()

	// Expired sessions and stale login-attempt rows accumulate forever
	// otherwise. Cheap deletes, so hourly is plenty.
	go reapExpired(ctx, db, log)

	// Raw heartbeats are the fastest-growing table in the product. Rolling
	// them up keeps history unlimited at a bounded cost.
	go rollupHeartbeats(ctx, db, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received", "timeout", cfg.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	// Wait for in-flight checks so no result is lost mid-write.
	select {
	case <-schedulerDone:
	case <-shutdownCtx.Done():
		log.Warn("scheduler did not stop within the shutdown timeout")
	}

	log.Info("shutdown complete")
	return nil
}

// displayAddr turns a listen address into something a person can paste into a
// browser: ":8080" alone is not a URL anyone can click.
func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// reapExpired periodically clears expired sessions and old login attempts.
//
// Neither is urgent, so failures are logged and retried on the next tick
// rather than treated as fatal.
// rollupHeartbeats folds raw heartbeats older than the retention window into
// hourly buckets, once a day.
//
// It runs once at startup rather than waiting out the first interval: an
// instance that is restarted more often than the interval would otherwise
// never roll up at all, and that is exactly the instance whose database grows
// without anyone noticing.
func rollupHeartbeats(ctx context.Context, db *store.DB, log *slog.Logger) {
	const interval = 24 * time.Hour

	run := func() {
		res, err := db.RollupHeartbeats(ctx, store.DefaultRawRetention)
		if err != nil {
			// A failed rollup costs disk, not correctness: the raw rows are
			// still there and the next pass picks them up.
			log.Error("heartbeat rollup", "error", err)
			return
		}
		if res.Heartbeats > 0 {
			log.Info("rolled up heartbeats",
				"heartbeats", res.Heartbeats,
				"buckets", res.Buckets,
				"cutoff", res.Cutoff)
		}
	}

	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func reapExpired(ctx context.Context, db *store.DB, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := db.PurgeExpiredSessions(ctx); err != nil {
				log.Error("purge expired sessions", "error", err)
			} else if n > 0 {
				log.Debug("purged expired sessions", "count", n)
			}

			if n, err := db.PurgeOldLoginAttempts(ctx, 24*time.Hour); err != nil {
				log.Error("purge login attempts", "error", err)
			} else if n > 0 {
				log.Debug("purged old login attempts", "count", n)
			}
		}
	}
}
