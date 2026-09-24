package notifier

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// nightClock starts at 01:00 in Amsterdam, inside a 23:00–07:00 window. The
// date is in the future on purpose: rows released through the store use the
// real clock, and they must already be due by the test clock.
func nightClock() time.Time {
	return time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC) // 01:00 CET
}

func setQuiet(t *testing.T, db *store.DB, channelID int64, during string) {
	t.Helper()
	if err := db.SetQuietHours(context.Background(), store.QuietHours{
		ChannelID: channelID, Start: "23:00", End: "07:00",
		Timezone: "Europe/Amsterdam", During: during,
	}); err != nil {
		t.Fatalf("set quiet hours: %v", err)
	}
}

func sweepN(t *testing.T, n *Notifier, times int) {
	t.Helper()
	for i := 0; i < times; i++ {
		if _, err := n.sweep(context.Background()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}
}

// TestQuietHoursHoldAlertsAndDigestThemAtTheEnd is the acceptance test for
// quiet hours: an alert during the window does not disappear, it waits, and
// the night arrives as one message when the window closes.
func TestQuietHoursHoldAlertsAndDigestThemAtTheEnd(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	setQuiet(t, db, ch.ID, store.QuietHold)

	clock := nightClock()
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })

	inc := openIncident(t, db, m.ID, clock.Add(-2*time.Minute), "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	sweepN(t, n, 2)
	if got := len(sender.delivered()); got != 0 {
		t.Fatalf("an alert at 01:00 was sent during quiet hours (%d messages)", got)
	}

	// Not lost: still pending, held until the window ends.
	held, err := db.HeldDeliveries(ctx, ch.ID)
	if err != nil {
		t.Fatalf("held: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected the alert to be held, found %d held deliveries", len(held))
	}
	wantEnd := time.Date(2030, 1, 15, 6, 0, 0, 0, time.UTC) // 07:00 CET
	if !held[0].NextAttemptAt.Equal(wantEnd) {
		t.Fatalf("held until %s, want the end of the window %s", held[0].NextAttemptAt, wantEnd)
	}

	// The monitor recovers at 03:00; that news waits too.
	clock = clock.Add(2 * time.Hour)
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	sweepN(t, n, 2)
	if got := len(sender.delivered()); got != 0 {
		t.Fatalf("a recovery at 03:00 was sent during quiet hours (%d messages)", got)
	}

	// 07:00: exactly one message, covering both.
	clock = wantEnd
	sweepN(t, n, 3)
	sent := sender.delivered()
	if len(sent) != 1 {
		t.Fatalf("expected one digest at the end of quiet hours, got %d messages", len(sent))
	}
	d := sent[0]
	if !d.Digest || d.Event != EventQuietDigest {
		t.Fatalf("expected a digest, got event %q", d.Event)
	}
	if len(d.Members) != 2 {
		t.Fatalf("digest carries %d alerts, want both the failure and the recovery", len(d.Members))
	}
	if want := "api went down and recovered during quiet hours"; d.Title() != want {
		t.Fatalf("title %q, want %q", d.Title(), want)
	}
	if d.Down() {
		t.Fatal("a night that fixed itself was rendered as bad news")
	}
	if body := d.Body(); !strings.Contains(body, "00:58–03:00 CET") {
		t.Fatalf("digest body should give local times, got:\n%s", body)
	}

	// Nothing left behind to be sent again.
	sweepN(t, n, 2)
	if got := len(sender.delivered()); got != 1 {
		t.Fatalf("the digest's parts were sent again: %d messages", got)
	}
}

// TestQuietHoursDigestLeadsWithWhatIsStillDown covers the morning where the
// answer is "get up": the title must say so without opening the message.
func TestQuietHoursDigestLeadsWithWhatIsStillDown(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	setQuiet(t, db, ch.ID, store.QuietHold)

	clock := nightClock()
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	inc.ReminderCount = 1
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentReminder, clock.Add(time.Hour)); err != nil {
		t.Fatalf("enqueue reminder: %v", err)
	}
	sweepN(t, n, 1)

	clock = time.Date(2030, 1, 15, 6, 0, 0, 0, time.UTC)
	sweepN(t, n, 3)
	sent := sender.delivered()
	if len(sent) != 1 {
		t.Fatalf("expected one digest, got %d messages", len(sent))
	}
	if want := "api is still down after quiet hours"; sent[0].Title() != want {
		t.Fatalf("title %q, want %q", sent[0].Title(), want)
	}
	if !sent[0].Down() {
		t.Fatal("a digest with a monitor still down was rendered as good news")
	}
	if body := sent[0].Body(); !strings.Contains(body, "down since 01:00 CET — connection refused") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestQuietHoursDropDiscardsOnlyWhenChosen checks the opt-in mode, and that
// it leaves a trace rather than deleting the row.
func TestQuietHoursDropDiscardsOnlyWhenChosen(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	setQuiet(t, db, ch.ID, store.QuietDrop)

	clock := nightClock()
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	sweepN(t, n, 1)
	clock = clock.Add(12 * time.Hour)
	sweepN(t, n, 2)
	if got := len(sender.delivered()); got != 0 {
		t.Fatalf("a dropped alert was delivered later (%d messages)", got)
	}

	d, err := db.GetDelivery(ctx, 1)
	if err != nil {
		t.Fatalf("get delivery: %v", err)
	}
	if d.LastError != "dropped during quiet hours" {
		t.Fatalf("dropped delivery says %q; the interface needs to know why nothing was sent", d.LastError)
	}
}

// TestAlertOutsideQuietHoursIsNotDelayed guards the other direction: a window
// must never touch an alert at 14:00.
func TestAlertOutsideQuietHoursIsNotDelayed(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	setQuiet(t, db, ch.ID, store.QuietHold)

	clock := time.Date(2030, 1, 15, 13, 0, 0, 0, time.UTC) // 14:00 CET
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	sweepN(t, n, 1)
	sent := sender.delivered()
	if len(sent) != 1 || sent[0].Digest {
		t.Fatalf("expected the ordinary alert at once, got %d messages", len(sent))
	}
}

// TestClearingQuietHoursReleasesWhatTheyHeld: removing the window at 02:00
// must not leave the night's alerts waiting for a 07:00 nobody configured.
func TestClearingQuietHoursReleasesWhatTheyHeld(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	setQuiet(t, db, ch.ID, store.QuietHold)

	clock := nightClock()
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, func() time.Time { return clock })

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	sweepN(t, n, 1)

	if err := db.ClearQuietHours(ctx, ch.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	clock = clock.Add(time.Minute)
	sweepN(t, n, 2)
	sent := sender.delivered()
	if len(sent) != 1 {
		t.Fatalf("expected the held alert to go out once the window was removed, got %d", len(sent))
	}
	if sent[0].Digest {
		t.Fatal("a single held alert should be released as itself, not as a digest of one")
	}
}
