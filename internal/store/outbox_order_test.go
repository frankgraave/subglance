package store

import (
	"context"
	"testing"
	"time"
)

// The two queries the notifier keeps a channel's messages in order with.

func TestPendingBeforeListsOnlyWhatIsStillGoingOut(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ops := healthChannel(t, db, "ops")
	phone := healthChannel(t, db, "phone")

	enqueue := func(ch int64) Delivery {
		t.Helper()
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: "incident_confirmed", Payload: "{}"})
		if err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
		return d
	}

	retrying := enqueue(ops)
	if err := db.MarkRetry(ctx, retrying.ID, "503", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	held := enqueue(ops)
	if err := db.HoldDelivery(ctx, held.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("HoldDelivery: %v", err)
	}
	failed := enqueue(ops)
	if err := db.MarkFailed(ctx, failed.ID, "gave up"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	withheld := enqueue(ops)
	if err := db.WithholdDelivery(ctx, withheld.ID, "over the hourly limit"); err != nil {
		t.Fatalf("WithholdDelivery: %v", err)
	}
	sent := enqueue(ops)
	if err := db.MarkDelivered(ctx, sent.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	enqueue(phone) // another channel
	self := enqueue(ops)
	enqueue(ops) // queued after self

	got, err := db.PendingBefore(ctx, ops, self.ID)
	if err != nil {
		t.Fatalf("PendingBefore: %v", err)
	}
	var ids []int64
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if len(ids) != 2 || ids[0] != retrying.ID || ids[1] != held.ID {
		t.Fatalf("PendingBefore = %v, want only the retrying and the held row, %d and %d", ids, retrying.ID, held.ID)
	}
}

func TestPostponeDeliveryChargesNothing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "ops")

	d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: "incident_resolved", Payload: "{}"})
	if err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	if err := db.MarkRetry(ctx, d.ID, "unexpected status 503", time.Now()); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	until := time.Now().Add(42 * time.Second).Truncate(time.Second)
	if err := db.PostponeDelivery(ctx, d.ID, until); err != nil {
		t.Fatalf("PostponeDelivery: %v", err)
	}

	got, err := db.GetDelivery(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDelivery: %v", err)
	}
	if !got.NextAttemptAt.Equal(until) {
		t.Errorf("next attempt %s, want %s", got.NextAttemptAt, until)
	}
	if got.Attempts != 1 || got.LastError != "unexpected status 503" || got.Status != OutboxPending {
		t.Errorf("postponed row = attempts %d, error %q, status %s; want 1, the old error, pending",
			got.Attempts, got.LastError, got.Status)
	}
}
