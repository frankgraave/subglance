// Command subglance runs the SubGlance monitoring server.
//
//	subglance --addr :8080 --data-dir /data
//
// It also answers three subcommands, because the shipped image is distroless and
// has no shell to run anything else:
//
//	subglance healthcheck [--addr :8080]
//	subglance backup <path> [--data-dir /data]
//	subglance restore [--from NAME] [--data-dir /data]
//
// and it answers --version without loading configuration at all.
//
// See docs/ARCHITECTURE.md for how the pieces fit together.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/buildinfo"
	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/logging"
	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
	"github.com/frankgraave/subglance/internal/watchdog"
	"github.com/frankgraave/subglance/internal/webui"
)

func main() {
	args := os.Args[1:]

	// Before anything else, including the subcommands: what this binary is
	// must be answerable without a data directory, a free port or a valid
	// configuration. See runVersion.
	if wantsVersion(args) {
		runSubcommand("version", runVersion, nil)
		return
	}

	// Subcommands, and deliberately only these three: the shipped image is
	// distroless with no shell, so a container HEALTHCHECK and an operator
	// taking or restoring a backup have nothing to invoke except this binary.
	// Everything else stays flags-only.
	if len(args) > 0 {
		switch args[0] {
		case "healthcheck":
			runSubcommand("healthcheck", runHealthcheck, args[1:])
			return
		case "backup":
			runSubcommand("backup", runBackup, args[1:])
			return
		case "restore":
			runSubcommand("restore", runRestore, args[1:])
			return
		}
	}

	if err := run(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // the user asked for usage; not a failure
		}
		fmt.Fprintf(os.Stderr, "subglance: %v\n", err)
		os.Exit(1)
	}
}

