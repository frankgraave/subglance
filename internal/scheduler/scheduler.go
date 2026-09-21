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

// Job is one monitor as the scheduler sees it.
type Job struct {
	// Down enables recovery checks without changing the configured interval.
	Down     bool
	Monitor  checker.Monitor
	Interval time.Duration

	// NeverChecked marks a monitor that has no result yet, so its first check
	// should happen promptly instead of one full interval from now.
	//
	// Without this, adding a monitor with a 6-hour interval means waiting six
	// hours to discover the URL was mistyped. The moment right after saving a
	// monitor is exactly when someone is watching to see whether it works.
	NeverChecked bool
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
	// panic, not a slow shutdown. Reload is callable from the API, so
	// spawning workers inside it would be exactly that race.
	workerTarget int

	// skipped counts checks dropped because the previous run of the same
	// monitor was still going. It is the number that says the pool is behind:
	// a log line per skip is invisible at default level, a counter is not.
	skipped atomic.Uint64

	mu    sync.Mutex
	queue *jobHeap
	// inFlight guards against a slow monitor being scheduled twice. A check
	// that takes longer than its interval must not stack up.
	inFlight map[int64]bool

	// wake lets a reload interrupt the sleep when the new head of the queue is
	// due sooner than the old one.
	wake chan struct{}

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
			if err := s.reload(ctx); err != nil {
				// A reload racing a shutdown fails by design; logging it as an
				// error trains operators to ignore the error level.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				s.log.Error("monitor reload failed", "error", err)
			}
			s.growPool(ctx)

		case <-s.wake:
			// A reload changed the queue; recompute the wait — and take on any
			// workers that reload decided the new monitor set needs. This is
			// the path an API-triggered Reload arrives by, so it is what makes
			// a bulk import size the pool without waiting for the next tick.
			s.growPool(ctx)

		case <-timer.C:
			s.dispatchDue(ctx)
		}
	}
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
		s.runCheck(ctx, t.job)
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

// Reload re-reads the monitor set immediately, rather than waiting for the
// next reload tick.
//
// The API calls this after creating, deleting, pausing or resuming a monitor,
// so a change the user just made takes effect now instead of up to
// ReloadInterval later. Waiting would make the UI feel broken: you delete a
// monitor and it keeps checking for another thirty seconds.
func (s *Scheduler) Reload(ctx context.Context) error {
	return s.reload(ctx)
}

// reload re-reads the monitor set, adding new monitors, dropping removed ones
// and updating changed intervals.
//
// Existing monitors keep their scheduled time, so editing one monitor's name
// does not reset the whole schedule and cause a synchronised burst of checks.
func (s *Scheduler) reload(ctx context.Context) error {
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
			item := &queueItem{job: job, next: old.next}
			// A shortened interval must take effect now rather than after the
			// old, longer wait has elapsed.
			if job.cadence() != old.job.cadence() {
				item.next = s.nextRun(now, job.cadence())
				updated++
			}
			*next = append(*next, item)
			continue
		}

		// New monitors get a jittered first run rather than firing instantly,
		// so importing fifty monitors does not produce fifty simultaneous
		// requests.
		//
		// A monitor that has never been checked is the exception: it gets a
		// short jittered delay instead of a full interval, so someone who just
		// saved a monitor sees a result while still looking at the screen. The
		// jitter still applies, so a bulk import stays spread out.
		first := s.nextRun(now, job.cadence())
		if job.NeverChecked {
			first = s.nextRun(now, firstCheckDelay)
		}
		*next = append(*next, &queueItem{job: job, next: first})
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
	if s.jitterFraction <= 0 {
		return from.Add(interval)
	}
	maxJitter := float64(interval) * s.jitterFraction
	//nolint:gosec // scheduling jitter, not a security decision
	offset := time.Duration(rand.Float64() * maxJitter)
	return from.Add(interval + offset)
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
	job   Job
	next  time.Time
	index int
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

// cadence keeps already-fast monitors fast. Recovery uses the same queue,
// jitter and bounded workers as ordinary checks.
func (j Job) cadence() time.Duration {
	if j.Down {
		return min(j.Interval, time.Minute)
	}
	return j.Interval
}

// SetDown applies a confirmed transition immediately, before the next reload.
// Repeated results do not postpone an already scheduled check.
func (s *Scheduler) SetDown(id int64, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range *s.queue {
		if item.job.Monitor.ID != id || item.job.Down == down {
			continue
		}
		previousCadence := item.job.cadence()
		item.job.Down = down
		if item.job.cadence() == previousCadence {
			return // Already-fast monitors keep their existing due time.
		}
		next := s.nextRun(s.now(), item.job.cadence())
		if !down || next.Before(item.next) {
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
