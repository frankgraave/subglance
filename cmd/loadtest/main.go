//go:build manual

// Command loadtest measures scheduler behaviour at scale.
//
// It answers the two questions the architecture doc makes claims about: does
// memory stay flat with hundreds of monitors, and does the worker pool bound
// concurrency. Run:
//
//	go run -tags manual ./cmd/loadtest
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
)

// stubChecker simulates a fast endpoint without touching the network, so the
// numbers measure the scheduler rather than the internet.
type stubChecker struct {
	checks     atomic.Int64
	running    atomic.Int32
	maxRunning atomic.Int32
}

func (s *stubChecker) Check(ctx context.Context, m checker.Monitor) checker.Result {
	cur := s.running.Add(1)
	for {
		peak := s.maxRunning.Load()
		if cur <= peak || s.maxRunning.CompareAndSwap(peak, cur) {
			break
		}
	}
	defer s.running.Add(-1)

	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
	}
	s.checks.Add(1)
	return checker.Result{OK: true, Latency: 20 * time.Millisecond, StatusCode: 200, CheckedAt: time.Now()}
}

func memMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Alloc) / 1024 / 1024
}

func main() {
	const (
		monitors = 500
		interval = 5 * time.Second
		duration = 30 * time.Second
		workers  = 32
	)

	stub := &stubChecker{}

	jobs := make([]scheduler.Job, 0, monitors)
	for i := int64(1); i <= monitors; i++ {
		jobs = append(jobs, scheduler.Job{
			Monitor: checker.Monitor{
				ID:     i,
				Name:   fmt.Sprintf("monitor-%d", i),
				Type:   checker.TypeHTTP,
				Target: "https://example.com",
			},
			Interval: interval,
		})
	}

	var results atomic.Int64
	s := scheduler.New(scheduler.Options{
		Registry: scheduler.RegistryFunc(func(context.Context) ([]scheduler.Job, error) {
			return jobs, nil
		}),
		Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: stub},
		OnResult: func(scheduler.Outcome) { results.Add(1) },
		Workers:  workers,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	runtime.GC()
	before := memMB()

	fmt.Printf("monitors=%d interval=%s workers=%d duration=%s\n\n",
		monitors, interval, workers, duration)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	start := time.Now()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Printf("  t=%-5s checks=%-7d heap=%.1f MB goroutines=%d peak_concurrent=%d\n",
					time.Since(start).Truncate(time.Second),
					stub.checks.Load(), memMB(), runtime.NumGoroutine(), stub.maxRunning.Load())
			case <-ctx.Done():
				return
			}
		}
	}()

	_ = s.Run(ctx)
	elapsed := time.Since(start)

	runtime.GC()
	after := memMB()

	fmt.Printf("\nRESULTS\n")
	fmt.Printf("  checks completed    %d\n", stub.checks.Load())
	fmt.Printf("  results delivered   %d\n", results.Load())
	fmt.Printf("  checks/second       %.1f\n", float64(stub.checks.Load())/elapsed.Seconds())
	fmt.Printf("  peak concurrency    %d (worker limit %d)\n", stub.maxRunning.Load(), workers)
	fmt.Printf("  heap before/after   %.1f MB / %.1f MB\n", before, after)
	fmt.Printf("  goroutines at end   %d\n", runtime.NumGoroutine())

	expected := float64(monitors) * elapsed.Seconds() / interval.Seconds()
	fmt.Printf("  expected ~%.0f checks, got %d (%.0f%%)\n",
		expected, stub.checks.Load(), float64(stub.checks.Load())/expected*100)

	if stub.maxRunning.Load() > workers {
		fmt.Printf("\nFAIL: concurrency exceeded the worker limit\n")
	} else {
		fmt.Printf("\nOK: concurrency stayed within the worker limit\n")
	}
}
