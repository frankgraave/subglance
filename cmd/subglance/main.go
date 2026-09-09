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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/buildinfo"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/logging"
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

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.New(log, db).Handler(),

		// Bounded timeouts: an unbounded server is a resource leak waiting
		// for one slow client. ReadHeaderTimeout in particular defends
		// against Slowloris.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Shut down cleanly on SIGINT/SIGTERM so that in-flight requests finish
	// and (later) the database closes without corrupting the WAL.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	log.Info("shutdown complete")
	return nil
}
