package notifier

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// The point of grouping is that a shared outage produces one message, not
// twenty. These tests describe that from the outside: alerts go in through
// Enqueue, and what comes out is counted in the outbox.

// testClock is a hand-wound clock. Grouping is defined in terms of a window,
// and a test that waited real seconds for a window to close would be both slow
// and flaky — the two properties that make people stop trusting a suite.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// groupDB opens an empty store. Unlike testDB it creates no monitor or
// channel, because these tests need several of each.
func groupDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(t.TempDir(), "group.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func groupChannel(t *testing.T, db *store.DB, name string) store.Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), store.Channel{
		Name:    name,
		Type:    store.ChannelWebhook,
		Config:  map[string]string{"url": "https://example.com/" + name},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return ch
}

func groupMonitor(t *testing.T, db *store.DB, name string, channels ...int64) store.Monitor {
	t.Helper()
	ctx := context.Background()
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name:      name,
		Type:      "http",
		Target:    "https://example.com/" + name,
		IntervalS: 60,
		TimeoutS:  10,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create monitor %s: %v", name, err)
	}
	if len(channels) > 0 {
		if err := db.SetMonitorChannels(ctx, m.ID, channels); err != nil {
			t.Fatalf("assign channels to %s: %v", name, err)
		}
	}
	return m
}

func groupNotifier(t *testing.T, db *store.DB, clock *testClock, window time.Duration) *Notifier {
	t.Helper()
	return New(Options{
		DB:          db,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         clock.Now,
		GroupWindow: window,
	})
}

// countDeliveries counts outbox rows regardless of when they are due.
func countDeliveries(t *testing.T, db *store.DB) int {
	t.Helper()
	rows, err := db.DueDeliveries(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 500)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return len(rows)
}

// decodeOnlyDelivery asserts there is exactly one row and returns its alert.
func decodeOnlyDelivery(t *testing.T, db *store.DB) Alert {
	t.Helper()
	rows, err := db.DueDeliveries(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 500)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", len(rows))
	}
	var a Alert
	if err := json.Unmarshal([]byte(rows[0].Payload), &a); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return a
}

// TestSharedOutageSendsOneMessage is the ticket in one test. Twenty monitors
// fail inside the window; the channel is told once.
func TestSharedOutageSendsOneMessage(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 90*time.Second)
	ch := groupChannel(t, db, "ops")

	for i := 0; i < 20; i++ {
		m := groupMonitor(t, db, "edge-"+string(rune('a'+i)), ch.ID)
		inc := openIncident(t, db, m.ID, clock.Now(), "no such host")
		if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		// Monitors on a 60s schedule do not fail in the same instant;
		// they fail across the following minute as each turn comes up.
		clock.Advance(2 * time.Second)
	}

	if got := countDeliveries(t, db); got != 0 {
		t.Fatalf("expected nothing sent while the window is open, got %d rows", got)
	}

	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())

	if got := countDeliveries(t, db); got != 1 {
		t.Fatalf("expected one grouped delivery, got %d", got)
	}

	alert := decodeOnlyDelivery(t, db)
	if len(alert.GroupedNames) != 20 {
		t.Fatalf("expected 20 monitors in the group, got %d", len(alert.GroupedNames))
	}
	if !strings.Contains(alert.Title(), "20 monitors are down") {
		t.Fatalf("title does not say what happened: %q", alert.Title())
	}
}

// TestSingleAlertIsNotDressedUpAsAGroup guards the common case. One monitor
// failing on its own must read exactly as it did before grouping existed.
func TestSingleAlertIsNotDressedUpAsAGroup(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 30*time.Second)

	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "connection refused")

	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clock.Advance(time.Minute)
	n.flushDue(context.Background())

	alert := decodeOnlyDelivery(t, db)
	if alert.Grouped() {
		t.Fatal("a lone alert must not be marked as grouped")
	}
	if got, want := alert.Title(), "api is down"; got != want {
		t.Fatalf("title changed for an ungrouped alert: got %q, want %q", got, want)
	}
}

// TestRecoveriesDoNotJoinOutages checks the split by direction. "Three broke
// and one came back" in one message is not something anyone can act on.
func TestRecoveriesDoNotJoinOutages(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 60*time.Second)

	ch := groupChannel(t, db, "ops")
	down := groupMonitor(t, db, "down-one", ch.ID)
	up := groupMonitor(t, db, "up-one", ch.ID)

	downInc := openIncident(t, db, down.ID, clock.Now(), "timeout")
	upInc := openIncident(t, db, up.ID, clock.Now(), "timeout")

	if err := n.Enqueue(context.Background(), down, downInc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue down: %v", err)
	}
	if err := n.Enqueue(context.Background(), up, upInc, state.EventIncidentResolved, clock.Now()); err != nil {
		t.Fatalf("enqueue up: %v", err)
	}

	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())

	if got := countDeliveries(t, db); got != 2 {
		t.Fatalf("expected an outage message and a recovery message, got %d", got)
	}
}

// TestSeparateChannelsStaySeparate: one channel may be a phone and another a
// log. Merging across them would send one of them the wrong thing.
func TestSeparateChannelsStaySeparate(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 60*time.Second)

	phone := groupChannel(t, db, "phone")
	logbook := groupChannel(t, db, "logbook")

	m := groupMonitor(t, db, "api", phone.ID, logbook.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())

	if got := countDeliveries(t, db); got != 2 {
		t.Fatalf("expected one delivery per channel, got %d", got)
	}
}

