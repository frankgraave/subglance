// Package scheduler decides when each monitor runs and executes the checks.
//
// # Why not one goroutine per monitor
//
// The obvious design — a goroutine with a ticker per monitor — falls apart in
// two ways at the scale this product targets. Five hundred monitors means five
// hundred goroutines each holding a timer, and worse, monitors created together
// share an interval and therefore fire together: five hundred simultaneous
// outbound requests, four times a minute, with nothing in between.
//
// Instead there is one loop over a time-ordered queue and a bounded worker
// pool. Memory is proportional to the queue, not to concurrency, and the number
// of checks running at once has a ceiling regardless of how many monitors
// exist.
//
// # Jitter
//
// Each monitor's next run is spread by up to 10% of its interval. Without it,
// monitors added in the same batch stay locked in lockstep forever, producing
// a thundering herd against whatever they point at — which, for an agency
// monitoring forty sites on the same host, means being the cause of the outage
// you are watching for.
package scheduler

import (
	"container/heap"
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// firstCheckDelay is how long a never-checked monitor waits before its first
// run. Short enough that someone who just saved a monitor sees the result while
// still looking at the screen, long enough that the standard jitter can still
// spread a bulk import.
const firstCheckDelay = 2 * time.Second

// catchUpSpread caps how far apart overdue monitors are spread when they enter
// the queue together, at startup after downtime or when many are resumed at
// once. Their own jitter window would be the natural spread, but for a daily
// monitor that is 2.4 hours, and a check that was due before the start should
// run while the person who restarted it is still looking. Thirty seconds is
// enough to keep a few hundred overdue monitors from firing in one burst; the
// worker pool bounds the rest.
const catchUpSpread = 30 * time.Second

// reloadSettle is how long the scheduler waits after a ReloadSoon before it
// re-reads the monitor set. Every request that arrives in that time is served
// by the same read, so a bulk pause sent as one request per monitor, or an
// import that writes a hundred monitors, costs a few reads rather than one per
// monitor. It is short next to firstCheckDelay, so a monitor saved through the
// API is still checked while the person who saved it is looking.
const reloadSettle = 200 * time.Millisecond

// Job is one monitor as the scheduler sees it.
type Job struct {
	// Failing puts the monitor on the failure cadence (see Cadence) without
	// changing its configured interval. It is set from the first failed check
	// that is not yet confirmed until the incident closes: Warning, Down and
	// Recovering in the state engine's terms.
	Failing  bool
	Monitor  checker.Monitor
	Interval time.Duration

	// NeverChecked marks a monitor that has no result yet, so its first check
	// should happen promptly instead of one full interval from now.
	//
	// Without this, adding a monitor with a 6-hour interval means waiting six
	// hours to discover the URL was mistyped. The moment right after saving a
	// monitor is exactly when someone is watching to see whether it works.
	NeverChecked bool

	// LastCheck is when the monitor's newest recorded check ran, zero when
	// there is none or it could not be read. A monitor entering the queue,
	// at startup or when it is resumed, is due one cadence after this rather
	// than one cadence after the moment it entered: otherwise every restart
	// moved every monitor a full interval out, and a daily monitor on a host
	// that restarts daily was never checked at all. A monitor already in the
	// queue keeps its scheduled time and ignores this.
	LastCheck time.Time
}

// Outcome pairs a check result with the monitor that produced it.
type Outcome struct {
	Monitor checker.Monitor
	Result  checker.Result

	// Aborted marks a check that was cut short by shutdown rather than by
	// anything the target did.
	//
	// Every checker turns a cancelled context into a failed Result, and the
	// scheduler waits for in-flight checks before stopping — so without this
	// flag the consumer cannot tell "the site is down" from "we are exiting",
	// and every restart would manufacture a burst of outages.
	Aborted bool
}

// Registry supplies the set of monitors to run.
//
// The scheduler reloads through this interface rather than holding a database
// handle, which keeps it testable and keeps the storage layer out of the
// scheduling logic.
type Registry interface {
	// Jobs returns every enabled monitor.
	Jobs(ctx context.Context) ([]Job, error)
}

// RegistryFunc adapts a function to Registry.
type RegistryFunc func(ctx context.Context) ([]Job, error)

// Jobs implements Registry.
func (f RegistryFunc) Jobs(ctx context.Context) ([]Job, error) { return f(ctx) }

// Options configures New.
type Options struct {
	// Registry supplies monitors. Required.
	Registry Registry

	// Checkers maps a monitor type to its implementation. Required.
	Checkers map[checker.Type]checker.Checker

	// Workers caps concurrent checks. Zero means auto: sized from CPU count
	// and from how many monitors are scheduled, recomputed on every reload
	// and bounded to [minWorkers, maxWorkers]. See autoWorkerCount for why
	// the monitor count belongs in that figure.
	//
	// A non-zero value is taken literally and never adjusted: an operator who
	// names a number owns it.
	Workers int

	// OnResult receives every completed check. It runs on a worker goroutine
	// and must not block for long; persisting and alerting belong behind a
	// queue. Required.
	OnResult func(Outcome)

	// ReloadInterval controls how often the monitor set is re-read.
	// Zero means 30 seconds.
	ReloadInterval time.Duration

	// JitterFraction spreads scheduled times by up to this fraction of the
	// interval. Zero means 0.1; negative disables jitter (tests only).
	JitterFraction float64

	// Now allows tests to control time. Zero means time.Now.
	Now func() time.Time

	Log *slog.Logger
}

// Scheduler runs monitors on their intervals.
type Scheduler struct {
	registry Registry
	checkers map[checker.Type]checker.Checker
	onResult func(Outcome)

	workers        int
	reloadInterval time.Duration
	jitterFraction float64
	now            func() time.Time
	log            *slog.Logger

	// autoWorkers records that Options.Workers was left at zero, so the pool
	// is ours to size and to re-size when the monitor set changes. An
	// operator who named a number owns it: --check-workers is a ceiling they
	// chose, and silently exceeding it would make the flag a suggestion.
	autoWorkers bool

	// workerTarget is the pool size reload would like, published for the Run
	// loop to act on. Growth happens on the loop's own goroutine and nowhere
	// else: wg.Wait runs there too, and a WaitGroup.Add racing its Wait is a
	// panic, not a slow shutdown. Reload runs on its caller's goroutine, so
	// spawning workers inside it would be exactly that race.
	workerTarget int

	// skipped counts checks dropped because the previous run of the same
	// monitor was still going. It is the number that says the pool is behind:
	// a log line per skip is invisible at default level, a counter is not.
	skipped atomic.Uint64

	// busy counts workers currently inside a check. Workers is the pool's
	// size and QueueDepth its backlog; busy is the third reading that makes
	// those two legible: a full pool with an empty queue is a pool that is
	// exactly keeping up, and one with a backlog is a pool that is not.
	busy atomic.Int64

	mu    sync.Mutex
	queue *jobHeap
	// failingSeq counts SetFailing calls, under mu. See queueItem.failingSeq.
	failingSeq uint64
	// inFlight guards against a slow monitor being scheduled twice. A check
	// that takes longer than its interval must not stack up.
	inFlight map[int64]bool

	// wake lets a reload interrupt the sleep when the new head of the queue is
	// due sooner than the old one.
	wake chan struct{}

	// reloadSoon carries ReloadSoon's requests to the Run loop. One slot is
	// enough: a request that finds it full is already covered by the read
	// that the pending one will cause.
	reloadSoon chan struct{}

	// tasks carries work to the pool. Buffered so a brief burst does not stall
	// the scheduling loop.
	tasks chan task

	wg sync.WaitGroup
}

type task struct {
	job Job
}

// New builds a Scheduler. It does not start until Run is called.
func New(opts Options) *Scheduler {
	autoWorkers := opts.Workers <= 0
	if autoWorkers {
		opts.Workers = autoWorkerCount(runtime.GOMAXPROCS(0), 0)
	}
	if opts.ReloadInterval <= 0 {
		opts.ReloadInterval = 30 * time.Second
	}
	if opts.JitterFraction == 0 {
		opts.JitterFraction = 0.1
	}
	if opts.JitterFraction < 0 {
		opts.JitterFraction = 0
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	q := &jobHeap{}
	heap.Init(q)

	return &Scheduler{
		registry:       opts.Registry,
		checkers:       opts.Checkers,
		onResult:       opts.OnResult,
		workers:        opts.Workers,
		autoWorkers:    autoWorkers,
		reloadInterval: opts.ReloadInterval,
		jitterFraction: opts.JitterFraction,
		now:            opts.Now,
		log:            opts.Log,
		queue:          q,
		inFlight:       map[int64]bool{},
		wake:           make(chan struct{}, 1),
		reloadSoon:     make(chan struct{}, 1),
		// The task buffer is sized for maxWorkers rather than for the
		// starting pool, because the pool grows and the channel cannot be
		// resized afterwards. Its capacity is queue depth, not concurrency:
		// a buffered task holds a Job value, not a goroutine.
		tasks: make(chan task, maxWorkers*2),
	}
}

// Worker-pool bounds.
//
// minWorkers keeps a single-core box from serialising checks; maxWorkers is
// the ceiling that makes the pool a pool. It is a real limit and not a
// formality: every running check holds an open connection and whatever the
// response body cost, and SubGlance's target deployment is a small VPS, so an
// auto-sized pool that grows with the monitor count has to stop somewhere.
const (
	minWorkers = 8
	maxWorkers = 128
)

// monitorsPerWorker is the sizing rule behind the auto worker count.
//
// The pool only matters when checks stop returning: a healthy check finishes
// in milliseconds and one worker serves hundreds of monitors. During a broad
// outage — the moment this tool exists for — every check instead holds its
// worker for the full timeout. With N monitors on interval I and timeout T,
// keeping up needs N*T/I workers. The defaults are I=60s and T=10s, so N/6;
// this uses N/4 so that a monitor configured with a longer timeout, or an
// interval shorter than a minute, still has headroom.
//
// Without it, 200 monitors on a 2-vCPU box got 8 workers, cleared about 48
// checks a minute against 200 due, and pushed detection latency for the
// still-healthy monitors out to minutes. The tool degraded worst exactly when
// it was needed most.
const monitorsPerWorker = 4

// autoWorkerCount is the auto pool size for a given CPU count and number of
// scheduled monitors, bounded to [minWorkers, maxWorkers].
//
// It is a pure function so the sizing rule can be tested at monitor counts
// nobody wants to instantiate, and so the rule lives in one place rather than
// once in New and once in reload.
func autoWorkerCount(procs, scheduled int) int {
	byCPU := procs * 4
	// Round up: 5 monitors needing 1.25 workers get 2, not 1.
	byMonitors := (scheduled + monitorsPerWorker - 1) / monitorsPerWorker
	return min(max(byCPU, byMonitors, minWorkers), maxWorkers)
}

// Run schedules monitors until ctx is cancelled.
//
// It blocks, and returns only once every in-flight check has finished — so a
// caller that cancels on SIGTERM gets a clean stop with no results lost
// mid-write.
func (s *Scheduler) Run(ctx context.Context) error {
	s.log.Info("scheduler starting", "workers", s.workers, "reload_interval", s.reloadInterval)

	for range s.workers {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	// Load once up front so the first checks do not wait a full reload cycle.
	if err := s.reload(ctx); err != nil {
		s.log.Error("initial monitor load failed", "error", err)
	}
	s.growPool(ctx)

	reloadTicker := time.NewTicker(s.reloadInterval)
	defer reloadTicker.Stop()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	// settle is armed by the first ReloadSoon after a read and fires once,
	// reloadSettle later; nil (never ready) while no request is waiting.
	var settle <-chan time.Time

	for {
		wait := s.timeUntilNext()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)

		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping, waiting for in-flight checks")
			close(s.tasks)
			s.wg.Wait()
			s.log.Info("scheduler stopped")
			return ctx.Err()

		case <-reloadTicker.C:
			s.reloadAndGrow(ctx)

		case <-s.reloadSoon:
			// Wait out the settle time rather than reading now: the request
			// is usually the first of several.
			if settle == nil {
				settle = time.After(reloadSettle)
			}

		case <-settle:
			settle = nil
			s.reloadAndGrow(ctx)

		case <-s.wake:
			// A reload or SetFailing changed the queue; recompute the wait,
			// and take on any workers a reload made on another goroutine
			// (Reload, from a test) decided the new monitor set needs.
			s.growPool(ctx)

		case <-timer.C:
			s.dispatchDue(ctx)
		}
	}
}

// reloadAndGrow re-reads the monitor set on Run's goroutine and takes on any
// workers the new set needs. A failed read is logged and otherwise ignored:
// the queue keeps what it had, and the next tick tries again.
func (s *Scheduler) reloadAndGrow(ctx context.Context) {
	if err := s.reload(ctx); err != nil {
		// A reload racing a shutdown fails by design; logging it as an
		// error trains operators to ignore the error level.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.log.Error("monitor reload failed", "error", err)
		}
	}
	s.growPool(ctx)
}

