package notifier

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func TestMaintenanceFailuresDeferDelivery(t *testing.T) {
	for _, failure := range []string{"read", "decision write", "retry write"} {
		t.Run(failure, func(t *testing.T) {
			ctx := t.Context()
			db, m, _ := testDB(t)
			now := time.Now().UTC().Truncate(time.Second)
			sender := &fakeSender{}
			var logs bytes.Buffer
			n := New(Options{DB: db, Log: slog.New(slog.NewTextHandler(&logs, nil)), Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: GroupingDisabled, Interval: time.Second})
			if err := n.Enqueue(ctx, m, store.Incident{}, state.EventIncidentConfirmed, now); err != nil {
				t.Fatal(err)
			}
			var reset string
			switch failure {
			case "read":
				if _, err := db.Writer.ExecContext(ctx, `INSERT INTO maintenance_windows(spec) VALUES('invalid JSON')`); err != nil {
					t.Fatal(err)
				}
				reset = "DELETE FROM maintenance_windows"
			case "decision write", "retry write":
				columns := " OF suppressed"
				if failure == "retry write" {
					columns = ""
				}
				if _, err := db.Writer.ExecContext(ctx, `CREATE TRIGGER break_maintenance BEFORE UPDATE`+columns+` ON notif_outbox BEGIN SELECT RAISE(FAIL,'write unavailable'); END`); err != nil {
					t.Fatal(err)
				}
				reset = "DROP TRIGGER break_maintenance"
			}
			_, err := n.sweep(ctx)
			if failure == "retry write" {
				if err == nil {
					t.Fatal("failed retry write must reach sweep's backoff path")
				}
				logs.Reset()
				runCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				n.Run(runCtx)
				if count := strings.Count(logs.String(), "notification sweep failed"); count != 1 {
					t.Fatalf("persistent write failure spun %d sweeps: %s", count, logs.String())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				due, err := db.DueDeliveries(ctx, now, 10)
				if err != nil || len(due) != 0 {
					t.Fatalf("failed maintenance attempt remains immediately due: %+v %v", due, err)
				}
				deferred, err := db.DueDeliveries(ctx, now.Add(time.Minute), 10)
				if err != nil || len(deferred) != 1 || deferred[0].Attempts != 0 || deferred[0].LastError == "" || !deferred[0].NextAttemptAt.After(now) {
					t.Fatalf("lost retry: %+v %v", deferred, err)
				}
			}
			if sender.attemptCount() != 0 {
				t.Fatal("sent without maintenance decision")
			}
			if _, err := db.Writer.ExecContext(ctx, reset); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			if _, err := n.sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if sender.attemptCount() != 1 {
				t.Fatal("deferred delivery did not recover")
			}
		})
	}
}

// Schedule-store outages are prerequisites, not attempts to contact a channel.
// Even a long outage must leave the channel's actual delivery budget intact.
func TestMaintenanceDeferralPreservesDeliveryBudget(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		prior         int
	}{
		{"read before first send", "read", 0}, {"write before first send", "decision write", 0},
		{"read after sender failures", "read", 2}, {"write after sender failures", "decision write", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db, m, _ := testDB(t)
			now := time.Now().UTC().Truncate(time.Second)
			sender := &fakeSender{failFirst: tc.prior + 1, err: &Retryable{Err: errors.New("temporary receiver outage")}}
			options := Options{DB: db, Senders: map[string]Sender{store.ChannelWebhook: sender}, Now: func() time.Time { return now }, GroupWindow: GroupingDisabled}
			n := New(options)
			if err := n.Enqueue(ctx, m, store.Incident{}, state.EventIncidentConfirmed, now); err != nil {
				t.Fatal(err)
			}
			due, err := db.DueDeliveries(ctx, now, 10)
			if err != nil || len(due) != 1 {
				t.Fatalf("queued: %v %v", due, err)
			}
			id := due[0].ID
			for i := 0; i < tc.prior; i++ {
				if _, err := n.sweep(ctx); err != nil {
					t.Fatal(err)
				}
				d, err := db.GetDelivery(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if d.Attempts != i+1 || d.Status != store.OutboxPending {
					t.Fatalf("sender precondition: %+v", d)
				}
				now = d.NextAttemptAt
			}
			reset := "DELETE FROM maintenance_windows"
			fault := `INSERT INTO maintenance_windows(spec) VALUES('invalid JSON')`
			if tc.failure == "decision write" {
				fault = `CREATE TRIGGER break_maintenance BEFORE UPDATE OF suppressed ON notif_outbox BEGIN SELECT RAISE(FAIL,'write unavailable'); END`
				reset = "DROP TRIGGER break_maintenance"
			}
			if _, err := db.Writer.ExecContext(ctx, fault); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < maxAttempts+2; i++ {
				if _, err := n.sweep(ctx); err != nil {
					t.Fatal(err)
				}
				d, err := db.GetDelivery(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if d.Status != store.OutboxPending || d.LastError == "" || !d.NextAttemptAt.After(now) {
					t.Fatalf("lost deferral: %+v", d)
				}
				if d.Attempts != tc.prior {
					t.Errorf("maintenance failure %d consumed delivery attempts: %d", i+1, d.Attempts)
				}
				if sender.attemptCount() != tc.prior {
					t.Fatal("sent without maintenance evaluation")
				}
				now = d.NextAttemptAt
				n = New(options) // persisted deferral also survives notifier restart
			}
			if _, err := db.Writer.ExecContext(ctx, reset); err != nil {
				t.Fatal(err)
			}
			if _, err := n.sweep(ctx); err != nil {
				t.Fatal(err)
			}
			d, err := db.GetDelivery(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status != store.OutboxPending || d.Attempts != tc.prior+1 || sender.attemptCount() != tc.prior+1 {
				t.Fatalf("first real send exhausted budget: %+v, sends=%d", d, sender.attemptCount())
			}
			now = d.NextAttemptAt
			if _, err := n.sweep(ctx); err != nil {
				t.Fatal(err)
			}
			d, err = db.GetDelivery(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status != store.OutboxDelivered || d.Attempts != tc.prior+2 || len(sender.delivered()) != 1 {
				t.Fatalf("delivery did not recover: %+v", d)
			}
		})
	}
}
