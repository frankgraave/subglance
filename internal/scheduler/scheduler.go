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
	"log/slog"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// Job is one monitor as the scheduler sees it.
type Job struct {
	Monitor  checker.Monitor
	Interval time.Duration
}

// Outcome pairs a check result with the monitor that produced it.
type Outcome struct {
	Monitor checker.Monitor
	Result  checker.Result
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

	// Workers caps concurrent checks. Zero means 4x GOMAXPROCS, bounded to
	// [8, 128] — checks are IO-bound, so more workers than cores is right,
	// but unbounded growth would defeat the purpose.
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
	if opts.Workers <= 0 {
		opts.Workers = min(max(runtime.GOMAXPROCS(0)*4, 8), 128)
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
		reloadInterval: opts.ReloadInterval,
		jitterFraction: opts.JitterFraction,
		now:            opts.Now,
		log:            opts.Log,
		queue:          q,
		inFlight:       map[int64]bool{},
		wake:           make(chan struct{}, 1),
		tasks:          make(chan task, opts.Workers*2),
	}
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
				s.log.Error("monitor reload failed", "error", err)
			}

		case <-s.wake:
			// A reload changed the queue; recompute the wait.

		case <-timer.C:
			s.dispatchDue(ctx)
		}
	}
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
		item.next = s.nextRun(now, item.job.Interval)
		heap.Push(s.queue, item)
		s.mu.Unlock()

		if skip {
			s.log.Warn("skipping check, previous run still active",
				"monitor_id", item.job.Monitor.ID,
				"monitor", item.job.Monitor.Name,
				"interval", item.job.Interval)
			continue
		}

		select {
		case s.tasks <- task{job: item.job}:
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.inFlight, item.job.Monitor.ID)
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
	s.report(Outcome{Monitor: job.Monitor, Result: res})
}

func (s *Scheduler) report(o Outcome) {
	if s.onResult == nil {
		return
	}
	s.onResult(o)
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
			if job.Interval != old.job.Interval {
				item.next = s.nextRun(now, job.Interval)
				updated++
			}
			*next = append(*next, item)
			continue
		}

		// New monitors get a jittered first run rather than firing instantly,
		// so importing fifty monitors does not produce fifty simultaneous
		// requests.
		*next = append(*next, &queueItem{job: job, next: s.nextRun(now, job.Interval)})
		added++
	}

	removed := len(existing) + added - len(*next)
	heap.Init(next)
	s.queue = next

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