// growPool starts however many workers reload has asked for since the last
// call. It must only ever be called from Run's goroutine; see workerTarget.
//
// The pool only grows. Shrinking it would mean signalling chosen workers to
// exit, and a worker is only interruptible between checks — so a pool sized
// for an outage would stay large until that outage ended anyway. Idle workers
// are a blocked receive on one channel, which is the cheapest thing in the
// runtime; the memory that matters is the in-flight checks, and those are
// bounded by the ceiling in autoWorkerCount.
func (s *Scheduler) growPool(ctx context.Context) {
	s.mu.Lock()
	want := s.workerTarget
	have := s.workers
	if want > have {
		s.workers = want
	}
	s.mu.Unlock()

	if want <= have {
		return
	}
	for range want - have {
		s.wg.Add(1)
		go s.worker(ctx)
	}
	s.log.Info("worker pool resized for the monitor set",
		"workers", want, "was", have)
}

// timeUntilNext reports how long to sleep before the next job is due.
func (s *Scheduler) timeUntilNext() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.queue.Len() == 0 {
		return time.Hour // nothing to do; a reload will wake us
	}
	wait := time.Until((*s.queue)[0].next)
	if wait < 0 {
		return 0
	}
	return wait
}

// dispatchDue sends every due job to the worker pool and reschedules it.
func (s *Scheduler) dispatchDue(ctx context.Context) {
	now := s.now()

	for {
		s.mu.Lock()
		if s.queue.Len() == 0 || (*s.queue)[0].next.After(now) {
			s.mu.Unlock()
			return
		}
		item := heap.Pop(s.queue).(*queueItem)

		// A check still running from the previous round is skipped rather than
		// queued again: stacking checks on a slow endpoint would multiply load
		// on a service that is already struggling.
		skip := s.inFlight[item.job.Monitor.ID]
		if !skip {
			s.inFlight[item.job.Monitor.ID] = true
		}
		item.next = s.nextRun(now, item.job.cadence())
		heap.Push(s.queue, item)
		job := item.job
		s.mu.Unlock()

		if skip {
			s.skipped.Add(1)
			s.log.Warn("skipping check, previous run still active",
				"monitor_id", job.Monitor.ID,
				"monitor", job.Monitor.Name,
				"interval", job.Interval)
			continue
		}

		select {
		case s.tasks <- task{job: job}:
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.inFlight, job.Monitor.ID)
			s.mu.Unlock()
			return
		}
	}
}