// runSubcommand applies the exit conventions every subcommand shares: usage
// requests are a clean exit, anything else is a failure named after the
// subcommand so the message says which one failed.
func runSubcommand(name string, fn func([]string, io.Writer) error, args []string) {
	if err := fn(args, os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "subglance %s: %v\n", name, err)
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

	// A binary built without the frontend still serves the full API, which is
	// a supported way to work on the Go side. Say so at startup: the
	// alternative is an operator discovering an empty page and assuming the
	// release is broken.
	if !webui.Available() {
		log.Warn("no dashboard embedded in this binary; the API is unaffected",
			"fix", "make web-install && make web-build && make build")
	}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}

	// Give startup its own bounded context: a database that hangs on open
	// should fail loudly rather than leave the process wedged before it ever
	// serves a request.
	openCtx, cancelOpen := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelOpen()

	// The keys are resolved before Open rather than passed as strings, so a
	// bad path or a short key is reported as a configuration problem. Neither
	// the key nor its length is logged; the only thing said out loud is
	// whether encryption is on, which an operator needs and an attacker
	// already knows from the flag they cannot see.
	secretKey, err := cfg.ResolveSecretKey()
	if err != nil {
		return fmt.Errorf("secret-key: %w", err)
	}
	previousSecretKey, err := cfg.ResolvePreviousSecretKey()
	if err != nil {
		return fmt.Errorf("secret-key-previous: %w", err)
	}

	db, err := store.Open(openCtx, store.Options{
		Path:              cfg.DBPath(),
		SecretKey:         secretKey,
		PreviousSecretKey: previousSecretKey,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database", "error", err)
		}
	}()
	log.Info("database ready", "path", db.Path())
	reportChannelEncryption(log, db.ChannelEncryption())

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

	// A channel URL is operator-supplied text that this process connects
	// to, exactly like a monitor target, so it gets the same SSRF guard.
	//
	// The runner builds its own guard from the same cfg.AllowPrivateTargets
	// value rather than being handed this one. Both derive from one config
	// field, so they cannot disagree, and a Guard holds no state worth
	// sharing — threading one through monitor.Options would add a second
	// way to set the same policy, which is how the two ends quietly drift
	// apart later.
	guard := checker.NewGuard(cfg.AllowPrivateTargets)

	// The notifier is built before the runner so the runner can hand it
	// alerts. It only writes to the outbox on that path; the delivery
	// itself happens in its own goroutine below, which is what keeps a
	// slow webhook from delaying a check.
	notify := notifier.New(notifier.Options{
		DB:          db,
		Log:         log,
		Guard:       guard,
		GroupWindow: cfg.NotifierGroupWindow(),
	})

	runner := monitor.New(monitor.Options{
		DB:                  db,
		Log:                 log,
		AllowPrivateTargets: cfg.AllowPrivateTargets,
		Workers:             cfg.CheckWorkers,
		Bus:                 bus,
		Notify: func(a monitor.Alert) {
			// Enqueue takes a context, but this callback runs on
			// the check path and must not be cancelled by it: an
			// alert that is dropped because its check timed out is
			// the alert that mattered most.
			if err := notify.Enqueue(context.Background(),
				a.Monitor, a.Incident, a.Event, a.At); err != nil {
				log.Error("could not queue alert",
					"monitor", a.Monitor.Name, "error", err)
			}
		},
	})

	// Scheduled backups, when a target is configured. Built before the API so
	// the status endpoint and /metrics read the same runner that uploads.
	backups, err := newBackups(cfg, db, log)
	if err != nil {
		return err
	}

	// The API is constructed before the server so a bad --trusted-proxies is
	// a startup error rather than a limiter that quietly trusts nobody.
	apiSrv, err := api.New(log, db).WithBus(bus).
		WithProber(runner).WithPushRecorder(runner).
		WithChannelTester(notify).
		// The same runner that records checks reports the counters, so
		// /metrics cannot disagree with what actually happened.
		WithMetrics(runner).
		// The same guard the notifier delivers through, so the save-time
		// refusal and the delivery-time refusal cannot disagree about
		// what --allow-private-targets permits. A channel the operator
		// is allowed to deliver to must be a channel they are allowed
		// to save.
		WithTargetGuard(guard).
		WithTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		// The runner doubles as the API's prober, so a manual check uses
		// the same checkers, the same SSRF guard and the same recording
		// path as a scheduled one.
		Handler: apiSrv.Handler(),

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

	// The notifier drains the outbox in its own goroutine. This is the
	// half of notification that is allowed to be slow: it talks to
	// webhooks and mail servers, retries what failed, and none of that can
	// reach back into the check path.
	notifierDone := make(chan struct{})
	go func() {
		defer close(notifierDone)
		notify.Run(ctx)
	}()

	// The watchdog is the only part of SubGlance that talks outbound to
	// something other than a monitored target, so it stays silent unless an
	// operator asked for it.
	dog, err := watchdog.New(watchdog.Options{
		URL:      cfg.WatchdogURL,
		Interval: cfg.WatchdogInterval,
		Log:      log,
		Liveness: func() watchdog.Liveness {
			return watchdog.Liveness{
				ChecksCompleted: runner.ChecksCompleted(),
				Scheduled:       runner.Size(),
			}
		},
	})
	if err != nil {
		return err
	}
	// The API reads the same process-local history the send path writes.
	// Explicit nil reports disabled, not an unavailable diagnostic source.
	apiSrv.WithWatchdog(dog)
	// Waited for on shutdown, so a backup cut short by the signal gets to
	// remove its staging files before the process exits.
	backupsDone := make(chan struct{})
	if backups != nil {
		apiSrv.WithBackups(backups)
		go func() {
			defer close(backupsDone)
			backups.Run(ctx, backupFailureNotice(ctx, notify, log))
		}()
	} else {
		close(backupsDone)
		// Untyped nil: a nil *backup.Backups in the interface would read as
		// configured and then panic on Status.
		apiSrv.WithBackups(nil)
	}
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		dog.Run(ctx)
	}()

	// Expired sessions and stale login-attempt rows accumulate forever
	// otherwise. Cheap deletes, so hourly is plenty.
	go reapExpired(ctx, db, log)

	// Raw heartbeats are the fastest-growing table in the product. Rolling
	// them up keeps history unlimited at a bounded cost.
	go rollupHeartbeats(ctx, db, log, runner, store.RetentionPolicy{
		Raw:    cfg.RawRetention,
		Rollup: cfg.RollupRetention,
	})

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

	// The scheduler gets its own budget, not a share of the HTTP one.
	//
	// Those are two different quantities. --shutdown-timeout is how long an
	// in-flight HTTP request may take; the scheduler's floor is how long the
	// slowest check that may be running right now takes to return, and a
	// per-monitor timeout goes up to 120s against a 15s default. With one
	// budget covering both in series, SIGTERM with a slow monitor mid-check
	// expired the deadline, fell through to the deferred db.Close(), and left
	// workers writing heartbeats against closed pools — an error-log burst
	// and lost beats on every restart, not an edge case. Docker's default
	// --stop-timeout is 10s, so this was the normal deploy path for anyone
	// with one slow monitor.
	//
	// The budget is derived rather than configured: it is a property of the
	// monitor set, which the operator already expressed one monitor at a
	// time. Asking them to keep --shutdown-timeout above their slowest
	// timeout by hand is asking them to maintain the same number twice.
	schedulerBudget := schedulerShutdownBudget(cfg.ShutdownTimeout, runner.MaxCheckTimeout())

	// Wait for in-flight checks so no result is lost mid-write, and do not
	// stop waiting when the budget expires: db.Close() is deferred and runs
	// the moment this function returns.
	awaitScheduler(schedulerDone, schedulerBudget, log)

	// A backup in progress sees ctx cancelled and returns promptly; its
	// deferred cleanup removes the snapshot. Bounded anyway, and whatever
	// is left is removed on the next start.
	select {
	case <-backupsDone:
	case <-shutdownCtx.Done():
		log.Warn("backup did not stop within the shutdown timeout; its staging files are removed on the next start")
	}

	// And for the watchdog's farewell ping, so a planned restart does not
	// read as a crash at the other end.
	select {
	case <-watchdogDone:
	case <-shutdownCtx.Done():
		log.Warn("watchdog did not stop within the shutdown timeout")
	}

	// The notifier last: an alert that fired during shutdown is already in
	// the outbox, so nothing is lost if this times out — the next start
	// picks the delivery up. Waiting anyway means the common case of a
	// clean restart sends what it had rather than deferring it.
	select {
	case <-notifierDone:
	case <-shutdownCtx.Done():
		log.Warn("notifier did not stop within the shutdown timeout; queued alerts will be sent after restart")
	}

	log.Info("shutdown complete")
	return nil
}