// TestSharedCauseIsNamedOnlyWhenShared. Agreement across twenty monitors is
// evidence worth printing; disagreement is not, and inventing a common cause
// would be worse than staying quiet.
func TestSharedCauseIsNamedOnlyWhenShared(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	same := []Alert{
		{MonitorName: "a", Cause: "no such host", At: base, Event: string(state.EventIncidentConfirmed)},
		{MonitorName: "b", Cause: "no such host", At: base.Add(time.Second), Event: string(state.EventIncidentConfirmed)},
	}
	if got := Summarise(same).GroupedCause; got != "no such host" {
		t.Fatalf("a cause every member agrees on should be named, got %q", got)
	}

	mixed := []Alert{
		{MonitorName: "a", Cause: "no such host", At: base, Event: string(state.EventIncidentConfirmed)},
		{MonitorName: "b", Cause: "connection refused", At: base.Add(time.Second), Event: string(state.EventIncidentConfirmed)},
	}
	if got := Summarise(mixed).GroupedCause; got != "" {
		t.Fatalf("disagreeing causes must not be summarised, got %q", got)
	}
}

// TestLongGroupIsCapped: sixty names is a wall of text nobody reads. The
// count in the title carries the scale instead.
func TestLongGroupIsCapped(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var alerts []Alert
	for i := 0; i < 60; i++ {
		alerts = append(alerts, Alert{
			MonitorName: "monitor-" + string(rune('a'+i%26)),
			At:          base.Add(time.Duration(i) * time.Second),
			Event:       string(state.EventIncidentConfirmed),
		})
	}

	body := Summarise(alerts).Body()
	if strings.Count(body, "•") > 10 {
		t.Fatalf("expected at most 10 named monitors, body was:\n%s", body)
	}
	if !strings.Contains(body, "and 50 more") {
		t.Fatalf("the remainder should still be counted, body was:\n%s", body)
	}
}

// TestShutdownFlushesPendingAlerts. An alert inside its window is a real event
// that already happened; a restart must not swallow it.
func TestShutdownFlushesPendingAlerts(t *testing.T) {
	t.Parallel()

	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 10*time.Minute)

	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	inc := openIncident(t, db, m.ID, clock.Now(), "timeout")

	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := countDeliveries(t, db); got != 0 {
		t.Fatalf("precondition: expected nothing sent yet, got %d", got)
	}

	// Well inside the ten-minute window.
	n.flushAll(context.Background())

	if got := countDeliveries(t, db); got != 1 {
		t.Fatalf("a pending alert was lost at shutdown, got %d rows", got)
	}
}

// TestOrderFollowsWhatHappened: the first line should name what most likely
// caused the rest, so the batch reads oldest failure first.
func TestOrderFollowsWhatHappened(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	alerts := []Alert{
		{MonitorName: "late", At: base.Add(30 * time.Second), Event: string(state.EventIncidentConfirmed)},
		{MonitorName: "first", At: base, Event: string(state.EventIncidentConfirmed)},
		{MonitorName: "middle", At: base.Add(10 * time.Second), Event: string(state.EventIncidentConfirmed)},
	}

	got := Summarise(alerts).GroupedNames
	want := []string{"first", "middle", "late"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("group order: got %v, want %v", got, want)
		}
	}
}

// TestGroupWindowModes pins both ends of the zero convention. GroupingDisabled
// is the setting an operator reaches for when they watch a handful of
// services: there is nothing to group, so the window is pure delay and the
// alert must reach the outbox before any window could have elapsed. A zero
// GroupWindow means the opposite, because every other field in Options reads
// zero as "use the default" and a caller omitting the field is not asking for
// a behaviour change — the config layer is what turns an operator's 0 into
// GroupingDisabled.
func TestGroupWindowModes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		window  time.Duration
		monitor string
		// hold is the window the notifier is expected to apply. Zero means
		// the alert is written straight away.
		hold time.Duration
	}{
		{name: "grouping disabled writes immediately", window: GroupingDisabled, monitor: "alpha"},
		{name: "zero window means the default", window: 0, monitor: "bravo", hold: DefaultGroupWindow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := groupDB(t)
			clock := newTestClock()
			n := groupNotifier(t, db, clock, tc.window)
			ch := groupChannel(t, db, "ops")

			m := groupMonitor(t, db, tc.monitor, ch.ID)
			inc := openIncident(t, db, m.ID, clock.Now(), "no such host")
			if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
				t.Fatalf("enqueue: %v", err)
			}

			if tc.hold == 0 {
				if got := countDeliveries(t, db); got != 1 {
					t.Fatalf("expected the alert in the outbox at once, got %d rows", got)
				}
				alert := decodeOnlyDelivery(t, db)
				if len(alert.GroupedNames) != 0 {
					t.Fatalf("an ungrouped alert must not carry group members, got %v", alert.GroupedNames)
				}
				return
			}

			if got := countDeliveries(t, db); got != 0 {
				t.Fatalf("a %s window must still batch, got %d rows already in the outbox", tc.window, got)
			}

			// Just short of the window: still held.
			clock.Advance(tc.hold - time.Second)
			n.flushDue(context.Background())
			if got := countDeliveries(t, db); got != 0 {
				t.Fatalf("flushed %d rows before the window closed", got)
			}

			clock.Advance(2 * time.Second)
			n.flushDue(context.Background())
			if got := countDeliveries(t, db); got != 1 {
				t.Fatalf("expected one delivery after the window, got %d", got)
			}
		})
	}
}
