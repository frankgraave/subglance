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

// TestReplaceWithRecoveryTakesOverTheAlert: the merged recovery inherits the
// alert's retry state, the alert is closed with a note of where its news
// went, a grouped alert keeps its other monitors, and a row that went out in
// the meantime is left alone.
func TestReplaceWithRecoveryTakesOverTheAlert(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "ops")

	enqueue := func(event, payload string) Delivery {
		t.Helper()
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: event, Payload: payload})
		if err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
		return d
	}

	alert := enqueue("incident_confirmed", `{"event":"incident_confirmed"}`)
	next := time.Now().Add(90 * time.Second).Truncate(time.Second)
	if err := db.MarkRetry(ctx, alert.ID, "unexpected status 503", next); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	if err := db.MarkRetry(ctx, alert.ID, "unexpected status 503", next); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	grouped := enqueue("incident_confirmed", `{"members":[1,2]}`)
	sent := enqueue("incident_reminder", `{"event":"incident_reminder"}`)
	if err := db.MarkDelivered(ctx, sent.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	recovery := enqueue("incident_resolved", `{"event":"incident_resolved"}`)

	from, err := db.GetDelivery(ctx, alert.ID)
	if err != nil {
		t.Fatalf("GetDelivery: %v", err)
	}
	err = db.ReplaceWithRecovery(ctx, recovery.ID, `{"merged":true}`, from, []Replaced{
		{ID: alert.ID},
		{ID: grouped.ID, Payload: `{"members":[2]}`},
		{ID: sent.ID},
	})
	if err != nil {
		t.Fatalf("ReplaceWithRecovery: %v", err)
	}

	get := func(id int64) Delivery {
		t.Helper()
		d, err := db.GetDelivery(ctx, id)
		if err != nil {
			t.Fatalf("GetDelivery %d: %v", id, err)
		}
		return d
	}

	merged := get(recovery.ID)
	if merged.Payload != `{"merged":true}` || merged.Attempts != 2 || merged.LastError != "unexpected status 503" ||
		!merged.NextAttemptAt.Equal(next) || merged.Status != OutboxPending {
		t.Errorf("merged recovery = payload %s, attempts %d, error %q, next %s, status %s; want the merged payload on the alert's retry state",
			merged.Payload, merged.Attempts, merged.LastError, merged.NextAttemptAt, merged.Status)
	}

	due, err := db.DueDeliveries(ctx, next.Add(time.Second), 10)
	if err != nil {
		t.Fatalf("DueDeliveries: %v", err)
	}
	var ids []int64
	for _, d := range due {
		ids = append(ids, d.ID)
	}
	if len(ids) != 2 || ids[0] != grouped.ID || ids[1] != recovery.ID {
		t.Fatalf("due after the merge = %v, want the rest of the grouped alert %d and the recovery %d: the alert %d is closed",
			ids, grouped.ID, recovery.ID, alert.ID)
	}
	closed := get(alert.ID)
	if closed.Status != OutboxPending || closed.LastError == "unexpected status 503" {
		t.Errorf("replaced alert = status %s, error %q; want it closed with a note of where its news went", closed.Status, closed.LastError)
	}
	if rest := get(grouped.ID); rest.Payload != `{"members":[2]}` {
		t.Errorf("grouped alert payload = %s, want what it still has to say", rest.Payload)
	}
	if got := get(sent.ID); got.Status != OutboxDelivered || got.Payload != `{"event":"incident_reminder"}` {
		t.Errorf("a row that went out meanwhile was changed: status %s, payload %s", got.Status, got.Payload)
	}
}