// worker consumes tasks until the channel closes.
func (s *Scheduler) worker(ctx context.Context) {
	defer s.wg.Done()

	for t := range s.tasks {
		s.busy.Add(1)
		s.runCheck(ctx, t.job)
		s.busy.Add(-1)
	}
}

// runCheck executes one check and reports the outcome.
func (s *Scheduler) runCheck(ctx context.Context, job Job) {
	defer func() {
		s.mu.Lock()
		delete(s.inFlight, job.Monitor.ID)
		s.mu.Unlock()

		// A panic in one check must not take down the worker, let alone the
		// process: a monitoring tool that dies because of one bad endpoint is
		// worse than useless.
		if v := recover(); v != nil {
			s.log.Error("panic during check",
				"monitor_id", job.Monitor.ID,
				"monitor", job.Monitor.Name,
				"panic", v)
			s.report(Outcome{
				Monitor: job.Monitor,
				Result: checker.Result{
					OK:        false,
					Kind:      checker.FailInternal,
					Error:     "internal error during check",
					CheckedAt: s.now(),
				},
			})
		}
	}()

	c, ok := s.checkers[job.Monitor.Type]
	if !ok {
		s.log.Error("no checker for monitor type",
			"monitor_id", job.Monitor.ID, "type", job.Monitor.Type)
		s.report(Outcome{
			Monitor: job.Monitor,
			Result: checker.Result{
				OK:        false,
				Kind:      checker.FailInternal,
				Error:     "unsupported monitor type " + string(job.Monitor.Type),
				CheckedAt: s.now(),
			},
		})
		return
	}

	res := c.Check(ctx, job.Monitor)

	// Distinguish "the check failed" from "we were told to stop". The
	// checkers cannot make this call themselves: from inside a probe a
	// cancelled context looks the same either way.
	aborted := !res.OK && ctx.Err() != nil

	s.report(Outcome{Monitor: job.Monitor, Result: res, Aborted: aborted})
}

