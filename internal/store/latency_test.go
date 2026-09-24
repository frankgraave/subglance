package store

import (
	"context"
	"testing"
	"time"
)

func recordLatency(t *testing.T, db *DB, hb Heartbeat) {
	t.Helper()
	if err := db.RecordHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
}

// The series groups raw checks into steps, weights the average by samples,
// and leaves out steps that hold no checks instead of reporting them as 0ms.
func TestLatencySeriesGroupsRawChecksIntoSteps(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "latency")
	other := seedMonitor(t, db, "other")

	base := time.Unix(1_800_000_000, 0).UTC().Truncate(time.Hour)
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(1 * time.Minute), OK: true, Assessment: "up", LatencyMS: 100})
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(2 * time.Minute), OK: true, Assessment: "up", LatencyMS: 300})
	// A failed check with no latency counts as a check and as down, but must
	// not drag the average towards zero.
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(3 * time.Minute), Assessment: "down"})
	// Down during maintenance is not confirmed downtime.
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(4 * time.Minute), Assessment: "down", Maintenance: true})
	// The next step is empty; the one after holds a single check.
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(2*time.Hour + time.Minute), OK: true, Assessment: "up", LatencyMS: 50})
	// Another monitor's checks and checks outside the range stay out.
	recordLatency(t, db, Heartbeat{MonitorID: other, TS: base.Add(time.Minute), OK: true, Assessment: "up", LatencyMS: 9999})
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(-time.Minute), OK: true, Assessment: "up", LatencyMS: 9999})
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(3 * time.Hour), OK: true, Assessment: "up", LatencyMS: 9999})

	got, err := db.LatencySeries(ctx, id, base, base.Add(3*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("LatencySeries: %v", err)
	}
	want := []LatencyPoint{
		{Start: base, Samples: 2, Checks: 4, Down: 1, AvgMS: 200, MinMS: 100, MaxMS: 300},
		{Start: base.Add(2 * time.Hour), Samples: 1, Checks: 1, AvgMS: 50, MinMS: 50, MaxMS: 50},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A step that holds only failed checks is a point with no latency, not a gap:
// the chart has to be able to say "it was checked and it was down".
func TestLatencySeriesKeepsStepsWithoutLatency(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "down-only")
	base := time.Unix(1_800_000_000, 0).UTC().Truncate(time.Hour)
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: base.Add(time.Minute), Assessment: "down"})

	got, err := db.LatencySeries(context.Background(), id, base, base.Add(time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("LatencySeries: %v", err)
	}
	if len(got) != 1 || got[0].Samples != 0 || got[0].Checks != 1 || got[0].Down != 1 || got[0].AvgMS != 0 {
		t.Fatalf("got %+v, want one latency-less down point", got)
	}
}

// History older than raw retention lives in hourly buckets. The series must
// read both sides and merge a step that holds some of each, weighting the two
// averages by their own sample counts.
func TestLatencySeriesSpansTheRollupBoundary(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "rolled")

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour).Truncate(24 * time.Hour)
	// Three checks at 100ms in the first hour of the day; they get rolled up.
	for i := range 3 {
		recordLatency(t, db, Heartbeat{MonitorID: id, TS: old.Add(time.Duration(i) * time.Minute), OK: true, Assessment: "up", LatencyMS: 100})
	}
	if _, err := db.rollupAt(ctx, now, 24*time.Hour); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var raw int
	if err := db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM heartbeats WHERE monitor_id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 0 {
		t.Fatalf("%d raw heartbeats survived the rollup; the test would not cross the boundary", raw)
	}
	// One raw check at 500ms later on the same day, inserted after the rollup,
	// as a late arrival would be. Same one-day step as the rolled-up hour.
	recordLatency(t, db, Heartbeat{MonitorID: id, TS: old.Add(5 * time.Hour), OK: true, Assessment: "up", LatencyMS: 500})

	got, err := db.LatencySeries(ctx, id, old, old.Add(24*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatalf("LatencySeries: %v", err)
	}
	want := LatencyPoint{Start: old, Samples: 4, Checks: 4, AvgMS: 200, MinMS: 100, MaxMS: 500}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
}

func TestLatencySeriesRejectsSubSecondStep(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "step")
	now := time.Now()
	if _, err := db.LatencySeries(context.Background(), id, now.Add(-time.Hour), now, time.Millisecond); err == nil {
		t.Fatal("a sub-second step was accepted")
	}
}
