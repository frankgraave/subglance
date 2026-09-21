package notifier

import (
	"bytes"
	"context"
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
				if err != nil || len(deferred) != 1 || deferred[0].Attempts != 1 || deferred[0].LastError == "" || !deferred[0].NextAttemptAt.After(now) {
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