func (s *Scheduler) report(o Outcome) {
	if s.onResult == nil {
		return
	}
	s.onResult(o)
}

// ReloadSoon asks the Run loop to re-read the monitor set within
// reloadSettle, rather than at the next reload tick.
//
// The API asks for it, through the monitor runner, after every write that
// changes what is scheduled: creating, editing, deleting, pausing or resuming
// a monitor, an import and a reset. Without it a change waited for the tick,
// up to ReloadInterval: a new monitor's first check came half a minute after
// it was saved, and a deleted or paused one went on being checked, and could
// alert, for as long.
//
// It never blocks and cannot fail, so it is safe on a request's path: the read
// happens on Run's goroutine, where a failure is logged and the tick remains
// the fallback. Requests that arrive while one is waiting are served by the
// same read. Before Run starts, a request waits for it.
func (s *Scheduler) ReloadSoon() {
	select {
	case s.reloadSoon <- struct{}{}:
	default:
	}
}

// Reload re-reads the monitor set now, on the caller's goroutine. Run does
// this on its own at start and on every tick; it is exported for tests that
// drive a scheduler without running it. Code that serves a request calls
// ReloadSoon instead, which neither waits for the read nor returns its error.
func (s *Scheduler) Reload(ctx context.Context) error {
	return s.reload(ctx)
}

