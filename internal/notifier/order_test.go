package notifier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
// about an outage that was already over. With the rule, a recovery that finds
// its alert still queued replaces it: one message about the whole outage.

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
	// refused is when the receiver last turned an alert away, and
	// refusals how often it did.
	refused  time.Time
	refusals int
	got      []received
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
		r.refusals++
		return retryable("endpoint returned 503")
	}
	if r.reject != "" && a.Event == r.reject {
		r.refused = now
		r.refusals++
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
	seen := titles(got)
	if down < 0 || up < 0 {
		t.Fatalf("expected both the alert and the recovery, got %v", seen)
	}
	if up < down {
		t.Fatalf("the recovery arrived before the alert it closes: %v", seen)
	}
}

// TestRecoveryReplacesAnAlertStillQueued is the ticket's reproduction: a
// receiver that is away for a while, and an outage that ends while the alert
// about it is still being retried. The three cases are the ones measured on
// the unfixed code, where the first two delivered the recovery first. Now the
// channel hears one message, "api was down for ..., now back up", and never
// a "down" for an outage that was already over.
func TestRecoveryReplacesAnAlertStillQueued(t *testing.T) {
	cases := []struct {
		name         string
		window       time.Duration
		receiverDown time.Duration
		recoverAfter time.Duration
	}{
		{"grouping off", GroupingDisabled, 60 * time.Second, 20 * time.Second},
		{"default window, long receiver outage", DefaultGroupWindow, 240 * time.Second, 60 * time.Second},
		{"default window, short receiver outage", DefaultGroupWindow, 200 * time.Second, 30 * time.Second},
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
			if len(got) != 1 {
				t.Fatalf("expected one message about the whole outage, got %d: %v", len(got), titles(got))
			}
			if !got[0].alert.ReplacesAlert || got[0].alert.Down() {
				t.Fatalf("the message is %q, want the recovery that replaces the alert", got[0].alert.Title())
			}
			if want := "api was down for"; !strings.HasPrefix(got[0].alert.Title(), want) {
				t.Errorf("title %q, want it to start %q", got[0].alert.Title(), want)
			}

			// The merged message took over the alert's retries rather
			// than starting its own: every refusal was charged to the
			// one schedule, and the send that got through ends it.
			r.mu.Lock()
			refusals := r.refusals
			r.mu.Unlock()
			for _, row := range outboxRows(t, db) {
				switch row.event {
				case string(state.EventIncidentResolved):
					if row.status != store.OutboxDelivered || row.attempts != refusals+1 {
						t.Errorf("merged recovery = %s after %d attempts, want delivered after %d (the alert's %d refusals, then the send)",
							row.status, row.attempts, refusals+1, refusals)
					}
				case string(state.EventIncidentConfirmed):
					if !row.suppressed || row.status != store.OutboxPending {
						t.Errorf("the replaced alert is %s (suppressed %v), want it closed", row.status, row.suppressed)
					}
				}
			}
		})
	}
}

func titles(got []received) []string {
	var out []string
	for _, g := range got {
		out = append(out, fmt.Sprintf("%s@%s", g.alert.Title(), g.at.Format("15:04:05")))
	}
	return out
}

type outboxRow struct {
	id         int64
	channel    int64
	event      string
	status     string
	attempts   int
	suppressed bool
}

func outboxRows(t *testing.T, db *store.DB) []outboxRow {
	t.Helper()
	rows, err := db.Reader.QueryContext(context.Background(),
		`SELECT id, channel_id, event, status, attempts, suppressed FROM notif_outbox ORDER BY id`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.channel, &r.event, &r.status, &r.attempts, &r.suppressed); err != nil {
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
// "back up" without the "down" before it is better than hearing nothing. The
// alert gave up before the outage ended, so there is nothing to merge with.
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
	// Six attempts span about twenty minutes.
	tick(t, n, clock, start.Add(40*time.Minute))
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Hour))

	got := r.deliveredTo(ch.Config["url"])
	if len(got) != 1 || got[0].alert.Event != string(state.EventIncidentResolved) {
		t.Fatalf("expected the recovery alone once the alert gave up, got %v", titles(got))
	}
	if got[0].alert.ReplacesAlert {
		t.Errorf("the recovery claims to replace an alert that had already given up: %q", got[0].alert.Title())
	}
	for _, row := range outboxRows(t, db) {
		if row.event == string(state.EventIncidentConfirmed) && (row.status != store.OutboxFailed || row.attempts != maxAttempts) {
			t.Fatalf("the alert is %s after %d attempts, want it dead-lettered after %d", row.status, row.attempts, maxAttempts)
		}
	}
}

