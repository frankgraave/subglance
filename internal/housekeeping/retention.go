// Package housekeeping runs the daily retention pass: when it runs, catching
// up a pass that was missed while the server was down, and making sure two
// passes never run at once.
//
// What a pass does lives in the store (ApplyRetention). This package only
// decides when, and keeps the record of how it went.
package housekeeping

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Triggers, as recorded on a pass.
const (
	TriggerSchedule = "schedule"
	// TriggerStartup is a pass run at startup because the scheduled one was
	// missed, or has never run.
	TriggerStartup = "startup"
	TriggerManual  = "manual"
)

// ErrPassRunning is returned by Run when a pass is already in progress.
var ErrPassRunning = errors.New("a retention pass is already running")

// recheckEvery bounds how long the scheduler sleeps before looking at the
// clock and the configured time again. It is what makes a time saved on the
// settings page apply without a restart, and what keeps a suspended machine
// or a stepped clock from postponing a pass by a whole day.
const recheckEvery = 15 * time.Minute

// Store is what the scheduler needs from the database.
type Store interface {
	ResolveRetention(ctx context.Context, pins store.RetentionPins) (store.EffectiveRetention, error)
	ApplyRetention(ctx context.Context, p store.RetentionPolicy) (store.RetentionResult, error)
	ResolveRetentionRunAt(ctx context.Context, pin *store.RetentionRunAtPin) (store.RetentionRunAt, error)
	ResolveMaxDatabaseSize(ctx context.Context, pin *store.MaxDatabaseSizePin) (store.MaxDatabaseSize, error)
	LastRetentionPass(ctx context.Context) (*store.RetentionPass, error)
	SaveRetentionPass(ctx context.Context, p store.RetentionPass) error
}

// Options configures a Retention scheduler.
type Options struct {
	Store Store
	// Pins are the retention windows fixed by flags or variables.
	Pins store.RetentionPins
	// RunAtPin is the time of day fixed by a flag or variable, or nil.
	RunAtPin *store.RetentionRunAtPin
	// MaxSizePin is the database size limit fixed by a flag or variable,
	// or nil.
	MaxSizePin *store.MaxDatabaseSizePin
	// Location is the time zone the time of day is read in. Nil means the
	// server's local zone.
	Location *time.Location
	Log      *slog.Logger
	// OnFailure is called once for every pass that fails, for /metrics.
	OnFailure func()
	// OnSizeLimit is called with what the database size limit did, once
	// for every pass that ran with a limit set, for /metrics.
	OnSizeLimit func(store.SizeCapResult)

	// Now and Wait are the clock, injectable for tests. Wait returns false
	// when ctx ended before d elapsed.
	Now  func() time.Time
	Wait func(ctx context.Context, d time.Duration) bool
}

// Retention schedules and runs retention passes.
type Retention struct {
	opts    Options
	running atomic.Bool
}

// New returns a scheduler. It does nothing until Start or Run is called.
func New(opts Options) *Retention {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.OnFailure == nil {
		opts.OnFailure = func() {}
	}
	if opts.OnSizeLimit == nil {
		opts.OnSizeLimit = func(store.SizeCapResult) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Wait == nil {
		opts.Wait = sleep
	}
	return &Retention{opts: opts}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Running reports whether a pass is in progress.
func (r *Retention) Running() bool { return r.running.Load() }

// Run performs one pass now and records it. It returns ErrPassRunning, and
// does nothing, when another pass has not finished yet: two passes would
// only queue behind each other on the single writer connection, and the
// second would find nothing left to do.
//
// A failed pass is returned as a record with Error set, not as an error:
// the failure is the outcome being recorded. The error return is only for a
// pass that did not happen.
func (r *Retention) Run(ctx context.Context, trigger string) (store.RetentionPass, error) {
	if !r.running.CompareAndSwap(false, true) {
		return store.RetentionPass{}, ErrPassRunning
	}
	defer r.running.Store(false)
	return r.pass(ctx, trigger), nil
}

// Begin starts a pass in the background and returns as soon as it has been
// claimed, or ErrPassRunning when another pass has not finished yet. It is
// what "run now" on the settings page calls: a pass over a large table takes
// longer than a request should, and the outcome is recorded like any other
// pass, so the caller reads it back from LastRetentionPass.
//
// The pass is claimed before Begin returns, so Running is true from then on
// and a second Begin or Run is refused rather than queued.
func (r *Retention) Begin(ctx context.Context, trigger string) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrPassRunning
	}
	go func() {
		defer r.running.Store(false)
		r.pass(ctx, trigger)
	}()
	return nil
}

// pass performs one claimed pass and records it.
func (r *Retention) pass(ctx context.Context, trigger string) store.RetentionPass {
	log := r.opts.Log
	started := r.opts.Now()
	pass := store.RetentionPass{StartedAt: started, Trigger: trigger}

	res, err := r.apply(ctx)
	pass.Duration = r.opts.Now().Sub(started)
	pass.Heartbeats = res.Rollup.Heartbeats
	pass.HourlyBuckets = res.HourlyBuckets
	pass.Incidents = res.Incidents
	pass.Deliveries = res.Deliveries
	pass.FreedBytes = res.ReclaimedBytes
	pass.SizeCap = res.SizeCap
	if c := res.SizeCap; c != nil {
		r.opts.OnSizeLimit(*c)
		if c.Intervened() || c.AtFloor {
			r.reportSizeLimit(trigger, *c)
		}
	}
	if err != nil {
		// A failed pass costs disk, not correctness: the rows are still
		// there and the next pass picks them up. It is counted as well as
		// logged because the pass that fails on a full disk is the one
		// that would have freed the space, and a daily error line is not
		// something anyone is watching for.
		pass.Error = err.Error()
		r.opts.OnFailure()
		log.Error("retention pass", "trigger", trigger, "error", err)
	} else if res.Rollup.Heartbeats > 0 || res.HourlyBuckets > 0 || res.Incidents > 0 || res.Deliveries > 0 {
		log.Info("applied retention",
			"trigger", trigger,
			"heartbeats", res.Rollup.Heartbeats,
			"buckets", res.Rollup.Buckets,
			"cutoff", res.Rollup.Cutoff,
			"pruned_buckets", res.HourlyBuckets,
			"pruned_incidents", res.Incidents,
			"pruned_deliveries", res.Deliveries,
			"reclaimed_pages", res.ReclaimedPages)
	}

	// Recorded under a context of its own: a pass cut short by shutdown is
	// exactly the one the next start needs to know did not finish.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.opts.Store.SaveRetentionPass(saveCtx, pass); err != nil {
		log.Error("record retention pass", "error", err)
	}
	return pass
}