// reload re-reads the monitor set, adding new monitors, dropping removed ones
// and updating changed intervals.
//
// Existing monitors keep their scheduled time, so editing one monitor's name
// does not reset the whole schedule and cause a synchronised burst of checks.
func (s *Scheduler) reload(ctx context.Context) error {
	// The registry is read without the lock, so a check can finish and
	// call SetFailing while it is being read. Remember where the sequence
	// stood: a SetFailing after this point is newer than the snapshot.
	s.mu.Lock()
	readFrom := s.failingSeq
	s.mu.Unlock()

	jobs, err := s.registry.Jobs(ctx)
	if err != nil {
		return err
	}

	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	existing := make(map[int64]*queueItem, s.queue.Len())
	for _, item := range *s.queue {
		existing[item.job.Monitor.ID] = item
	}

	next := &jobHeap{}
	var added, updated int

	for _, job := range jobs {
		if job.Interval <= 0 {
			s.log.Warn("monitor has a non-positive interval, skipping",
				"monitor_id", job.Monitor.ID, "interval", job.Interval)
			continue
		}

		if old, ok := existing[job.Monitor.ID]; ok {
			// An outcome reported after the registry was read wins over
			// the snapshot: otherwise a monitor that failed during the
			// read would go back to its full interval until its next
			// check, delaying the confirmation the failure cadence is for.
			if old.failingSeq > readFrom {
				job.Failing = old.job.Failing
			}
			item := &queueItem{job: job, next: old.next, failingSeq: old.failingSeq}
			// A shortened interval must take effect now rather than after the
			// old, longer wait has elapsed.
			if job.cadence() != old.job.cadence() {
				item.next = s.nextRun(now, job.cadence())
				updated++
			}
			*next = append(*next, item)
			continue
		}

		// New to the queue: a monitor just created or resumed, or every
		// monitor at startup. firstRun places it by its own last check, with
		// jitter, so importing fifty monitors does not produce fifty
		// simultaneous requests and a restart does not reset the schedule.
		*next = append(*next, &queueItem{job: job, next: s.firstRun(now, job)})
		added++
	}

	removed := len(existing) + added - len(*next)
	heap.Init(next)
	s.queue = next

	// Re-size the pool for the monitor set we now hold. The whole point of
	// recomputing on reload rather than once at startup is that the count
	// that matters is the one after a bulk import, not the one at boot.
	if s.autoWorkers {
		if want := autoWorkerCount(runtime.GOMAXPROCS(0), next.Len()); want > s.workerTarget {
			s.workerTarget = want
		}
	}

	if added > 0 || updated > 0 || removed > 0 {
		s.log.Info("monitor set reloaded",
			"total", len(*next), "added", added, "updated", updated, "removed", removed)
	}

	// Wake the loop: the new head may be due sooner than the old one.
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// nextRun returns when a job with this interval should run next, with jitter.
func (s *Scheduler) nextRun(from time.Time, interval time.Duration) time.Time {
	return from.Add(interval + spread(s.jitterWindow(interval)))
}

// firstRun returns when a job that is not yet in the queue runs first: at
// startup, when a monitor is created, and when one is resumed.
//
//   - Never checked: in firstCheckDelay, so someone who just saved a monitor
//     sees a result while still looking at the screen.
//   - Checked before: one cadence after its last check, with the usual
//     jitter. The schedule is the monitor's, not the process's: a restart
//     that lands 40 seconds into a one-minute interval leaves 20 seconds,
//     not another minute.
//   - Overdue (the start came after the check was due, or the pause lasted
//     longer than the interval): in firstCheckDelay, spread over at most
//     catchUpSpread so a restart after downtime does not fire every monitor
//     in the same instant.
//   - A last check later than now means the clock moved back. Counting from
//     it would postpone the check by however far the clock moved, so the
//     wait is capped at one cadence from now, which is what a restart did
//     before last checks were read.
//   - Last check unknown: one cadence from now.
func (s *Scheduler) firstRun(now time.Time, job Job) time.Time {
	cadence := job.cadence()
	if job.NeverChecked {
		return s.nextRun(now, firstCheckDelay)
	}
	if job.LastCheck.IsZero() {
		return s.nextRun(now, cadence)
	}
	due := job.LastCheck.Add(cadence)
	if due.After(now.Add(cadence)) {
		return s.nextRun(now, cadence)
	}
	if due.After(now.Add(firstCheckDelay)) {
		return due.Add(spread(s.jitterWindow(cadence)))
	}
	return now.Add(firstCheckDelay + spread(min(s.jitterWindow(cadence), catchUpSpread)))
}

// jitterWindow is how much later than its due time a job with this cadence
// may run: the jitter fraction of the cadence, or nothing when jitter is off.
func (s *Scheduler) jitterWindow(cadence time.Duration) time.Duration {
	if s.jitterFraction <= 0 {
		return 0
	}
	return time.Duration(float64(cadence) * s.jitterFraction)
}

// spread returns a random delay in [0, window).
func spread(window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	//nolint:gosec // scheduling jitter, not a security decision
	return time.Duration(rand.Float64() * float64(window))
}

// Size reports how many monitors are scheduled. For tests and diagnostics.
func (s *Scheduler) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.Len()
}