// TestRecoveryReplacesOnlyBeforeTheAlertArrived: an alert that got through is
// not merged with anything, and its recovery reads as an ordinary one.
func TestRecoveryReplacesOnlyBeforeTheAlertArrived(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	clock := newTestClock()
	start := clock.Now()
	r := &receiver{clock: clock}
	n := orderNotifier(t, db, clock, GroupingDisabled, r)

	inc := openIncident(t, db, m.ID, start, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, start); err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}
	tick(t, n, clock, start.Add(20*time.Second))
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Minute))

	got := r.deliveredTo(ch.Config["url"])
	assertInOrder(t, got, inc.ID)
	if len(got) != 2 || got[1].alert.ReplacesAlert {
		t.Fatalf("expected the alert, then a plain recovery, got %v", titles(got))
	}
}

// TestRecoveryTakesAQueuedReminderWithIt: a reminder still being retried when
// the monitor comes back would arrive saying "still down" about something
// that is up. It goes with the recovery; the recovery reads as an ordinary
// one, since the channel did hear the first alert.
func TestRecoveryTakesAQueuedReminderWithIt(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	clock := newTestClock()
	start := clock.Now()
	url := ch.Config["url"]
	r := &receiver{clock: clock}
	n := orderNotifier(t, db, clock, GroupingDisabled, r)

	inc := openIncident(t, db, m.ID, start, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, start); err != nil {
		t.Fatalf("enqueue alert: %v", err)
	}
	tick(t, n, clock, start.Add(10*time.Minute))
	r.mu.Lock()
	r.downUntil = map[string]time.Time{url: clock.Now().Add(5 * time.Minute)}
	r.mu.Unlock()
	inc.ReminderCount = 1
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentReminder, clock.Now()); err != nil {
		t.Fatalf("enqueue reminder: %v", err)
	}
	tick(t, n, clock, start.Add(11*time.Minute))
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue recovery: %v", err)
	}
	tick(t, n, clock, start.Add(time.Hour))

	got := r.deliveredTo(url)
	if len(got) != 2 {
		t.Fatalf("expected the alert and the recovery, got %v", titles(got))
	}
	assertInOrder(t, got, inc.ID)
	if got[1].alert.ReplacesAlert {
		t.Errorf("the recovery says the channel never heard the outage, but it got the alert: %q", got[1].alert.Title())
	}
	for _, row := range outboxRows(t, db) {
		if row.event == string(state.EventIncidentReminder) && !row.suppressed {
			t.Errorf("the reminder is %s, want it closed by the recovery", row.status)
		}
	}
}

