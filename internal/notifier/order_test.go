package notifier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// These tests pin the order a channel hears about one outage in: a recovery
// never arrives before the alert it closes. The outbox retries each row on
// its own schedule, so without a rule a recovery queued while the alert was
// still being retried went out first, and the alert followed minutes later,
// about an outage that was already over.

// receiver is a fake endpoint with a hand-wound clock. It answers 503 to
// every request while its own outage lasts, and records what got through and
// when, per channel URL.
type receiver struct {
	clock *testClock

	mu sync.Mutex
	// downUntil is when each URL starts answering again. A URL missing
	// from the map is up.
	downUntil map[string]time.Time
	// reject refuses every alert with this event, for good, the way an
	// endpoint that cannot parse one kind of message would.
	reject string
	// refused is when the receiver last turned an alert away.
	refused time.Time
	got     []received
}

type received struct {
	url   string
	at    time.Time
	alert Alert
}

func (r *receiver) Send(_ context.Context, cfg map[string]string, a Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	if until, ok := r.downUntil[cfg["url"]]; ok && now.Before(until) {
		r.refused = now
		return retryable("endpoint returned 503")
	}
	if r.reject != "" && a.Event == r.reject {
		r.refused = now
		return retryable("endpoint returned 503 for this alert")
	}
	r.got = append(r.got, received{url: cfg["url"], at: now, alert: a})
	return nil
}

func (r *receiver) Validate(map[string]string) error { return nil }

// deliveredTo returns what one URL received, in order.
func (r *receiver) deliveredTo(url string) []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []received
	for _, g := range r.got {
		if g.url == url {
			out = append(out, g)
		}
	}
	return out
}

func orderNotifier(t *testing.T, db *store.DB, clock *testClock, window time.Duration, r *receiver) *Notifier {
	t.Helper()
	return New(Options{
		DB:          db,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders:     map[string]Sender{store.ChannelWebhook: r},
		Now:         clock.Now,
		GroupWindow: window,
	})
}

// tick runs the notifier's loop body once per simulated second until the
// clock reaches until.
func tick(t *testing.T, n *Notifier, clock *testClock, until time.Time) {
	t.Helper()
	ctx := context.Background()
	for clock.Now().Before(until) {
		clock.Advance(time.Second)
		n.flushDue(ctx)
		if _, err := n.sweep(ctx); err != nil {
			t.Fatalf("sweep at %s: %v", clock.Now(), err)
		}
	}
}

// leaves flattens a message into the alerts it carries.
func leaves(a Alert) []Alert {
	if len(a.Members) == 0 {
		return []Alert{a}
	}
	var out []Alert
	for _, m := range a.Members {
		out = append(out, leaves(m)...)
	}
	return out
}

// firstAbout returns the index of the first message carrying news about the
// incident in the given direction, or -1.
func firstAbout(got []received, incident int64, down bool) int {
	for i, g := range got {
		for _, l := range leaves(g.alert) {
			if l.IncidentID == incident && l.Down() == down {
				return i
			}
		}
	}
	return -1
}

// assertInOrder fails unless the channel heard the incident go down, and only
// afterwards heard it come back.
func assertInOrder(t *testing.T, got []received, incident int64) {
	t.Helper()
	down, up := firstAbout(got, incident, true), firstAbout(got, incident, false)
	var seen []string
	for _, g := range got {
		seen = append(seen, fmt.Sprintf("%s@%s", g.alert.Title(), g.at.Format("15:04:05")))
	}
	if down < 0 || up < 0 {
		t.Fatalf("expected both the alert and the recovery, got %v", seen)
	}
	if up < down {
		t.Fatalf("the recovery arrived before the alert it closes: %v", seen)
	}
}

// TestRecoveryNeverOvertakesItsAlert is the ticket's reproduction: a receiver
// that is away for a while, and an outage that ends while the alert about it
// is still being retried. The three cases are the ones measured on the
// unfixed code; the first two delivered the recovery first.
func TestRecoveryNeverOvertakesItsAlert(t *testing.T) {
	cases := []struct {
		name         string
		window       time.Duration
		receiverDown time.Duration
		recoverAfter time.Duration
	}{
		{"grouping off", GroupingDisabled, 60 * time.Second, 20 * time.Second},
		{"default window, long receiver outage", DefaultGroupWindow, 240 * time.Second, 60 * time.Second},
		{"default window, already in order", DefaultGroupWindow, 200 * time.Second, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, m, ch := testDB(t)
			ctx := context.Background()
			clock := newTestClock()
			start := clock.Now()
			r := &receiver{clock: clock, downUntil: map[string]time.Time{ch.Config["url"]: start.Add(tc.receiverDown)}}
			n := orderNotifier(t, db, clock, tc.window, r)

			inc := openIncident(t, db, m.ID, start, "connection refused")
			if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, start); err != nil {
				t.Fatalf("enqueue alert: %v", err)
			}
			tick(t, n, clock, start.Add(tc.recoverAfter))
			if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
				t.Fatalf("enqueue recovery: %v", err)
			}
			tick(t, n, clock, start.Add(time.Hour))

			got := r.deliveredTo(ch.Config["url"])
			if len(got) != 2 {
				t.Fatalf("expected the alert and the recovery, got %d messages", len(got))
			}
			assertInOrder(t, got, inc.ID)

			// Waiting is not a failed attempt: the recovery was sent
			// once, and its row says so.
			rows := outboxRows(t, db)
			for _, row := range rows {
				if row.event == string(state.EventIncidentResolved) && row.attempts != 1 {
					t.Errorf("the recovery was charged %d attempts for one send", row.attempts)
				}
			}
		})
	}
}

