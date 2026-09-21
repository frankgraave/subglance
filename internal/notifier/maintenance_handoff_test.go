package notifier

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func TestMaintenanceHandoffSurvivesEnqueueFailureAndRestart(t *testing.T) {
	ctx := t.Context()
	db, m, first := testDB(t)
	second := groupChannel(t, db, "second")
	if err := db.SetMonitorChannels(ctx, m.ID, []int64{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer target.Close()
	m.Target, m.Retries = target.URL, 1
	now := time.Now().UTC().Truncate(time.Second)
	w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var enqueueErr error
	n := New(Options{DB: db}) // Default grouping must not keep deferred intent only in RAM.
	newRunner := func() *monitor.Runner {
		return monitor.New(monitor.Options{DB: db, AllowPrivateTargets: true, Notify: func(a monitor.Alert) { enqueueErr = n.Enqueue(ctx, a.Monitor, a.Incident, a.Event, a.At) }})
	}
	r := newRunner()
	if _, err = r.CheckNow(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteMaintenance(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER reject_outbox BEFORE INSERT ON notif_outbox WHEN NEW.channel_id=%d BEGIN SELECT RAISE(ABORT,'injected disk failure'); END`, second.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = r.CheckNow(ctx, m); err != nil {
		t.Fatal(err)
	}
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil || !inc.MaintenancePending {
		t.Fatalf("enqueue failure lost durable intent: %+v %v (enqueue %v)", inc, err, enqueueErr)
	}
	if enqueueErr == nil {
		t.Fatal("enqueue failure was hidden")
	}
	if _, err = db.Writer.ExecContext(ctx, `DROP TRIGGER reject_outbox`); err != nil {
		t.Fatal(err)
	}
	// Reopen the physical database and discard the notifier without flushing.
	due, err := db.DueDeliveries(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("partial handoff survived rollback: %v %v", due, err)
	}
	path := db.Path()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n = New(Options{DB: db})
	r = newRunner()
	if _, err = r.CheckNow(ctx, m); err != nil {
		t.Fatal(err)
	}
	due, err = db.DueDeliveries(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("restart lost initial alert: %v %v", due, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err = r.CheckNow(ctx, m); err != nil {
		t.Fatal(err)
	}
	due, err = db.DueDeliveries(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("duplicate handoff: %v %v", due, err)
	}
}

func TestMaintenanceChannelIsolation(t *testing.T) {
	for _, recovers := range []bool{false, true} {
		t.Run(map[bool]string{false: "still down", true: "recovered"}[recovers], func(t *testing.T) {
			ctx := context.Background()
			db := groupDB(t)
			first, second := groupChannel(t, db, "first"), groupChannel(t, db, "second")
			m := groupMonitor(t, db, "service", first.ID, second.ID)
			now := time.Now().UTC().Truncate(time.Second)
			var healthy atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if healthy.Load() {
					w.WriteHeader(200)
				} else {
					w.WriteHeader(503)
				}
			}))
			defer target.Close()
			m.Target, m.Retries = target.URL, 1
			sender := &fakeSender{}
			n := New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: GroupingDisabled})
			r := monitor.New(monitor.Options{DB: db, AllowPrivateTargets: true, Notify: func(a monitor.Alert) {
				if err := n.Enqueue(ctx, a.Monitor, a.Incident, a.Event, a.At); err != nil {
					t.Error(err)
				}
			}})
			if _, err := r.CheckNow(ctx, m); err != nil {
				t.Fatal(err)
			}
			inc, err := db.OpenIncidentFor(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}

			due, err := db.DueDeliveries(ctx, now.Add(time.Hour), 10)
			if err != nil || len(due) != 2 {
				t.Fatalf("queued %v %v", due, err)
			}
			n.attempt(ctx, due[0]) // First channel received its initial alert.
			sender.err = &Retryable{Err: context.DeadlineExceeded}
			n.attempt(ctx, due[1]) // Second channel schedules a retry.
			sender.err = nil
			w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			n.attempt(ctx, due[1]) // The retry encounters maintenance.
			inc, err = db.OpenIncidentFor(ctx, m.ID)
			if err != nil || inc.MaintenancePending {
				t.Fatalf("channel suppression poisoned incident: %+v %v", inc, err)
			}
			// A deferred callback can meet another active window. It must keep
			// the channel scope instead of recreating incident-wide suppression.
			if err = n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, now); err != nil {
				t.Fatal(err)
			}
			inc, err = db.OpenIncidentFor(ctx, m.ID)
			if err != nil || inc.MaintenancePending {
				t.Fatalf("reentered maintenance poisoned incident: %+v %v", inc, err)
			}
			if err = db.DeleteMaintenance(ctx, w.ID); err != nil {
				t.Fatal(err)
			}
			n = New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now.Add(2 * time.Minute) }, GroupWindow: GroupingDisabled})
			healthy.Store(recovers)
			if _, err = r.CheckNow(ctx, m); err != nil {
				t.Fatal(err)
			}
			if _, err = n.sweep(ctx); err != nil {
				t.Fatal(err)
			}
			var firstCount, secondCount int
			if err = db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM notif_outbox WHERE channel_id=? AND status='delivered'`, first.ID).Scan(&firstCount); err != nil {
				t.Fatal(err)
			}
			if err = db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM notif_outbox WHERE channel_id=? AND status='delivered'`, second.ID).Scan(&secondCount); err != nil {
				t.Fatal(err)
			}
			wantFirst, wantSecond := 1, 1
			if recovers {
				wantFirst, wantSecond = 2, 0
			}
			if firstCount != wantFirst || secondCount != wantSecond {
				t.Fatalf("cross-channel delivery: first=%d second=%d want %d/%d", firstCount, secondCount, wantFirst, wantSecond)
			}
		})
	}
}

func TestMaintenanceGroupedIntentAndPayloadAreAtomic(t *testing.T) {
	ctx := t.Context()
	db := groupDB(t)
	ch := groupChannel(t, db, "ops")
	a, b := groupMonitor(t, db, "maintained", ch.ID), groupMonitor(t, db, "live", ch.ID)
	now := time.Now().UTC().Truncate(time.Second)
	sender := &fakeSender{}
	n := New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: time.Minute})
	var held store.Incident
	for _, m := range []store.Monitor{a, b} {
		inc, err := db.OpenIncident(ctx, m.ID, now, "status", "503")
		if err != nil {
			t.Fatal(err)
		}
		if err = db.ConfirmIncident(ctx, m.ID, now, "status", "503"); err != nil {
			t.Fatal(err)
		}
		if m.ID == a.ID {
			held = inc
		}
		if err = n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, now); err != nil {
			t.Fatal(err)
		}
	}
	n.flushAll(ctx)
	w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: a.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.ExecContext(ctx, `CREATE TRIGGER reject_filter BEFORE UPDATE ON notif_outbox BEGIN SELECT RAISE(ABORT,'filter disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.attemptCount() != 0 {
		t.Fatal("sent before persisting filter")
	}
	pending, err := db.HasPendingMaintenanceChannels(ctx, held.ID)
	if err != nil || pending {
		t.Fatalf("intent escaped filter rollback: %v %v", pending, err)
	}
	due, err := db.DueDeliveries(ctx, now.Add(time.Hour), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("queue %v %v", due, err)
	}
	original, err := DecodeAlert(due[0].Payload)
	if err != nil || len(original.Members) != 2 {
		t.Fatalf("payload lost on rollback: %+v %v", original, err)
	}
	if _, err = db.Writer.ExecContext(ctx, `DROP TRIGGER reject_filter`); err != nil {
		t.Fatal(err)
	}
	if _, err = n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got := sender.delivered()
	if len(got) != 1 || got[0].MonitorID != b.ID || got[0].Grouped() {
		t.Fatalf("group scope: %+v", got)
	}
	pending, err = db.HasPendingMaintenanceChannels(ctx, held.ID)
	if err != nil || !pending {
		t.Fatalf("group member lost intent: %v %v", pending, err)
	}
	if err = db.DeleteMaintenance(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	n = New(Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now.Add(time.Minute) }})
	if err = n.Enqueue(ctx, a, held, state.EventIncidentConfirmed, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got = sender.delivered()
	if len(got) != 2 || got[1].MonitorID != a.ID {
		t.Fatalf("group member release: %+v", got)
	}
}
