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
