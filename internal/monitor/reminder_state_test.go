package monitor

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

func TestRunnerExposesReminderSuppressionFromActualEngine(t *testing.T) {
	r := New(Options{DB: testDB(t), Log: quietLogger(), FlapWindow: 10 * time.Minute, FlapThreshold: 3})
	source, ok := any(r).(interface{ Flapping(int64) bool })
	if !ok {
		t.Fatal("the runner passed to api.WithProber lacks the read-only reminder suppression facet")
	}
	if source.Flapping(1) || source.Flapping(2) {
		t.Fatal("unknown monitors are not flapping")
	}
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for n, healthy := range []bool{false, true, false} {
		r.engine.Observe(state.Observation{MonitorID: 1, OK: healthy, At: at.Add(time.Duration(n) * time.Second), FailureThreshold: 1})
	}
	if !source.Flapping(1) || source.Flapping(2) {
		t.Fatal("read-only facet must expose the actual monitor's suppression")
	}
	r.engine.Forget(1)
	if source.Flapping(1) {
		t.Fatal("forgotten engine state must not leave stale suppression in the API")
	}
}
