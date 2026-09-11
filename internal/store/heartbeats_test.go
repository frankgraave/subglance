package store

import (
	"context"
	"testing"
	"time"
)

// The dashboard draws one beat bar per monitor. Doing that with ListHeartbeats
// costs a query per monitor, so RecentHeartbeatsForAll replaces the N+1 with a
// single windowed query. The risk that buys is a partitioning bug: one
// monitor's beats bleeding into another's, or the wrong end of the series
// being kept. Both are checked here.

// seedBeats records n heartbeats for a monitor, one second apart starting at
// base, and returns them oldest first.
func seedBeats(t *testing.T, db *DB, monitorID int64, base time.Time, n int) []Heartbeat {
	t.Helper()
	ctx := context.Background()

	out := make([]Heartbeat, 0, n)
	for i := range n {
		hb := Heartbeat{
			MonitorID: monitorID,
			TS:        base.Add(time.Duration(i) * time.Second).UTC(),
			OK:        i%2 == 0,
			LatencyMS: 10 + i,
		}
		if err := db.RecordHeartbeat(ctx, hb); err != nil {
			t.Fatalf("RecordHeartbeat monitor=%d i=%d: %v", monitorID, i, err)
		}
		out = append(out, hb)
	}
	return out
}

func newTestMonitor(t *testing.T, db *DB, name string) Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), Monitor{
		Name: name, Type: "http", Target: "https://" + name + ".example.com",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor %s: %v", name, err)
	}
	return m
}

func TestRecentHeartbeatsForAllKeepsNewestPerMonitor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	base := time.Unix(1_700_000_000, 0).UTC()
	first := newTestMonitor(t, db, "first")
	second := newTestMonitor(t, db, "second")
	never := newTestMonitor(t, db, "never")

	// Both monitors get more beats than we ask for, at disjoint times, so a
	// partitioning mistake shows up as either a wrong count or a foreign
	// timestamp rather than as a coincidence.
	firstBeats := seedBeats(t, db, first.ID, base, 10)
	secondBeats := seedBeats(t, db, second.ID, base.Add(time.Hour), 7)

	got, err := db.RecentHeartbeatsForAll(ctx, 3)
	if err != nil {
		t.Fatalf("RecentHeartbeatsForAll: %v", err)
	}

	if _, ok := got[never.ID]; ok {
		t.Errorf("monitor %d has never been checked but appears in the map", never.ID)
	}
	if len(got) != 2 {
		t.Fatalf("got %d monitors in the map, want 2 (keys: %v)", len(got), got)
	}

	cases := []struct {
		name string
		id   int64
		all  []Heartbeat
	}{
		{"first", first.ID, firstBeats},
		{"second", second.ID, secondBeats},
	}
	for _, tc := range cases {
		beats := got[tc.id]
		if len(beats) != 3 {
			t.Errorf("%s: got %d heartbeats, want 3", tc.name, len(beats))
			continue
		}
		// Newest first, same contract as ListHeartbeats.
		want := []Heartbeat{
			tc.all[len(tc.all)-1],
			tc.all[len(tc.all)-2],
			tc.all[len(tc.all)-3],
		}
		for i, hb := range beats {
			if hb.MonitorID != tc.id {
				t.Errorf("%s[%d]: monitor_id = %d, want %d (beats leaked across monitors)",
					tc.name, i, hb.MonitorID, tc.id)
			}
			if !hb.TS.Equal(want[i].TS) {
				t.Errorf("%s[%d]: ts = %s, want %s", tc.name, i, hb.TS, want[i].TS)
			}
			if hb.LatencyMS != want[i].LatencyMS {
				t.Errorf("%s[%d]: latency_ms = %d, want %d",
					tc.name, i, hb.LatencyMS, want[i].LatencyMS)
			}
			if hb.OK != want[i].OK {
				t.Errorf("%s[%d]: ok = %v, want %v", tc.name, i, hb.OK, want[i].OK)
			}
		}
	}
}

func TestRecentHeartbeatsForAllMatchesListHeartbeats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m := newTestMonitor(t, db, "single")
	seedBeats(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), 5)

	want, err := db.ListHeartbeats(ctx, m.ID, 4)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	got, err := db.RecentHeartbeatsForAll(ctx, 4)
	if err != nil {
		t.Fatalf("RecentHeartbeatsForAll: %v", err)
	}

	beats := got[m.ID]
	if len(beats) != len(want) {
		t.Fatalf("got %d heartbeats, ListHeartbeats returned %d", len(beats), len(want))
	}
	for i := range want {
		if beats[i].ID != want[i].ID || !beats[i].TS.Equal(want[i].TS) {
			t.Errorf("beat %d: got id=%d ts=%s, want id=%d ts=%s",
				i, beats[i].ID, beats[i].TS, want[i].ID, want[i].TS)
		}
	}
}

func TestRecentHeartbeatsForAllClampsPerMonitor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m := newTestMonitor(t, db, "clamped")
	seedBeats(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), 4)

	// A non-positive value means "use the default", not "return nothing".
	for _, perMonitor := range []int{0, -1} {
		got, err := db.RecentHeartbeatsForAll(ctx, perMonitor)
		if err != nil {
			t.Fatalf("RecentHeartbeatsForAll(%d): %v", perMonitor, err)
		}
		if len(got[m.ID]) != 4 {
			t.Errorf("perMonitor=%d: got %d heartbeats, want all 4",
				perMonitor, len(got[m.ID]))
		}
	}
}

// The upper clamp is the one that protects the server: without it a caller
// could ask for the entire heartbeats table in one response. Observing it
// needs more rows than the cap, hence the separate, slower test.
func TestRecentHeartbeatsForAllClampsToMaximum(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m := newTestMonitor(t, db, "busy")
	seedBeats(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), maxHeartbeatsPerQuery+5)

	for _, perMonitor := range []int{maxHeartbeatsPerQuery + 1, 100_000} {
		got, err := db.RecentHeartbeatsForAll(ctx, perMonitor)
		if err != nil {
			t.Fatalf("RecentHeartbeatsForAll(%d): %v", perMonitor, err)
		}
		if len(got[m.ID]) != maxHeartbeatsPerQuery {
			t.Errorf("perMonitor=%d: got %d heartbeats, want the cap of %d",
				perMonitor, len(got[m.ID]), maxHeartbeatsPerQuery)
		}
	}
}
