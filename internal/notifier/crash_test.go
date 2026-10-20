package notifier

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// An alert waits in memory for up to the grouping window before its batch is
// written to the outbox. A clean stop writes it (stop_test.go); a process that
// is killed, runs out of memory or loses power gets no stop. The incident
// behind the alert is already stored as confirmed, so the next start restores
// it as an outage that has been announced: if the alert is not on disk, it is
// never sent. These tests end a notifier the way a crash does, by abandoning
// it, and start a new one on the same database file.

// crashDB opens a database at a path that outlives one handle, so a test can
// close it the way a dying process does and open it again.
func crashDB(t *testing.T) (string, *store.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crash.db")
	return path, reopenDB(t, path)
}

func reopenDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// heldRows counts the alerts still waiting on disk.
func heldRows(t *testing.T, db *store.DB) int {
	t.Helper()
	held, err := db.ListHeldAlerts(context.Background())
	if err != nil {
		t.Fatalf("list held alerts: %v", err)
	}
	return len(held)
}

func enqueueConfirmed(t *testing.T, n *Notifier, m store.Monitor, inc store.Incident, at time.Time) {
	t.Helper()
	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, at); err != nil {
		t.Fatalf("enqueue %s: %v", m.Name, err)
	}
}

// TestHeldAlertSurvivesACrash is the ticket: the process ends inside the
// window with no final flush, and the next start sends the alert when the
// window it was waiting in closes.
func TestHeldAlertSurvivesACrash(t *testing.T) {
	t.Parallel()

	path, db := crashDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	before := groupNotifier(t, db, clock, 2*time.Minute)
	enqueueConfirmed(t, before, m, inc, clock.Now())
	if got := countDeliveries(t, db); got != 0 {
		t.Fatalf("precondition: the alert should still be waiting, got %d outbox rows", got)
	}
	// The crash: no Run to cancel, no flush. Only what is on disk is left.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	clock.Advance(30 * time.Second)
	db = reopenDB(t, path)
	after := groupNotifier(t, db, clock, 2*time.Minute)
	after.restoreHeld(context.Background())

	// The window it was waiting in has not closed yet, so it still waits:
	// a restart in the middle of an outage must not split one message in two.
	after.flushDue(context.Background())
	if got := countDeliveries(t, db); got != 0 {
		t.Fatalf("a restored alert went out before its window closed: %d outbox rows", got)
	}

	clock.Advance(91 * time.Second)
	after.flushDue(context.Background())
	a := decodeOnlyDelivery(t, db)
	if a.Event != string(state.EventIncidentConfirmed) || a.MonitorName != "api" || a.IncidentID != inc.ID {
		t.Fatalf("restored alert reads %+v, want the confirmed alert for api's incident %d", a, inc.ID)
	}
	if got := heldRows(t, db); got != 0 {
		t.Fatalf("%d alerts still held after their batch reached the outbox", got)
	}
}

// TestRunRestoresHeldAlerts: Run reads what the last process left before it
// does anything else, so the alert is delivered without anyone calling
// restoreHeld by hand. The stop at the end writes the restored batch out.
func TestRunRestoresHeldAlerts(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	enqueueConfirmed(t, groupNotifier(t, db, clock, time.Hour), m, inc, clock.Now())

	runAndStop(t, groupNotifier(t, db, clock, time.Hour))
	if a := decodeOnlyDelivery(t, db); a.MonitorName != "api" {
		t.Fatalf("restored alert names %q, want api", a.MonitorName)
	}
}

// TestCleanStopLeavesNothingHeld: a batch written by the final flush releases
// its alerts in the same transaction, so the next start does not send them a
// second time.
func TestCleanStopLeavesNothingHeld(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	first := groupNotifier(t, db, clock, time.Hour)
	enqueueConfirmed(t, first, m, inc, clock.Now())
	if got := heldRows(t, db); got != 1 {
		t.Fatalf("an alert in its window is not kept on disk: %d held rows", got)
	}
	runAndStop(t, first)
	if got := heldRows(t, db); got != 0 {
		t.Fatalf("%d alerts still held after the clean stop wrote them", got)
	}

	// The second start delivers what the first one queued; a stub sender
	// keeps that off the network, and every outbox row is counted whatever
	// its status, so a delivery that ran first cannot hide a second write.
	sender := &fakeSender{}
	second := New(Options{
		DB:          db,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         clock.Now,
		GroupWindow: time.Hour,
		Senders:     map[string]Sender{store.ChannelWebhook: sender},
	})
	runAndStop(t, second)
	if got := len(outboxRows(t, db)); got != 1 {
		t.Fatalf("the alert was written %d times across a clean restart, want once", got)
	}
}

