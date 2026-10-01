package store

import (
	"context"
	"testing"
	"time"
)

// The channel health a notifications row is drawn from. These tests pin the
// two clocks it depends on: a delivery's outcome is counted by when it
// finished, which is the clock retention prunes by, and a delivery still
// waiting is counted whatever its age.

func healthChannel(t *testing.T, db *DB, name string) int64 {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), Channel{
		Name: name, Type: ChannelWebhook, Enabled: true,
		Config: map[string]string{"url": "https://example.com/hook"},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	return ch.ID
}

// seedOutcome writes one delivery that finished at the given moment, after
// being queued long before it: an alert retried for an hour, or a backlog
// drained after an outage.
func seedOutcome(t *testing.T, db *DB, monitor, channel int64, status string, attempts int, lastErr string, queued, finished time.Time) {
	t.Helper()
	if _, err := db.SeedDelivery(context.Background(), Delivery{
		ChannelID: channel, MonitorID: monitor, Event: "incident_confirmed", Payload: "{}",
		Status: status, Attempts: attempts, LastError: lastErr,
		CreatedAt: queued, UpdatedAt: finished, NextAttemptAt: finished.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SeedDelivery: %v", err)
	}
}

func TestChannelHealthReportsTheNewestOutcomeOfEachKind(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "pager")

	now := time.Now().Truncate(time.Second)
	seedOutcome(t, db, m, ch, OutboxDelivered, 1, "", now.Add(-5*time.Hour), now.Add(-5*time.Hour))
	seedOutcome(t, db, m, ch, OutboxFailed, 5, "older failure", now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	seedOutcome(t, db, m, ch, OutboxFailed, 5, "newest failure", now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	seedOutcome(t, db, m, ch, OutboxDelivered, 1, "", now.Add(-time.Hour), now.Add(-time.Hour))

	health, err := db.ChannelHealthSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ChannelHealthSince: %v", err)
	}
	h := health[ch]
	if h.Failed != 2 {
		t.Errorf("Failed = %d, want 2", h.Failed)
	}
	if !h.LastDeliveredAt.Equal(now.Add(-time.Hour)) {
		t.Errorf("LastDeliveredAt = %v, want %v", h.LastDeliveredAt, now.Add(-time.Hour))
	}
	if !h.LastFailedAt.Equal(now.Add(-2 * time.Hour)) {
		t.Errorf("LastFailedAt = %v, want %v", h.LastFailedAt, now.Add(-2*time.Hour))
	}
	if h.LastError != "newest failure" {
		t.Errorf("LastError = %q, want the newest failure's message", h.LastError)
	}
}

func TestChannelHealthWindowIsWhenADeliveryFinished(t *testing.T) {
	// A delivery queued 40 days ago that gave up yesterday is a failure from
	// yesterday. Windowing by created_at hid it; the retention pass prunes by
	// updated_at, so the window has to be on the same clock or it either
	// shows a success that is about to vanish or hides a failure that is not.
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "pager")

	now := time.Now().Truncate(time.Second)
	since := now.Add(-DeliveryLogRetention)
	seedOutcome(t, db, m, ch, OutboxFailed, 5, "gave up late", now.Add(-40*24*time.Hour), now.Add(-24*time.Hour))
	seedOutcome(t, db, m, ch, OutboxDelivered, 1, "", now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour))

	health, err := db.ChannelHealthSince(ctx, since)
	if err != nil {
		t.Fatalf("ChannelHealthSince: %v", err)
	}
	h := health[ch]
	if h.Failed != 1 || h.LastError != "gave up late" {
		t.Errorf("health = %+v, want the late failure counted", h)
	}
	if !h.LastDeliveredAt.IsZero() {
		t.Errorf("LastDeliveredAt = %v, want zero: that success finished outside the window", h.LastDeliveredAt)
	}
}

func TestChannelHealthCountsEveryPendingDeliveryAndItsRetries(t *testing.T) {
	// Pending is work, not history: a backlog older than the window is the
	// one that most needs showing. Of the pending rows, only those that have
	// already failed an attempt are retrying, and the newest of their errors
	// is the channel's last error.
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "warehouse")

	now := time.Now().Truncate(time.Second)
	old := now.Add(-60 * 24 * time.Hour)
	seedOutcome(t, db, m, ch, OutboxPending, 2, "unexpected status 503", old, old.Add(time.Minute))
	seedOutcome(t, db, m, ch, OutboxPending, 0, "", now, now)

	health, err := db.ChannelHealthSince(ctx, now.Add(-DeliveryLogRetention))
	if err != nil {
		t.Fatalf("ChannelHealthSince: %v", err)
	}
	h, ok := health[ch]
	if !ok {
		t.Fatal("a channel with a backlog older than the window has no health at all")
	}
	if h.Pending != 2 || h.Retrying != 1 {
		t.Errorf("Pending = %d, Retrying = %d; want 2 and 1", h.Pending, h.Retrying)
	}
	if h.LastError != "unexpected status 503" {
		t.Errorf("LastError = %q, want the retrying delivery's error", h.LastError)
	}
}

func TestChannelHealthIgnoresWithheldDeliveries(t *testing.T) {
	// A delivery held back on purpose (maintenance, quiet hours dropped, an
	// SMS over its hourly limit) is not waiting and did not fail.
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "sms")

	d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: "incident_confirmed", Payload: "{}"})
	if err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	if err := db.MarkRetry(ctx, d.ID, "first try failed", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	if err := db.WithholdDelivery(ctx, d.ID, "over the hourly limit"); err != nil {
		t.Fatalf("WithholdDelivery: %v", err)
	}

	health, err := db.ChannelHealthSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ChannelHealthSince: %v", err)
	}
	if h := health[ch]; h.Pending != 0 || h.Retrying != 0 || h.LastError != "" {
		t.Errorf("health = %+v, want nothing pending, retrying or failing", h)
	}
}
