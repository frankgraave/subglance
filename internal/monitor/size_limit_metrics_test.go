package monitor

import (
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The size-limit readings on /metrics: a counter of passes that removed
// history beyond the windows, and whether the last pass was left over the
// limit. A pass that was already under the limit moves neither.
func TestRecordSizeLimitCountsInterventionsAndTracksTheFloor(t *testing.T) {
	r := New(Options{Log: quietLogger()})

	r.RecordSizeLimit(store.SizeCapResult{Limit: 1 << 30, Before: 1 << 20, After: 1 << 20})
	if m := r.Metrics(); m.SizeLimitPasses != 0 || m.SizeLimitUnmet {
		t.Fatalf("under the limit: %d passes, unmet %v; want 0, false", m.SizeLimitPasses, m.SizeLimitUnmet)
	}

	r.RecordSizeLimit(store.SizeCapResult{Heartbeats: 10, HourlyBuckets: 2, AtFloor: true})
	if m := r.Metrics(); m.SizeLimitPasses != 1 || !m.SizeLimitUnmet {
		t.Fatalf("at the floor: %d passes, unmet %v; want 1, true", m.SizeLimitPasses, m.SizeLimitUnmet)
	}

	// The next pass meets the limit: the gauge clears, the counter stays.
	r.RecordSizeLimit(store.SizeCapResult{Heartbeats: 4})
	if m := r.Metrics(); m.SizeLimitPasses != 2 || m.SizeLimitUnmet {
		t.Errorf("met again: %d passes, unmet %v; want 2, false", m.SizeLimitPasses, m.SizeLimitUnmet)
	}
}