// TestMergedRowsAreNotSentFromAStaleBatch: a sweep loads its rows before it
// attempts any. When a recovery early in the batch takes over an alert later
// in it, that alert must not go out as it was loaded.
func TestMergedRowsAreNotSentFromAStaleBatch(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	clock := newTestClock()
	start := clock.Now()
	r := &receiver{clock: clock}
	n := orderNotifier(t, db, clock, GroupingDisabled, r)

	inc := openIncident(t, db, m.ID, start, "connection refused")
	enqueue := func(event state.Event, at, due time.Time) store.Delivery {
		t.Helper()
		payload, err := AlertFromStore(m, inc, event, at).Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		d, err := db.EnqueueDelivery(ctx, store.Delivery{ChannelID: ch.ID, MonitorID: m.ID, IncidentID: inc.ID,
			Event: string(event), Payload: payload, NextAttemptAt: due})
		if err != nil {
			t.Fatalf("enqueue %s: %v", event, err)
		}
		return d
	}
	// The alert's retry comes due a few seconds after the recovery was
	// queued, so the recovery is first in the batch.
	enqueue(state.EventIncidentConfirmed, start, start.Add(5*time.Second))
	enqueue(state.EventIncidentResolved, start.Add(2*time.Second), start.Add(2*time.Second))
	clock.Advance(10 * time.Second)
	tick(t, n, clock, start.Add(time.Minute))

	got := r.deliveredTo(ch.Config["url"])
	if len(got) != 1 || !got[0].alert.ReplacesAlert {
		t.Fatalf("expected the one merged message, got %v", titles(got))
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
	if onPhone[1].alert.ReplacesAlert {
		t.Errorf("the phone got the alert, but its recovery says it did not: %q", onPhone[1].alert.Title())
	}
	onOps := r.deliveredTo(ops.Config["url"])
	if len(onOps) != 1 || !onOps[0].alert.ReplacesAlert {
		t.Fatalf("ops never got the alert; expected one message for the whole outage, got %v", titles(onOps))
	}
}

// TestGroupedAlertLosesOnlyTheRecoveredMember: the rule is per monitor in a
// batch, not per row. A shared outage is one row naming two incidents, and
// only the first is in the row's own incident column. When the second
// monitor recovers while that row is still being retried, its news leaves
// the row and goes with its recovery; the first monitor's alert stays.
func TestGroupedAlertLosesOnlyTheRecoveredMember(t *testing.T) {
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
		t.Fatalf("expected api's alert and web's merged recovery, got %v", titles(got))
	}
	if first := got[0].alert; first.Grouped() || first.IncidentID != incA.ID || !first.Down() {
		t.Errorf("the first message is %q, want api's alert alone", first.Title())
	}
	if second := got[1].alert; second.IncidentID != incB.ID || !second.ReplacesAlert {
		t.Errorf("the second message is %q, want web's recovery replacing its alert", second.Title())
	}
	if firstAbout(got, incB.ID, true) >= 0 {
		t.Errorf("web was reported down after it was back up: %v", titles(got))
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

// TestReplacedRecoveryWording: a recovery that stands in for its alert says
// both halves, because it is the only message the channel gets about that
// outage. A grouped one says so only when every member replaces its alert.
func TestReplacedRecoveryWording(t *testing.T) {
	up := smsAlert(state.EventIncidentResolved)
	up.ReplacesAlert = true

	if got, want := up.Title(), "Production API was down for 12 minutes, now back up"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if body := up.Body(); !strings.Contains(body, "Down from 2026-09-29 12:03 UTC to 12:15 UTC") || !strings.Contains(body, "timeout after 10s") {
		t.Errorf("body = %q, want the failure and when the outage ran", body)
	}
	noStart := up
	noStart.StartedAt = time.Time{}
	if got, want := noStart.Title(), "Production API was down and is back up"; got != want {
		t.Errorf("title without a start = %q, want %q", got, want)
	}
	if got, want := smsText(up, "Europe/Amsterdam", smsSeptets), "UP Production API: timeout after 10s (down 12 min)"; got != want {
		t.Errorf("sms = %q, want %q", got, want)
	}

	// An outages-only SMS channel never heard the outage: it gets this.
	s := NewSMSSender(nil)
	cfg := map[string]string{"recoveries": "false", "numbers": "+316****5678"}
	if reason := s.Withhold(cfg, up, time.Now()); reason != "" {
		t.Errorf("outages-only channel withheld the only message about an outage: %q", reason)
	}

	other := smsAlert(state.EventIncidentResolved)
	other.MonitorID, other.MonitorName, other.IncidentID = 8, "Web", 2
	up.IncidentID = 1
	all := Alert{Members: []Alert{up, other}}
	if markReplaced(&all, map[int64]bool{1: true}) || all.ReplacesAlert {
		t.Errorf("a batch with one plain recovery is marked as replacing its alerts")
	}
	if !all.Members[0].ReplacesAlert || all.Members[1].ReplacesAlert {
		t.Errorf("members marked %v, %v; want only the first", all.Members[0].ReplacesAlert, all.Members[1].ReplacesAlert)
	}
	if got := Summarise(all.Members).Title(); got != "2 monitors are back up" {
		t.Errorf("mixed batch title = %q", got)
	}
	// Marked once, it stays marked: a later pass with nothing left to
	// take does not turn it back into a plain recovery.
	again := up
	if !markReplaced(&again, nil) {
		t.Errorf("a second pass cleared the flag on a recovery that replaced its alert")
	}

	both := Alert{Members: []Alert{up, other}}
	markReplaced(&both, map[int64]bool{1: true, 2: true})
	if got := Summarise(both.Members).Title(); got != "2 monitors were down and are back up" {
		t.Errorf("replaced batch title = %q", got)
	}
}
