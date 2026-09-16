package monitor

import (
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/store"
)

// outcomeFor builds a scheduler.Outcome as the scheduler would produce it,
// so tests can exercise the recording path without waiting on real intervals.
func outcomeFor(m store.Monitor, target string, ok bool) scheduler.Outcome {
	cm := toCheckerMonitor(m)
	cm.Target = target

	res := checker.Result{
		OK:        ok,
		Latency:   5 * time.Millisecond,
		CheckedAt: time.Now(),
	}
	if ok {
		res.StatusCode = 200
	} else {
		res.StatusCode = 500
		res.Kind = checker.FailStatus
		res.Error = "unexpected status 500"
	}

	return scheduler.Outcome{Monitor: cm, Result: res}
}

// at stamps an outcome with a specific check time. Tests that depend on the
// flap window use it to move time forward without sleeping: the engine expires
// recorded flips against the result's timestamp, never against the wall clock.
func at(o scheduler.Outcome, ts time.Time) scheduler.Outcome {
	o.Result.CheckedAt = ts
	return o
}
