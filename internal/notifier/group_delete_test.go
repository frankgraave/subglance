package notifier

import (
	"context"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// A batch is filed under the monitor that started it. Deleting that monitor
// while the batch waits for company must not lose the news about the rest.
func TestGroupOutlivesItsFirstMonitor(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 90*time.Second)
	ch := groupChannel(t, db, "ops")

	first := groupMonitor(t, db, "retired-host", ch.ID)
	other := groupMonitor(t, db, "api", ch.ID)
	firstInc := openIncident(t, db, first.ID, clock.Now(), "timeout")
	otherInc := openIncident(t, db, other.ID, clock.Now(), "timeout")
	if err := n.Enqueue(context.Background(), first, firstInc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue %s: %v", first.Name, err)
	}
	clock.Advance(2 * time.Second)
	if err := n.Enqueue(context.Background(), other, otherInc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue %s: %v", other.Name, err)
	}

	if err := db.DeleteMonitor(context.Background(), first.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())

	rows, err := db.DueDeliveries(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 10)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued %d deliveries, want the grouped alert that still names api", len(rows))
	}
	if rows[0].MonitorID != other.ID || rows[0].IncidentID != otherInc.ID {
		t.Errorf("delivery filed under monitor %d, incident %d; want api's (%d, %d)",
			rows[0].MonitorID, rows[0].IncidentID, other.ID, otherInc.ID)
	}
	alert := decodeOnlyDelivery(t, db)
	if len(alert.GroupedNames) != 2 {
		t.Errorf("grouped names = %v, want both monitors as they failed", alert.GroupedNames)
	}
}

// A batch whose monitors were all deleted has nobody left to speak for.
func TestGroupOfDeletedMonitorsIsDropped(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 90*time.Second)
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "retired-host", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")
	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.DeleteMonitor(context.Background(), m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())
	if got := countDeliveries(t, db); got != 0 {
		t.Errorf("queued %d deliveries for a deleted monitor, want none", got)
	}
}
