package notifier

import (
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
	"testing"
	"time"
)

func TestMaintenanceFiltersGroupedDelivery(t *testing.T) {
	ctx := t.Context()
	db := groupDB(t)
	ch := groupChannel(t, db, "ops")
	a := groupMonitor(t, db, "maintained", ch.ID)
	b := groupMonitor(t, db, "live", ch.ID)
	now := time.Now().UTC().Truncate(time.Second)
	sender := &fakeSender{}
	n := New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: time.Minute})
	for _, m := range []store.Monitor{a, b} {
		if err := n.Enqueue(ctx, m, store.Incident{}, state.EventIncidentConfirmed, now); err != nil {
			t.Fatal(err)
		}
	}
	// Persist the group before maintenance is added, then simulate restart.
	n.flushAll(ctx)
	w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: a.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	now = w.CreatedAt
	n = New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }})
	if _, err := n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got := sender.delivered()
	if len(got) != 1 || got[0].MonitorName != "live" || got[0].Grouped() {
		t.Fatalf("maintained member leaked into delivery: %+v", got)
	}
}
func TestMaintenanceSuppressesEveryEvent(t *testing.T) {
	for _, event := range []state.Event{state.EventIncidentConfirmed, state.EventIncidentResolved, state.EventIncidentReminder} {
		t.Run(string(event), func(t *testing.T) {
			ctx := t.Context()
			db, m, _ := testDB(t)
			now := time.Now().UTC().Truncate(time.Second)
			sender := &fakeSender{}
			w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			now = w.CreatedAt
			n := New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: GroupingDisabled})
			if err := n.Enqueue(ctx, m, store.Incident{}, event, now); err != nil {
				t.Fatal(err)
			}
			if _, err := n.sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if sender.attemptCount() != 0 {
				t.Fatal("maintenance alert sent")
			}
		})
	}
}

func TestMaintenanceSuppressesQueuedReminderWithoutSilencingRecovery(t *testing.T) {
	ctx := t.Context()
	db, m, _ := testDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	inc, err := db.OpenIncident(ctx, m.ID, now, "status", "503")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmIncident(ctx, m.ID, now, "status", "503"); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	n := New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: GroupingDisabled})
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentReminder, now); err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	now = w.CreatedAt
	if _, err := n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.attemptCount() != 0 {
		t.Fatal("queued reminder escaped maintenance")
	}
	pending, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil || pending.MaintenancePending {
		t.Fatal("suppressing a reminder erased the previously announced incident")
	}
	if err := db.DeleteMaintenance(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.attemptCount() != 0 {
		t.Fatal("suppressed reminder replayed after maintenance")
	}
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentResolved, now); err != nil {
		t.Fatal(err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.attemptCount() != 1 {
		t.Fatal("announced incident recovery was suppressed")
	}
}