// Workers reports the current pool size. It moves when the auto-sized pool
// grows with the monitor set, which is why it is a method and not the
// Options value the caller passed.
func (s *Scheduler) Workers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workers
}

// SkippedChecks reports how many checks were dropped because the previous run
// of the same monitor had not finished.
//
// This is the saturation signal. A rising count means checks are taking longer
// than their interval, which during a broad outage is the pool falling behind
// and detection latency growing for monitors that are still fine.
func (s *Scheduler) SkippedChecks() uint64 { return s.skipped.Load() }

// QueueDepth reports how many dispatched checks are waiting for a worker.
//
// Zero is the healthy reading: a pool that keeps up hands each task straight
// to a blocked worker. A depth that stays high is the pool saturated, and it
// is the reading that moves before the skip counter does.
func (s *Scheduler) QueueDepth() int { return len(s.tasks) }

// Busy reports how many workers are running a check right now.
//
// runCheck recovers its own panics, so the decrement after it always runs and
// a crashing checker cannot leave a worker counted as busy forever.
func (s *Scheduler) Busy() int { return int(s.busy.Load()) }

// MaxCheckTimeout reports the longest per-monitor timeout currently scheduled.
//
// It exists for shutdown. A worker is only interruptible between checks, so
// the time the scheduler needs to stop is bounded by the slowest check that
// may be in flight — which is a property of the monitor set, not of the
// --shutdown-timeout an operator picked for HTTP requests. Reading it from
// the live queue rather than from config is what keeps the two from drifting
// when a monitor's timeout is edited.
//
// Zero when nothing is scheduled, which the caller must treat as "no
// constraint" rather than "stop immediately".
func (s *Scheduler) MaxCheckTimeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	var maxTimeout time.Duration
	for _, item := range *s.queue {
		if t := item.job.Monitor.Timeout; t > maxTimeout {
			maxTimeout = t
		}
	}
	return maxTimeout
}