// TestReleasingABatchKeepsAnotherBatchsAlert: a cascade removes a held row
// without the notifier knowing, so the batch still names its id. The next
// alert held must not be given that id, or writing the first batch deletes
// the copy of an alert waiting in another one.
func TestReleasingABatchKeepsAnotherBatchsAlert(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ops := groupChannel(t, db, "ops")
	pager := groupChannel(t, db, "pager")
	a := groupMonitor(t, db, "edge-a", ops.ID)
	b := groupMonitor(t, db, "edge-b", ops.ID)
	c := groupMonitor(t, db, "edge-c", pager.ID)
	n := groupNotifier(t, db, clock, 90*time.Second)

	enqueueConfirmed(t, n, a, openIncident(t, db, a.ID, clock.Now(), "no such host"), clock.Now())
	enqueueConfirmed(t, n, b, openIncident(t, db, b.ID, clock.Now(), "no such host"), clock.Now())
	// edge-b's row, the newest, goes with the monitor.
	if err := db.DeleteMonitor(context.Background(), b.ID); err != nil {
		t.Fatalf("delete edge-b: %v", err)
	}

	clock.Advance(30 * time.Second)
	enqueueConfirmed(t, n, c, openIncident(t, db, c.ID, clock.Now(), "no such host"), clock.Now())

	// Only the ops batch has come of age; pager's waits another 29 s.
	clock.Advance(61 * time.Second)
	n.flushDue(context.Background())
	if got := countDeliveries(t, db); got != 1 {
		t.Fatalf("precondition: want the ops batch alone in the outbox, got %d rows", got)
	}
	if got := heldRows(t, db); got != 1 {
		t.Fatalf("writing the ops batch left %d held rows, want edge-c's alert still on disk", got)
	}
}

// TestAlertAfterRestartJoinsRestoredBatch: the window survives the restart,
// not only the alert. A monitor that fails a few seconds after the start, on
// the same dead uplink, still lands in the message about the ones before it,
// and the batch closes when it would have without the restart.
func TestAlertAfterRestartJoinsRestoredBatch(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	first := groupMonitor(t, db, "edge-a", ch.ID)
	second := groupMonitor(t, db, "edge-b", ch.ID)
	firstInc := openIncident(t, db, first.ID, clock.Now(), "no such host")

	enqueueConfirmed(t, groupNotifier(t, db, clock, 90*time.Second), first, firstInc, clock.Now())

	clock.Advance(20 * time.Second)
	after := groupNotifier(t, db, clock, 90*time.Second)
	after.restoreHeld(context.Background())
	clock.Advance(10 * time.Second)
	enqueueConfirmed(t, after, second, openIncident(t, db, second.ID, clock.Now(), "no such host"), clock.Now())

	// 90 s after the first alert: the restored window, not a fresh one.
	clock.Advance(61 * time.Second)
	after.flushDue(context.Background())
	a := decodeOnlyDelivery(t, db)
	if !slices.Equal(a.GroupedNames, []string{"edge-a", "edge-b"}) {
		t.Fatalf("grouped names = %v, want [edge-a edge-b] in one message", a.GroupedNames)
	}
	if got := heldRows(t, db); got != 0 {
		t.Fatalf("%d alerts still held after their batch reached the outbox", got)
	}
}

// TestRestoredAlertsComeFirstInAnOpenBatch: the scheduler starts beside the
// notifier, so a check can raise an alert before Run has read back what the
// last process left. The two batches become one, oldest news first.
func TestRestoredAlertsComeFirstInAnOpenBatch(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	first := groupMonitor(t, db, "edge-a", ch.ID)
	second := groupMonitor(t, db, "edge-b", ch.ID)

	enqueueConfirmed(t, groupNotifier(t, db, clock, 90*time.Second), first,
		openIncident(t, db, first.ID, clock.Now(), "no such host"), clock.Now())

	clock.Advance(5 * time.Second)
	after := groupNotifier(t, db, clock, 90*time.Second)
	enqueueConfirmed(t, after, second, openIncident(t, db, second.ID, clock.Now(), "no such host"), clock.Now())
	after.restoreHeld(context.Background())

	// The restored deadline is the earlier one, and it applies.
	clock.Advance(86 * time.Second)
	after.flushDue(context.Background())
	a := decodeOnlyDelivery(t, db)
	if !slices.Equal(a.GroupedNames, []string{"edge-a", "edge-b"}) {
		t.Fatalf("grouped names = %v, want [edge-a edge-b]", a.GroupedNames)
	}
	if got := heldRows(t, db); got != 0 {
		t.Fatalf("%d alerts still held after their batch reached the outbox", got)
	}
}

