package notifier

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// The notifier stops before the code that feeds it does. After the signal the
// scheduler finishes its in-flight checks, the HTTP server drains push reports
// and a reminder sweep completes, and each can raise an alert. The incident
// behind it is already marked confirmed, reminded or resolved by then, so the
// next start does not raise it again: if it is not in the outbox, it is gone.

// runAndStop runs n until its loop has returned, as a shutdown does.
func runAndStop(t *testing.T, n *Notifier) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

// queuedRows returns every pending outbox row with its payload, oldest first.
func queuedRows(t *testing.T, db *store.DB) []store.Delivery {
	t.Helper()
	rows, err := db.DueDeliveries(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 500)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return rows
}

// TestAlertRaisedAfterStopReachesTheOutbox: an alert enqueued once Run has
// returned is written straight away, whatever kind of news it is. Before the
// fix it went into a batch that nothing would ever close.
func TestAlertRaisedAfterStopReachesTheOutbox(t *testing.T) {
	t.Parallel()

	for _, event := range []state.Event{
		state.EventIncidentConfirmed,
		state.EventIncidentReminder,
		state.EventIncidentResolved,
	} {
		t.Run(string(event), func(t *testing.T) {
			t.Parallel()

			db := groupDB(t)
			clock := newTestClock()
			// A window far longer than any shutdown: only the stop can
			// make this alert durable.
			n := groupNotifier(t, db, clock, time.Hour)
			ch := groupChannel(t, db, "ops")
			m := groupMonitor(t, db, "api", ch.ID)
			inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

			runAndStop(t, n)

			if err := n.Enqueue(context.Background(), m, inc, event, clock.Now()); err != nil {
				t.Fatalf("enqueue: %v", err)
			}

			rows := queuedRows(t, db)
			if len(rows) != 1 {
				t.Fatalf("an alert raised after the notifier stopped was not written: %d outbox rows", len(rows))
			}
			if rows[0].Event != string(event) {
				t.Fatalf("outbox row says %q, want %q", rows[0].Event, event)
			}
			if held := len(n.batches); held != 0 {
				t.Fatalf("%d batches still held in memory after the notifier stopped", held)
			}
		})
	}
}

// TestStopWritesHeldBatchesBeforeLaterAlerts: what was waiting in a window goes
// out as it would have, grouped, and an alert raised after the stop follows it
// as a message of its own. A recovery written ahead of the alert it closes
// would tell a channel an outage ended before telling it one began.
func TestStopWritesHeldBatchesBeforeLaterAlerts(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, time.Hour)
	ch := groupChannel(t, db, "ops")

	api := groupMonitor(t, db, "api", ch.ID)
	apiInc := openIncident(t, db, api.ID, clock.Now(), "timeout")
	web := groupMonitor(t, db, "web", ch.ID)
	webInc := openIncident(t, db, web.ID, clock.Now(), "timeout")

	for _, a := range []struct {
		m   store.Monitor
		inc store.Incident
	}{{api, apiInc}, {web, webInc}} {
		if err := n.Enqueue(context.Background(), a.m, a.inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
			t.Fatalf("enqueue %s: %v", a.m.Name, err)
		}
	}
	if got := countDeliveries(t, db); got != 0 {
		t.Fatalf("precondition: the window should still hold both alerts, got %d rows", got)
	}

	runAndStop(t, n)

	clock.Advance(10 * time.Second)
	if err := n.Enqueue(context.Background(), api, apiInc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}

	rows := queuedRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("want the grouped alert and the recovery, got %d rows", len(rows))
	}
	first, err := DecodeAlert(rows[0].Payload)
	if err != nil {
		t.Fatalf("decode first row: %v", err)
	}
	if len(first.GroupedNames) != 2 || rows[0].Event != string(state.EventIncidentConfirmed) {
		t.Fatalf("first row should be the grouped outage of two, got %q naming %v", rows[0].Event, first.GroupedNames)
	}
	if rows[1].Event != string(state.EventIncidentResolved) {
		t.Fatalf("second row should be the recovery, got %q", rows[1].Event)
	}
}

// TestAlertRaisedDuringTheFinalFlushWaitsForIt: an alert that arrives while
// the stop is writing the held batches is neither held nor written ahead of
// them. The clock is the seam: flush reads it just before its insert, so the
// test checks at that moment that the stop still holds the lock, and raises a
// recovery from another goroutine. Done right, the recovery waits on the lock
// until the stop is over and then goes straight to the outbox, after the
// alert it closes. The lock check is what makes a regression fail every run:
// the outbox order alone would depend on how fast that goroutine gets going.
func TestAlertRaisedDuringTheFinalFlushWaitsForIt(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()

	var (
		armed      atomic.Bool
		raised     = make(chan struct{})
		onFlushNow func()
	)
	n := New(Options{
		DB:  db,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time {
			if armed.CompareAndSwap(true, false) {
				onFlushNow()
			}
			return clock.Now()
		},
		GroupWindow: time.Hour,
	})
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}

	onFlushNow = func() {
		if n.mu.TryLock() {
			n.mu.Unlock()
			t.Error("stop does not hold mu while it writes the held batches")
		}
		go func() {
			defer close(raised)
			if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
				t.Errorf("enqueue recovery: %v", err)
			}
		}()
	}
	armed.Store(true)

	n.stop(context.Background())
	<-raised

	n.mu.Lock()
	held := len(n.batches)
	n.mu.Unlock()
	if held != 0 {
		t.Fatalf("%d batches held in memory after the final flush", held)
	}
	if armed.Load() {
		t.Fatal("the final flush never read the clock; the test raised nothing")
	}

	rows := queuedRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("want the alert and the recovery in the outbox, got %d rows", len(rows))
	}
	if rows[0].Event != string(state.EventIncidentConfirmed) || rows[1].Event != string(state.EventIncidentResolved) {
		t.Fatalf("outbox order is %q, %q: a recovery must not be written before the alert it closes", rows[0].Event, rows[1].Event)
	}
}
