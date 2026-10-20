package notifier

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// An outage is confirmed in one write and its alert kept on disk in another.
// A process that ended between the two used to leave an incident stored as
// confirmed and no alert anywhere, so the next start restored an outage that
// read as announced and nobody was ever told. The confirm now marks the
// incident as owed its alert, the write that keeps the alert clears the mark,
// and the next start announces whatever is still owed. These tests end a
// notifier the way a crash does, by abandoning it.

// confirmOwed confirms an open incident the way the runner does when its
// alert is about to be sent, and returns it as the runner reads it back.
func confirmOwed(t *testing.T, db *store.DB, m store.Monitor, at time.Time) store.Incident {
	t.Helper()
	ctx := context.Background()
	if _, err := db.OpenIncident(ctx, m.ID, at.Add(-time.Minute), "timeout", "timeout"); err != nil {
		t.Fatalf("open incident: %v", err)
	}
	if err := db.ConfirmOwedIncident(ctx, m.ID, at, "timeout", "timeout"); err != nil {
		t.Fatalf("confirm incident: %v", err)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil {
		t.Fatalf("read incident: %v", err)
	}
	if !inc.AlertOwed {
		t.Fatal("precondition: the confirmed incident is not marked as owed its alert")
	}
	return inc
}

func owedCount(t *testing.T, db *store.DB) int {
	t.Helper()
	owed, err := db.OwedIncidents(context.Background())
	if err != nil {
		t.Fatalf("owed incidents: %v", err)
	}
	return len(owed)
}

// restart starts a notifier on the database the way the server does: owed
// alerts first, then whatever the last run left waiting, then one pass with
// every window that has closed.
func restart(t *testing.T, db *store.DB, clock *testClock, window time.Duration) *Notifier {
	t.Helper()
	n := groupNotifier(t, db, clock, window)
	if err := n.AnnounceOwed(context.Background()); err != nil {
		t.Fatalf("announce owed: %v", err)
	}
	n.restoreHeld(context.Background())
	return n
}

// failHolds makes every write to notif_held fail, as a full disk would, until
// the returned function is called.
func failHolds(t *testing.T, db *store.DB) func() {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Writer.ExecContext(ctx, `
		CREATE TRIGGER fail_hold BEFORE INSERT ON notif_held
		BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	return func() {
		if _, err := db.Writer.ExecContext(ctx, `DROP TRIGGER fail_hold`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
	}
}

// TestOwedAlertIsSentAfterACrashBeforeItWasHeld is the ticket: the process
// ends after the outage is confirmed and before its alert is anywhere on
// disk. The next start sends it, once, and a start after that sends nothing.
func TestOwedAlertIsSentAfterACrashBeforeItWasHeld(t *testing.T) {
	t.Parallel()

	path, db := crashDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := confirmOwed(t, db, m, clock.Now())
	// The crash: Enqueue never ran.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	clock.Advance(time.Minute)
	db = reopenDB(t, path)
	after := restart(t, db, clock, 90*time.Second)
	if got := owedCount(t, db); got != 0 {
		t.Fatalf("%d incidents still owed after the start announced them", got)
	}
	clock.Advance(91 * time.Second)
	after.flushDue(context.Background())
	a := decodeOnlyDelivery(t, db)
	if a.IncidentID != inc.ID || a.MonitorName != "api" {
		t.Fatalf("announced alert reads %+v, want the alert for api's incident %d", a, inc.ID)
	}

	// A further start, clean or not, has nothing left to announce.
	restart(t, db, clock, 90*time.Second).flushAll(context.Background())
	if got := countDeliveries(t, db); got != 1 {
		t.Fatalf("the owed alert was written %d times across two starts, want once", got)
	}
}

// TestFailedHoldIsOwedUntilItsBatchIsWritten: when writing the waiting copy
// fails the alert lives in memory only, and its incident stays owed. A crash
// then is covered by the next start; a window that closes normally writes the
// batch and clears the mark in the same write, so the next start does not
// send it again.
func TestFailedHoldIsOwedUntilItsBatchIsWritten(t *testing.T) {
	t.Parallel()

	t.Run("crash inside the window", func(t *testing.T) {
		t.Parallel()
		path, db := crashDB(t)
		clock := newTestClock()
		ch := groupChannel(t, db, "ops")
		m := groupMonitor(t, db, "api", ch.ID)
		inc := confirmOwed(t, db, m, clock.Now())

		restore := failHolds(t, db)
		enqueueConfirmed(t, groupNotifier(t, db, clock, 90*time.Second), m, inc, clock.Now())
		if got := heldRows(t, db); got != 0 {
			t.Fatalf("precondition: the held write should have failed, got %d rows", got)
		}
		if got := owedCount(t, db); got != 1 {
			t.Fatalf("a failed held write cleared the owed mark: %d owed", got)
		}
		restore()
		if err := db.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		db = reopenDB(t, path)
		restart(t, db, clock, 90*time.Second).flushAll(context.Background())
		if a := decodeOnlyDelivery(t, db); a.IncidentID != inc.ID {
			t.Fatalf("announced alert names incident %d, want %d", a.IncidentID, inc.ID)
		}
	})

	t.Run("window closes", func(t *testing.T) {
		t.Parallel()
		db := groupDB(t)
		clock := newTestClock()
		ops := groupChannel(t, db, "ops")
		pager := groupChannel(t, db, "pager")
		m := groupMonitor(t, db, "api", ops.ID, pager.ID)
		inc := confirmOwed(t, db, m, clock.Now())

		restore := failHolds(t, db)
		n := groupNotifier(t, db, clock, 90*time.Second)
		enqueueConfirmed(t, n, m, inc, clock.Now())
		restore()

		clock.Advance(91 * time.Second)
		n.flushDue(context.Background())
		if got := countDeliveries(t, db); got != 2 {
			t.Fatalf("want the alert on both channels, got %d outbox rows", got)
		}
		if got := owedCount(t, db); got != 0 {
			t.Fatalf("writing the alert left %d incidents owed", got)
		}
		restart(t, db, clock, 90*time.Second).flushAll(context.Background())
		if got := countDeliveries(t, db); got != 2 {
			t.Fatalf("a restart after the alert was written sent it again: %d outbox rows", got)
		}
	})
}

// TestOwedMarkGoesWithTheAlertOnDisk: every path that keeps the alert on disk
// clears the mark in the same write, so a crash after it is covered by the
// copy rather than by a second announcement.
func TestOwedMarkGoesWithTheAlertOnDisk(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		window time.Duration
		held   int
		queued int
	}{
		{"held in its window", 90 * time.Second, 1, 0},
		{"grouping off", GroupingDisabled, 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path, db := crashDB(t)
			clock := newTestClock()
			ch := groupChannel(t, db, "ops")
			m := groupMonitor(t, db, "api", ch.ID)
			inc := confirmOwed(t, db, m, clock.Now())

			enqueueConfirmed(t, groupNotifier(t, db, clock, c.window), m, inc, clock.Now())
			if got := owedCount(t, db); got != 0 {
				t.Fatalf("%d incidents still owed with the alert on disk", got)
			}
			if got, q := heldRows(t, db), countDeliveries(t, db); got != c.held || q != c.queued {
				t.Fatalf("held %d, queued %d; want %d and %d", got, q, c.held, c.queued)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			db = reopenDB(t, path)
			restart(t, db, clock, c.window).flushAll(context.Background())
			if got := countDeliveries(t, db); got != 1 {
				t.Fatalf("the alert was written %d times across a crash, want once", got)
			}
		})
	}
}

// TestNothingToSendIsNotAnnouncedOnStart: an outage with no channel to go to,
// with every channel switched off, or confirmed in maintenance, owes nothing,
// and a start does not announce it.
func TestNothingToSendIsNotAnnouncedOnStart(t *testing.T) {
	t.Parallel()

	for _, c := range []string{"no channel", "channel switched off", "maintenance"} {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := groupDB(t)
			// A maintenance window covers only the time after it was
			// created, so this clock starts at the real time.
			clock := &testClock{t: time.Now().UTC().Truncate(time.Second).Add(time.Minute)}
			ch := groupChannel(t, db, "ops")
			var m store.Monitor
			switch c {
			case "no channel":
				m = groupMonitor(t, db, "api")
			case "channel switched off":
				m = groupMonitor(t, db, "api", ch.ID)
				ch.Enabled = false
				if _, err := db.UpdateChannel(ctx, ch); err != nil {
					t.Fatalf("switch channel off: %v", err)
				}
			case "maintenance":
				m = groupMonitor(t, db, "api", ch.ID)
				if _, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{
					Name: "deploy", MonitorID: m.ID,
					StartsAt: clock.Now().Add(-time.Hour), EndsAt: clock.Now().Add(time.Hour),
				}); err != nil {
					t.Fatalf("create maintenance: %v", err)
				}
			}
			inc := confirmOwed(t, db, m, clock.Now())

			enqueueConfirmed(t, groupNotifier(t, db, clock, 90*time.Second), m, inc, clock.Now())
			if got := owedCount(t, db); got != 0 {
				t.Fatalf("an alert with nothing to send left %d incidents owed", got)
			}
			if c == "maintenance" {
				got, err := db.GetIncident(ctx, inc.ID)
				if err != nil || !got.MaintenancePending {
					t.Fatalf("maintenance did not take the alert over: %+v, %v", got, err)
				}
			}
			restart(t, db, clock, 90*time.Second).flushAll(ctx)
			if got := countDeliveries(t, db); got != 0 {
				t.Fatalf("a start announced an outage with nothing to send: %d outbox rows", got)
			}
		})
	}
}

// TestAnnounceOwedKeepsGoingPastAFailure: an owed alert whose write fails
// does not hold up another, and stays owed for the next start.
func TestAnnounceOwedKeepsGoingPastAFailure(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	ch := groupChannel(t, db, "ops")
	first := groupMonitor(t, db, "edge-a", ch.ID)
	second := groupMonitor(t, db, "edge-b", ch.ID)
	confirmOwed(t, db, first, clock.Now())
	confirmOwed(t, db, second, clock.Now())

	// edge-a's alert cannot be written to the outbox; with grouping off
	// nothing else keeps it.
	if _, err := db.Writer.ExecContext(context.Background(), `
		CREATE TRIGGER fail_first BEFORE INSERT ON notif_outbox
		WHEN NEW.monitor_id = `+strconv.FormatInt(first.ID, 10)+`
		BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	n := groupNotifier(t, db, clock, GroupingDisabled)
	if err := n.AnnounceOwed(context.Background()); err != nil {
		t.Fatalf("announce owed: %v", err)
	}
	if a := decodeOnlyDelivery(t, db); a.MonitorName != "edge-b" {
		t.Fatalf("announced %q, want edge-b past edge-a's failure", a.MonitorName)
	}
	owed, err := db.OwedIncidents(context.Background())
	if err != nil || len(owed) != 1 || owed[0].MonitorID != first.ID {
		t.Fatalf("owed after the start = %+v, %v; want edge-a's incident kept for the next start", owed, err)
	}
}