func (r *Retention) apply(ctx context.Context) (store.RetentionResult, error) {
	eff, err := r.opts.Store.ResolveRetention(ctx, r.opts.Pins)
	if err != nil {
		return store.RetentionResult{}, err
	}
	policy := eff.Policy()
	// A limit that cannot be read fails the pass rather than running it
	// without one: on a small disk the limit is the part that matters, and
	// a failed pass is counted and retried, where a silently unlimited one
	// is neither.
	limit, err := r.opts.Store.ResolveMaxDatabaseSize(ctx, r.opts.MaxSizePin)
	if err != nil {
		return store.RetentionResult{}, err
	}
	policy.MaxBytes = limit.Value
	return r.opts.Store.ApplyRetention(ctx, policy)
}

// reportSizeLimit makes a pass that went past the windows visible. Removing
// history the operator's windows would have kept is only acceptable if it is
// said out loud, so it is a warning, not an info line, and it is counted.
func (r *Retention) reportSizeLimit(trigger string, c store.SizeCapResult) {
	args := []any{
		"trigger", trigger,
		"limit", store.FormatByteSize(c.Limit),
		"before", store.FormatByteSize(c.Before),
		"after", store.FormatByteSize(c.After),
		"heartbeats_rolled_up", c.Heartbeats,
		"hourly_buckets_deleted", c.HourlyBuckets,
	}
	if !c.RawSince.IsZero() {
		args = append(args, "raw_since", c.RawSince)
	}
	if !c.HourlySince.IsZero() {
		args = append(args, "hourly_since", c.HourlySince)
	}
	if c.AtFloor {
		r.opts.Log.Warn("the database is still over its size limit: raw heartbeats are down to the last day, "+
			"no older summaries are left, and incidents are never removed to meet it", args...)
		return
	}
	r.opts.Log.Warn("removed history beyond the retention windows to keep the database under its size limit", args...)
}

// runAt resolves the configured time of day, falling back to the default
// when the stored value cannot be read: a pass at the default time is better
// than no pass at all.
func (r *Retention) runAt(ctx context.Context) store.ClockTime {
	rt, err := r.opts.Store.ResolveRetentionRunAt(ctx, r.opts.RunAtPin)
	if err != nil {
		r.opts.Log.Error("resolve retention run time, using the default",
			"default", store.DefaultRetentionRunAt.String(), "error", err)
		return store.DefaultRetentionRunAt
	}
	return rt.Value
}

// Start runs the schedule until ctx ends.
//
// At startup it first catches up: if no pass has completed since the most
// recent scheduled time, the pass runs now. That covers a server that was
// down at the scheduled time, a first start, and a server restarted more
// often than once a day, which would otherwise never reach its scheduled
// time at all — exactly the instance whose database grows without anyone
// noticing.
//
// After that it runs the pass each day at the configured time. Changing the
// time never triggers a pass by itself: moving it from 03:30 to 08:00 at
// 09:00 waits for tomorrow's 08:00 rather than running a second pass today.
func (r *Retention) Start(ctx context.Context) {
	loc := r.opts.Location
	at := r.runAt(ctx)
	handled := at.Previous(r.opts.Now(), loc)

	if r.missed(ctx, handled) {
		if _, err := r.Run(ctx, TriggerStartup); err != nil {
			r.opts.Log.Warn("skipped the catch-up retention pass", "reason", err)
		}
	}

	for {
		now := r.opts.Now()
		wait := min(at.Next(now, loc).Sub(now), recheckEvery)
		if !r.opts.Wait(ctx, wait) {
			return
		}

		now = r.opts.Now()
		if next := r.runAt(ctx); next != at {
			at = next
			if prev := at.Previous(now, loc); prev.After(handled) {
				handled = prev
			}
			continue
		}
		due := at.Previous(now, loc)
		if !due.After(handled) {
			continue
		}
		handled = due
		if _, err := r.Run(ctx, TriggerSchedule); err != nil {
			// A manual pass is running; it does the same work.
			r.opts.Log.Info("scheduled retention pass skipped", "reason", err)
		}
	}
}

// missed reports whether no pass has completed since due.
//
// A pass counts by when it finished, not when it started: one that began a
// minute before due and ended after it has done the work due asked for, and
// running it again on the next start would only repeat it.
func (r *Retention) missed(ctx context.Context, due time.Time) bool {
	last, err := r.opts.Store.LastRetentionPass(ctx)
	if err != nil {
		r.opts.Log.Error("read the last retention pass", "error", err)
		return true
	}
	return last == nil || !last.Succeeded() || last.StartedAt.Add(last.Duration).Before(due)
}
