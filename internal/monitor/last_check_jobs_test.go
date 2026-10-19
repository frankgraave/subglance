package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The runner hands the scheduler each monitor's last recorded check, which is
// what keeps a monitor's place across a restart; a monitor with none is
// marked never checked and carries no time.
func TestJobsCarryTheLastRecordedCheck(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	create := func(name string) store.Monitor {
		t.Helper()
		m, err := db.CreateMonitor(ctx, store.Monitor{
			Name: name, Type: "http", Target: "https://example.com/" + name,
			IntervalS: 3600, TimeoutS: 5, Enabled: true,
		})
		if err != nil {
			t.Fatalf("CreateMonitor %s: %v", name, err)
		}
		return m
	}
	checked := create("checked")
	fresh := create("fresh")
	last := time.Now().Add(-50 * time.Minute).Truncate(time.Second).UTC()
	for _, ts := range []time.Time{last.Add(-time.Hour), last} {
		if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: checked.ID, TS: ts, OK: true}); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}
	}

	r := New(Options{DB: db, Log: quietLogger()})
	jobs, err := r.jobs(ctx)
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	for _, j := range jobs {
		switch j.Monitor.ID {
		case checked.ID:
			if !j.LastCheck.Equal(last) || j.NeverChecked {
				t.Errorf("checked monitor: LastCheck %v, NeverChecked %v; want %v and false", j.LastCheck, j.NeverChecked, last)
			}
		case fresh.ID:
			if !j.LastCheck.IsZero() || !j.NeverChecked {
				t.Errorf("fresh monitor: LastCheck %v, NeverChecked %v; want zero and true", j.LastCheck, j.NeverChecked)
			}
		}
	}
}