// CheckerFor returns the implementation registered for a check type.
//
// It exists so an on-demand check can reuse exactly the checkers the schedule
// runs, rather than building a second set that could drift out of step — a
// "check now" button that probes differently from the schedule would be worse
// than no button at all. The map itself is not exposed: it is written once in
// New and read concurrently by the workers, so handing it out would invite a
// data race.
func (s *Scheduler) CheckerFor(t checker.Type) (checker.Checker, bool) {
	c, ok := s.checkers[t]
	return c, ok
}

// queueItem is one monitor waiting for its turn.
type queueItem struct {
	job  Job
	next time.Time
	// failingSeq is the scheduler's failingSeq when SetFailing last
	// reported on this monitor; zero if it never has. Reload compares it
	// with the value it saw before reading the registry, so a check
	// outcome that lands while the registry is being read is not undone
	// by the older snapshot.
	failingSeq uint64
	index      int
}

// jobHeap is a min-heap ordered by next run time, so finding the next monitor
// to run is O(1) and rescheduling is O(log n) — with a sorted slice, every
// reschedule would be a linear insert.
type jobHeap []*queueItem

func (h jobHeap) Len() int           { return len(h) }
func (h jobHeap) Less(i, j int) bool { return h[i].next.Before(h[j].next) }

func (h jobHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *jobHeap) Push(x any) {
	item := x.(*queueItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// FailureRecheck caps a failing monitor's interval. It is a cap, not the
// effective wait: Cadence applies the checker type's floor and the timeout
// margin after it, so a domain monitor or one with a timeout over 48 seconds
// is rechecked less often than this.
const FailureRecheck = time.Minute

// Cadence is how long a monitor waits between checks: its interval, or, while
// it is failing, the shorter of the interval and FailureRecheck.
// Already-fast monitors stay fast. A failure uses the same queue, jitter and
// bounded workers as ordinary checks.
//
// Failing starts at the first failed check, not at confirmation. The failure
// threshold (retries) is evidence that a failure is real, and it should not
// also be a wait: rechecked at a five-minute interval, three retries alerted
// ten minutes after the first failure, while the one-minute pace began only
// once the alert had gone out. Rechecking an unconfirmed failure every minute
// confirms it in (retries - 1) minutes, and a pass in between puts the monitor
// back on its interval. The cadence then holds until the incident closes, so
// the passes that close it come at the pace the failures did.
//
// A type with a floor of its own never goes below it, failing or not: a
// domain monitor's registry does not answer faster for an expired domain, and
// asking it every minute is what gets an instance rate-limited.
//
// Nor does a monitor come due again before its slowest check could have
// finished, with the same 20% margin the API keeps between a timeout and its
// interval. A monitor is never run twice at once, so a dispatch inside a
// check that is still running is skipped and logged as a warning; for a
// monitor failing on a 90-second timeout, the one-minute failure cadence would
// otherwise skip every other recheck for the length of the outage, and a
// monitor stored before the API refused a timeout longer than its interval
// would skip most of its checks for good.
func Cadence(typ checker.Type, interval, timeout time.Duration, failing bool) time.Duration {
	if failing {
		interval = min(interval, FailureRecheck)
	}
	return max(interval, checker.MinInterval(typ), timeout*5/4)
}

func (j Job) cadence() time.Duration {
	return Cadence(j.Monitor.Type, j.Interval, j.Monitor.Timeout, j.Failing)
}

// SetFailing applies a failing or passing check's cadence immediately, before
// the next reload. A monitor that starts failing is brought forward only when
// that is sooner than its scheduled check; one that stops failing goes back to
// its interval from now. Repeated results do not postpone a scheduled check.
func (s *Scheduler) SetFailing(id int64, failing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range *s.queue {
		if item.job.Monitor.ID != id {
			continue
		}
		s.failingSeq++
		item.failingSeq = s.failingSeq
		if item.job.Failing == failing {
			return
		}
		previousCadence := item.job.cadence()
		item.job.Failing = failing
		if item.job.cadence() == previousCadence {
			return // Already-fast monitors keep their existing due time.
		}
		next := s.nextRun(s.now(), item.job.cadence())
		if !failing || next.Before(item.next) {
			item.next = next
		}
		heap.Init(s.queue)
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return
	}
}