type outboxRow struct {
	id       int64
	channel  int64
	event    string
	status   string
	attempts int
}

func outboxRows(t *testing.T, db *store.DB) []outboxRow {
	t.Helper()
	rows, err := db.Reader.QueryContext(context.Background(),
		`SELECT id, channel_id, event, status, attempts FROM notif_outbox ORDER BY id`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.channel, &r.event, &r.status, &r.attempts); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return out
}

// TestRecoveryGoesOutAfterItsAlertGivesUp pins the dead-letter decision. An
// alert that never arrives does not take its recovery down with it: hearing
// "back up" without the "down" before it is better than hearing nothing.
func TestRecoveryGoesOutAfterItsAlertGivesUp(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	clock := newTestClock()
	start := clock.Now()
	r := &receiver{clock: clock, reject: string(state.EventIncidentConfirmed)}
	n := orderNotifier(t, db, clock, GroupingDisabled, r)

	inc := openIncident(t, db, m.ID, start, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, start); err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}
	tick(t, n, clock, start.Add(20*time.Second))
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Hour))

	got := r.deliveredTo(ch.Config["url"])
	if len(got) != 1 || got[0].alert.Event != string(state.EventIncidentResolved) {
		t.Fatalf("expected the recovery alone once the alert gave up, got %d messages", len(got))
	}

	// It waited for the alert to give up rather than going out at once.
	for _, row := range outboxRows(t, db) {
		if row.event == string(state.EventIncidentConfirmed) && row.status != store.OutboxFailed {
			t.Fatalf("the alert is %s, want it dead-lettered", row.status)
		}
	}
	r.mu.Lock()
	gaveUp := r.refused
	r.mu.Unlock()
	if got[0].at.Before(gaveUp) {
		t.Fatalf("the recovery went out at %s, before its alert gave up at %s", got[0].at, gaveUp)
	}
}

// TestChannelsDoNotWaitForEachOther: the rule is per channel. A phone that
// heard the alert hears the recovery at once, however long another channel's
// copy of the alert is stuck in retries.
func TestChannelsDoNotWaitForEachOther(t *testing.T) {
	db, m, ops := testDB(t)
	ctx := context.Background()
	phone, err := db.CreateChannel(ctx, store.Channel{
		Name: "phone", Type: store.ChannelWebhook, Enabled: true,
		Config: map[string]string{"url": "https://example.com/phone"},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.SetMonitorChannels(ctx, m.ID, []int64{ops.ID, phone.ID}); err != nil {
		t.Fatalf("assign channels: %v", err)
	}

	clock := newTestClock()
	start := clock.Now()
	r := &receiver{clock: clock, downUntil: map[string]time.Time{ops.Config["url"]: start.Add(10 * time.Minute)}}
	n := orderNotifier(t, db, clock, GroupingDisabled, r)

	inc := openIncident(t, db, m.ID, start, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, start); err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}
	tick(t, n, clock, start.Add(20*time.Second))
	recovered := clock.Now()
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, recovered); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Hour))

	onPhone := r.deliveredTo(phone.Config["url"])
	assertInOrder(t, onPhone, inc.ID)
	if late := onPhone[1].at.Sub(recovered); late > 2*time.Second {
		t.Fatalf("the phone heard the recovery %s late, waiting on another channel", late)
	}
	assertInOrder(t, r.deliveredTo(ops.Config["url"]), inc.ID)
}

