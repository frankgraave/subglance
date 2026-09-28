package store

import (
	"context"
	"testing"
	"time"
)

// Failures recorded while the host itself was offline did not advance the
// failure streak, so a restart must not count them into it either: a
// threshold-3 monitor with one real failure and ten local-network ones would
// otherwise confirm on its very next failure after the restart.
func TestCountFailedHeartbeatsSkipsLocalNetworkFailures(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "streak")
	start := time.Now().Add(-time.Hour).Truncate(time.Second)

	beats := []Heartbeat{{MonitorID: id, TS: start, FailureKind: "connection", Assessment: "warning"}}
	for i := range 10 {
		beats = append(beats, Heartbeat{MonitorID: id, TS: start.Add(time.Duration(i+1) * time.Minute),
			FailureKind: FailureKindLocalNetwork, Assessment: "warning"})
	}
	for _, hb := range beats {
		if err := db.RecordHeartbeat(ctx, hb); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.CountFailedHeartbeatsSince(ctx, id, start, 20)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("streak = %d, want 1 (the local-network failures do not count)", n)
	}
}