// awaitScheduler blocks until the check pipeline has stopped.
//
// It always waits for done, and the budget only decides when the operator is
// told that it is taking a while. That asymmetry is the fix: db.Close() is
// deferred in run and therefore executes the instant run returns, so a wait
// that gives up on a deadline is a wait that closes the database underneath a
// worker still calling RecordHeartbeat. Returning early bought nothing — the
// process cannot exit without closing the pools anyway — and cost an error-log
// burst and a lost heartbeat on every restart with a slow monitor.
//
// This cannot hang indefinitely: every checker applies the monitor's own
// timeout to its context, so a check cannot outlive it, and the scheduler
// returns once the last one has. A supervisor's SIGKILL is the backstop if one
// ever does.
func awaitScheduler(done <-chan struct{}, budget time.Duration, log *slog.Logger) {
	timer := time.NewTimer(budget)
	defer timer.Stop()

	select {
	case <-done:
		return
	case <-timer.C:
		log.Warn("scheduler still has checks in flight after its shutdown budget; "+
			"the database stays open until they finish", "budget", budget)
	}

	<-done
	log.Info("in-flight checks finished; closing the database")
}

// schedulerShutdownBudget is how long the check pipeline is given to finish.
//
// It is the larger of the operator's --shutdown-timeout and the slowest
// scheduled per-monitor timeout plus a margin. The margin covers the work
// after a check returns — recording the heartbeat, advancing the state
// machine, queueing an alert — which the monitor's own timeout does not
// include.
//
// The floor is never lowered below --shutdown-timeout: an operator who raised
// it wanted the longer grace, and this is about the setting being too small
// for the monitor set, never too large.
func schedulerShutdownBudget(configured, maxCheckTimeout time.Duration) time.Duration {
	const recordingMargin = 5 * time.Second

	if maxCheckTimeout <= 0 {
		return configured
	}
	return max(configured, maxCheckTimeout+recordingMargin)
}

