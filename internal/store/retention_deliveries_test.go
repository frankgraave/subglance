package store

import (
	"context"
	"testing"
	"time"
)

// seedAgedDelivery queues one delivery, moves it to the given terminal state
// and backdates its last update, so a pass with an injected clock sees it at
// a chosen age.
func seedAgedDelivery(t *testing.T, db *DB, monitor, channel int64, state string, updated time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: channel, MonitorID: monitor, Event: "incident_confirmed", Payload: "{}"})
	if err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	switch state {
	case "delivered":
		err = db.MarkDelivered(ctx, d.ID)
	case "failed":
		err = db.MarkFailed(ctx, d.ID, "boom")
	case "suppressed":
		err = db.SuppressDelivery(ctx, d.ID)
	case "suppressed-failed":
		// A delivery can still fail after maintenance flagged it: the
		// flag and the status are written independently.
		if err = db.SuppressDelivery(ctx, d.ID); err == nil {
			err = db.MarkFailed(ctx, d.ID, "boom")
		}
	case "pending":
	default:
		t.Fatalf("unknown state %q", state)
	}
	if err != nil {
		t.Fatalf("set %s: %v", state, err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE notif_outbox SET updated_at = ?, created_at = ? WHERE id = ?",
		updated.Unix(), updated.Unix(), d.ID); err != nil {
		t.Fatalf("backdate delivery: %v", err)
	}
	return d.ID
}

func outboxIDs(t *testing.T, db *DB) map[int64]bool {
	t.Helper()
	rows, err := db.Reader.QueryContext(context.Background(), "SELECT id FROM notif_outbox")
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	return out
}

func TestApplyRetentionPrunesTheDeliveryLog(t *testing.T) {
	db := openTestDB(t)
	monitor := seedMonitor(t, db, "delivery-log")
	channel := seedChannel(t, db, "delivery-log").ID

	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	old := now.Add(-DeliveryLogRetention - time.Hour)
	recent := now.Add(-DeliveryLogRetention + time.Hour)

	oldDelivered := seedAgedDelivery(t, db, monitor, channel, "delivered", old)
	oldSuppressed := seedAgedDelivery(t, db, monitor, channel, "suppressed", old)
	oldFailed := seedAgedDelivery(t, db, monitor, channel, "failed", old)
	oldPending := seedAgedDelivery(t, db, monitor, channel, "pending", old)
	oldSuppressedFailed := seedAgedDelivery(t, db, monitor, channel, "suppressed-failed", old)
	recentDelivered := seedAgedDelivery(t, db, monitor, channel, "delivered", recent)

	// Both windows at "forever": the delivery log is pruned regardless,
	// because a sent notification is not monitoring history.
	res, err := db.applyRetentionAt(context.Background(), now, RetentionPolicy{})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.Deliveries != 2 {
		t.Errorf("pruned %d deliveries, want 2 (the old delivered and suppressed rows)", res.Deliveries)
	}

	left := outboxIDs(t, db)
	for id, why := range map[int64]string{
		oldDelivered:  "delivered and past the window",
		oldSuppressed: "suppressed and past the window",
	} {
		if left[id] {
			t.Errorf("delivery %d survived: it is %s", id, why)
		}
	}
	for id, why := range map[int64]string{
		oldFailed:           "a failed row is evidence the operator may not have seen",
		oldPending:          "a pending row is still work",
		oldSuppressedFailed: "a failure is kept even under the maintenance flag",
		recentDelivered:     "it is inside the window",
	} {
		if !left[id] {
			t.Errorf("delivery %d was pruned, but %s", id, why)
		}
	}
}
