package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// LastCheckTimes places each monitor in the scheduler's queue after a
// restart, so it must name every monitor's newest heartbeat, leave out a
// monitor with none, and keep one monitor's beats out of another's answer.
func TestLastCheckTimesNamesEachMonitorsNewestBeat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	base := time.Unix(1_700_000_000, 0).UTC()
	first := newTestMonitor(t, db, "first")
	second := newTestMonitor(t, db, "second")
	never := newTestMonitor(t, db, "never")
	seedBeats(t, db, first.ID, base, 5)
	seedBeats(t, db, second.ID, base.Add(time.Hour), 3)

	got, err := db.LastCheckTimes(ctx)
	if err != nil {
		t.Fatalf("LastCheckTimes: %v", err)
	}
	if want := base.Add(4 * time.Second); !got[first.ID].Equal(want) {
		t.Errorf("first: last check %v, want %v", got[first.ID], want)
	}
	if want := base.Add(time.Hour + 2*time.Second); !got[second.ID].Equal(want) {
		t.Errorf("second: last check %v, want %v", got[second.ID], want)
	}
	if ts, ok := got[never.ID]; ok {
		t.Errorf("a monitor without heartbeats is in the map at %v; it must be absent so it counts as never checked", ts)
	}
	if len(got) != 2 {
		t.Errorf("map holds %d monitors, want 2: %v", len(got), got)
	}
}

// The query runs on every reload, every 30 seconds. It must read one index
// entry per monitor, not walk the heartbeats table, which holds a week of raw
// checks per monitor by default.
func TestLastCheckTimesReadsTheIndexPerMonitor(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.Reader.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+lastCheckTimesQuery)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "SEARCH heartbeats") {
		t.Errorf("heartbeats are not searched by monitor; plan:\n%s", joined)
	}
	if strings.Contains(joined, "SCAN heartbeats") {
		t.Errorf("heartbeats are scanned whole on every reload; plan:\n%s", joined)
	}
}