// TestDeletionTakesHeldAlerts: a channel or monitor deleted while an alert
// waits takes the waiting alert with it, as it takes its queued messages, so
// the next start has nothing to send to a channel that is gone.
func TestDeletionTakesHeldAlerts(t *testing.T) {
	t.Parallel()

	for _, gone := range []string{"channel", "monitor"} {
		t.Run(gone, func(t *testing.T) {
			t.Parallel()

			db := groupDB(t)
			clock := newTestClock()
			ch := groupChannel(t, db, "ops")
			m := groupMonitor(t, db, "api", ch.ID)
			n := groupNotifier(t, db, clock, time.Hour)
			enqueueConfirmed(t, n, m, openIncident(t, db, m.ID, clock.Now(), "timeout"), clock.Now())

			var err error
			if gone == "channel" {
				err = db.DeleteChannel(context.Background(), ch.ID)
			} else {
				err = db.DeleteMonitor(context.Background(), m.ID)
			}
			if err != nil {
				t.Fatalf("delete %s: %v", gone, err)
			}
			if got := heldRows(t, db); got != 0 {
				t.Fatalf("deleting the %s left %d alerts held for it", gone, got)
			}
			runAndStop(t, n)
			if got := countDeliveries(t, db); got != 0 {
				t.Fatalf("an alert for a deleted %s was written: %d outbox rows", gone, got)
			}
		})
	}
}

// TestNothingIsHeldWithoutAWindow: with grouping off an alert goes straight
// to the outbox, and so does a certificate notice; neither costs an extra
// write.
func TestNothingIsHeldWithoutAWindow(t *testing.T) {
	t.Parallel()

	t.Run("grouping off", func(t *testing.T) {
		t.Parallel()
		db := groupDB(t)
		clock := newTestClock()
		ch := groupChannel(t, db, "ops")
		m := groupMonitor(t, db, "api", ch.ID)
		n := groupNotifier(t, db, clock, GroupingDisabled)
		enqueueConfirmed(t, n, m, openIncident(t, db, m.ID, clock.Now(), "timeout"), clock.Now())
		if got := heldRows(t, db); got != 0 {
			t.Fatalf("%d alerts held with grouping off", got)
		}
		if got := countDeliveries(t, db); got != 1 {
			t.Fatalf("expected the alert in the outbox at once, got %d rows", got)
		}
	})

	t.Run("certificate notice", func(t *testing.T) {
		t.Parallel()
		db := groupDB(t)
		clock := newTestClock()
		ch := groupChannel(t, db, "ops")
		m := groupMonitor(t, db, "api", ch.ID)
		n := groupNotifier(t, db, clock, time.Hour)
		notice := store.Incident{ID: 0, MonitorID: m.ID, StartedAt: clock.Now(), Notice: true, Cause: "ssl_expiring"}
		if err := n.Enqueue(context.Background(), m, notice, state.EventIncidentConfirmed, clock.Now()); err != nil {
			t.Fatalf("enqueue notice: %v", err)
		}
		if got := heldRows(t, db); got != 0 {
			t.Fatalf("%d notices held; a notice is never batched", got)
		}
	})
}

// TestUnreadableHeldAlertIsDiscarded: a row that does not decode is dropped
// with a log line rather than read and refused on every start.
func TestUnreadableHeldAlertIsDiscarded(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	if _, err := db.HoldAlert(context.Background(), store.HeldAlert{
		ChannelID: ch.ID, MonitorID: m.ID, Payload: "not an alert", Due: clock.Now(),
	}); err != nil {
		t.Fatalf("hold: %v", err)
	}

	n := New(Options{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock.Now, GroupWindow: time.Hour})
	n.restoreHeld(context.Background())
	if got := heldRows(t, db); got != 0 {
		t.Fatalf("an unreadable held alert was kept: %d rows", got)
	}
	if got := len(n.batches); got != 0 {
		t.Fatalf("an unreadable held alert made %d batches", got)
	}
}