// reportChannelEncryption says, once, what state notification channel
// configuration is stored in.
//
// The off case is a warning rather than silence. It is the default, so it is
// the state most instances are in, and a self-hoster who assumed otherwise
// should learn it from their own log rather than from a stranger reading the
// database. It names the flag so the fix does not require finding the
// documentation first.
//
// Nothing here touches the key or a config value — not at debug level either.
// The counts are rows.
func reportChannelEncryption(log *slog.Logger, r store.ChannelEncryptionReport) {
	if !r.Enabled {
		log.Warn("notification channel configuration is stored unencrypted, " +
			"so webhook URLs, bot tokens and SMTP passwords are readable in the database file " +
			"and in every backup of it; set --secret-key to encrypt them")
		if r.Decrypted > 0 {
			log.Warn("decrypted notification channel configuration back to plain text as asked",
				"channels", r.Decrypted)
		}
		return
	}
	log.Info("notification channel configuration is encrypted at rest")
	if r.Encrypted > 0 {
		log.Info("encrypted the configuration of channels that were stored in plain text",
			"channels", r.Encrypted)
	}
	if r.Rewrapped > 0 {
		log.Info("moved notification channel configuration to the new secret key; "+
			"--secret-key-previous is no longer needed and should be removed",
			"channels", r.Rewrapped)
	}
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
func rollupHeartbeats(ctx context.Context, db *store.DB, log *slog.Logger, runner *monitor.Runner, policy store.RetentionPolicy) {
	const interval = 24 * time.Hour

	// Space is only actually returned to the filesystem when the database is
	// in incremental auto-vacuum mode, and that mode can only be turned on by
	// rewriting the file. Doing it here, once, means an existing installation
	// with a bloated file gets the fix too — see EnsureIncrementalVacuum for
	// why that is worth a startup pause, and what happens when the database is
	// too large for one.
	switch mode, err := db.EnsureIncrementalVacuum(ctx); {
	case err != nil:
		log.Error("enable incremental vacuum", "error", err)
	case mode == store.VacuumRebuilt:
		log.Info("rebuilt the database with incremental auto-vacuum enabled, so retention now returns disk space")
	case mode == store.VacuumNeedsRebuild:
		log.Warn("this database is too large to rebuild at startup, so deleted rows will not shrink the file; " +
			"run VACUUM manually once, at a moment when a pause is acceptable")
	}

	run := func() {
		res, err := db.ApplyRetention(ctx, policy)
		if err != nil {
			// A failed pass costs disk, not correctness: the rows are still
			// there and the next pass picks them up. It is counted as well as
			// logged because the pass that fails on a full disk is the one
			// that would have freed the space, and a daily error line is not
			// something anyone is watching for.
			runner.RecordRollupFailure()
			log.Error("heartbeat rollup", "error", err)
			return
		}
		if res.Rollup.Heartbeats > 0 || res.HourlyBuckets > 0 || res.Incidents > 0 {
			log.Info("applied retention",
				"heartbeats", res.Rollup.Heartbeats,
				"buckets", res.Rollup.Buckets,
				"cutoff", res.Rollup.Cutoff,
				"pruned_buckets", res.HourlyBuckets,
				"pruned_incidents", res.Incidents,
				"reclaimed_pages", res.ReclaimedPages)
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