// TestGroupedRecoveryWaitsForEachMember: the rule is per monitor in a batch,
// not per row. A shared outage is one row naming two incidents, and only the
// first is in the row's own incident column; the second monitor's recovery
// must still wait for it.
func TestGroupedRecoveryWaitsForEachMember(t *testing.T) {
	db := groupDB(t)
	ctx := context.Background()
	ch := groupChannel(t, db, "ops")
	a := groupMonitor(t, db, "api", ch.ID)
	b := groupMonitor(t, db, "web", ch.ID)

	clock := newTestClock()
	start := clock.Now()
	// Away long enough for the grouped alert to be in retry when the
	// recovery's batch closes at 2m40s, and back before that.
	r := &receiver{clock: clock, downUntil: map[string]time.Time{ch.Config["url"]: start.Add(150 * time.Second)}}
	n := orderNotifier(t, db, clock, time.Minute, r)

	incA := openIncident(t, db, a.ID, start, "timeout")
	incB := openIncident(t, db, b.ID, start, "timeout")
	if err := n.Enqueue(ctx, a, incA, state.EventIncidentConfirmed, start); err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	tick(t, n, clock, start.Add(5*time.Second))
	if err := n.Enqueue(ctx, b, incB, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}
	tick(t, n, clock, start.Add(100*time.Second))
	if err := n.Enqueue(ctx, b, incB, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue b recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Hour))

	got := r.deliveredTo(ch.Config["url"])
	if len(got) != 2 {
		t.Fatalf("expected the grouped alert and one recovery, got %d messages", len(got))
	}
	assertInOrder(t, got, incB.ID)
	if !got[0].alert.Grouped() {
		t.Fatalf("the first message is %q, want the grouped alert", got[0].alert.Title())
	}
}

// TestRecoveryInAnEarlierBatchDoesNotOvertake covers the order the grouping
// window itself can invert, with a receiver that never fails. A recovery joins
// a batch of recoveries that opened before its own alert was queued, so that
// batch closes first; the alert's batch has to go with it.
func TestRecoveryInAnEarlierBatchDoesNotOvertake(t *testing.T) {
	db := groupDB(t)
	ctx := context.Background()
	ch := groupChannel(t, db, "ops")
	early := groupMonitor(t, db, "early", ch.ID)
	api := groupMonitor(t, db, "api", ch.ID)

	clock := newTestClock()
	start := clock.Now()
	r := &receiver{clock: clock}
	n := orderNotifier(t, db, clock, time.Minute, r)

	// "early" recovers first and opens the batch of recoveries.
	incEarly := openIncident(t, db, early.ID, start.Add(-10*time.Minute), "timeout")
	if err := n.Enqueue(ctx, early, incEarly, state.EventIncidentResolved, start); err != nil {
		t.Fatalf("enqueue early recovery: %v", err)
	}
	// Then api goes down and comes back inside that batch's window.
	tick(t, n, clock, start.Add(40*time.Second))
	incAPI := openIncident(t, db, api.ID, clock.Now(), "timeout")
	if err := n.Enqueue(ctx, api, incAPI, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue api alert: %v", err)
	}
	tick(t, n, clock, start.Add(50*time.Second))
	if err := n.Enqueue(ctx, api, incAPI, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue api recovery: %v", err)
	}
	tick(t, n, clock, start.Add(10*time.Minute))

	assertInOrder(t, r.deliveredTo(ch.Config["url"]), incAPI.ID)
}

// TestShutdownWritesAlertsBeforeRecoveries: a restart writes every open batch
// to the outbox. The outbox holds a recovery back only for an alert queued
// before it, so the alert has to be written first, every time.
func TestShutdownWritesAlertsBeforeRecoveries(t *testing.T) {
	db := groupDB(t)
	ctx := context.Background()
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	clock := newTestClock()

	// Map order is random; enough rounds make an order that only holds by
	// luck fail.
	for i := 0; i < 24; i++ {
		n := orderNotifier(t, db, clock, time.Minute, &receiver{clock: clock})
		other := groupMonitor(t, db, fmt.Sprintf("other-%d", i), ch.ID)
		incOther := openIncident(t, db, other.ID, clock.Now().Add(-time.Hour), "timeout")
		if err := n.Enqueue(ctx, other, incOther, state.EventIncidentResolved, clock.Now()); err != nil {
			t.Fatalf("enqueue other recovery: %v", err)
		}
		inc := openIncident(t, db, m.ID, clock.Now(), "timeout")
		if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
			t.Fatalf("enqueue alert: %v", err)
		}
		if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
			t.Fatalf("enqueue recovery: %v", err)
		}
		n.flushAll(ctx)

		rows := outboxRows(t, db)
		last := rows[len(rows)-2:]
		if last[0].event != string(state.EventIncidentConfirmed) {
			t.Fatalf("round %d: the recovery was written before its alert (%s, %s)", i, last[0].event, last[1].event)
		}
		if _, err := db.ResolveIncident(ctx, m.ID, clock.Now()); err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("resolve: %v", err)
		}
		clock.Advance(time.Minute)
	}
}
