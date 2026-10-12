package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// A grouped alert, a grouped recovery or a digest is one outbox row per
// channel filed under its first monitor. Deleting that monitor must take only
// what speaks for it alone, never the message about the monitors that remain.

// groupedPayload is what the notifier writes for a batch: monitor_id 0 at the
// top level and each monitor's own alert in members.
func groupedPayload(members ...[2]int64) string {
	out := `{"members":[`
	for i, m := range members {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf(`{"monitor_id":%d,"incident_id":%d,"event":"incident_confirmed"}`, m[0], m[1])
	}
	return out + `],"monitor_id":0,"event":"incident_confirmed"}`
}

func TestDeleteMonitorKeepsAGroupedMessageForTheOthers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := seedMonitor(t, db, "api")
	web := seedMonitor(t, db, "web")
	ch := healthChannel(t, db, "ops")

	start := time.Now().Add(-time.Hour)
	apiInc := seedIncidentAt(t, db, api, start)
	webInc := seedIncidentAt(t, db, web, start)

	enqueue := func() Delivery {
		t.Helper()
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: api, IncidentID: apiInc.ID,
			Event: "incident_confirmed", Payload: groupedPayload([2]int64{api, apiInc.ID}, [2]int64{web, webInc.ID})})
		if err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
		return d
	}
	sent, queued := enqueue(), enqueue()
	if err := db.MarkDelivered(ctx, sent.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	if err := db.DeleteMonitor(ctx, api); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	for _, d := range []Delivery{sent, queued} {
		got, err := db.GetDelivery(ctx, d.ID)
		if err != nil {
			t.Fatalf("delivery %d about web went with api: %v", d.ID, err)
		}
		if got.MonitorID != web || got.IncidentID != webInc.ID || got.Payload != d.Payload {
			t.Errorf("delivery %d = monitor %d, incident %d, payload %s; want it filed under web (%d, %d) and said as it was",
				d.ID, got.MonitorID, got.IncidentID, got.Payload, web, webInc.ID)
		}
	}
	due, err := db.DueDeliveries(ctx, time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("DueDeliveries: %v", err)
	}
	if len(due) != 1 || due[0].ID != queued.ID {
		t.Errorf("due = %+v, want the queued grouped alert %d still going out", due, queued.ID)
	}
	history, err := db.IncidentDeliveries(ctx, webInc)
	if err != nil {
		t.Fatalf("IncidentDeliveries: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("web's incident lists %d deliveries after api was deleted, want both grouped messages", len(history))
	}
}

func TestDeleteMonitorTakesTheMessagesThatSpeakForItAlone(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := seedMonitor(t, db, "api")
	web := seedMonitor(t, db, "web")
	ch := healthChannel(t, db, "ops")

	var ids []int64
	for _, payload := range []string{
		fmt.Sprintf(`{"monitor_id":%d,"event":"incident_confirmed"}`, api),
		// A digest of api's night only, nested as members.
		groupedPayload([2]int64{api, 0}, [2]int64{api, 0}),
		"{}",
		"not json",
	} {
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: api, Event: "incident_confirmed", Payload: payload})
		if err != nil {
			t.Fatalf("EnqueueDelivery(%s): %v", payload, err)
		}
		ids = append(ids, d.ID)
	}
	webOwn, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: web, Event: "incident_confirmed",
		Payload: fmt.Sprintf(`{"monitor_id":%d}`, web)})
	if err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}

	if err := db.DeleteMonitor(ctx, api); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	for _, id := range ids {
		if d, err := db.GetDelivery(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("delivery %d (%s) outlived the only monitor it speaks for: %v", id, d.Payload, err)
		}
	}
	if got, err := db.GetDelivery(ctx, webOwn.ID); err != nil || got.MonitorID != web {
		t.Errorf("web's own delivery = %+v, %v; deleting api must not touch it", got, err)
	}
}

func TestEnqueueDeliveryFilesAGroupUnderAMonitorThatRemains(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := seedMonitor(t, db, "api")
	web := seedMonitor(t, db, "web")
	ch := healthChannel(t, db, "ops")

	// The batch was started by api, which was deleted while the batch
	// waited for company.
	if err := db.DeleteMonitor(ctx, api); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: api, Event: "incident_confirmed",
		Payload: groupedPayload([2]int64{api, 0}, [2]int64{web, 0})})
	if err != nil {
		t.Fatalf("EnqueueDelivery of a group whose first monitor is gone: %v", err)
	}
	if got, err := db.GetDelivery(ctx, d.ID); err != nil || got.MonitorID != web || d.MonitorID != web {
		t.Errorf("delivery = %+v (returned monitor %d), %v; want it filed under web (%d)", got, d.MonitorID, err, web)
	}

	// Nothing it speaks for is left: nothing is written.
	for _, payload := range []string{groupedPayload([2]int64{api, 0}), "not json"} {
		_, err = db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: api, Event: "incident_confirmed", Payload: payload})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("EnqueueDelivery(%s) for deleted monitors only = %v, want ErrNotFound", payload, err)
		}
	}
	due, err := db.DueDeliveries(ctx, time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("DueDeliveries: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("queued %d deliveries, want only the one that still speaks for web", len(due))
	}
}

// A batch can also outlive its channel. Deleting a channel takes its queued
// messages, so the one still waiting is not written either, and the refusal
// names the channel rather than surfacing as a foreign-key failure.
func TestEnqueueDeliveryForADeletedChannelWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := seedMonitor(t, db, "api")
	kept := healthChannel(t, db, "kept")
	gone := healthChannel(t, db, "gone")
	if err := db.DeleteChannel(ctx, gone); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	_, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: gone, MonitorID: api, Event: "incident_confirmed",
		Payload: groupedPayload([2]int64{api, 0})})
	if !errors.Is(err, ErrUnknownChannel) || errors.Is(err, ErrNotFound) {
		t.Fatalf("EnqueueDelivery to a deleted channel = %v, want ErrUnknownChannel", err)
	}
	// The channel is checked before the monitors: with both gone, the
	// channel is the reason given.
	if err := db.DeleteMonitor(ctx, api); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	_, err = db.EnqueueDelivery(ctx, Delivery{ChannelID: gone, MonitorID: api, Event: "incident_confirmed", Payload: "{}"})
	if !errors.Is(err, ErrUnknownChannel) {
		t.Errorf("EnqueueDelivery with channel and monitor gone = %v, want ErrUnknownChannel", err)
	}
	// A channel that exists still gets ErrNotFound for monitors that do not.
	_, err = db.EnqueueDelivery(ctx, Delivery{ChannelID: kept, MonitorID: api, Event: "incident_confirmed", Payload: "{}"})
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnknownChannel) {
		t.Errorf("EnqueueDelivery for a deleted monitor = %v, want ErrNotFound", err)
	}

	due, err := db.DueDeliveries(ctx, time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("DueDeliveries: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("queued %d deliveries, want none", len(due))
	}
}
