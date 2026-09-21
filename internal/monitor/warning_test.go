package monitor

import (
	"context"
	"encoding/json"
	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/scheduler"
	"github.com/frankgraave/subglance/internal/store"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestWarningLifecycle(t *testing.T) {
	for _, recoverWarning := range []bool{true, false} {
		t.Run(map[bool]string{true: "recover", false: "confirm"}[recoverWarning], func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			alerts := &alertRecorder{}
			m, err := db.CreateMonitor(ctx, store.Monitor{Name: "warning", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 2, IntervalS: 300})
			if err != nil {
				t.Fatal(err)
			}
			r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
			r.record(outcomeFor(m, m.Target, false))
			if got := string(r.Engine().Status(m.ID)); got != "warning" {
				t.Fatalf("unconfirmed status = %s, want warning", got)
			}
			if len(alerts.events()) != 0 {
				t.Fatal("warning alerted")
			}
			stats, err := db.Uptime(ctx, m.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if stats.Total != 0 {
				t.Fatalf("warning entered uptime denominator: %+v", stats)
			}
			r = New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
			if err := r.restore(ctx); err != nil {
				t.Fatal(err)
			}
			if got := string(r.Engine().Status(m.ID)); got != "warning" {
				t.Fatalf("restored %s", got)
			}
			r.record(outcomeFor(m, m.Target, recoverWarning))
			stats, err = db.Uptime(ctx, m.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := r.jobs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 || jobs[0].Down != !recoverWarning {
				t.Fatalf("runner did not expose recovery cadence: %+v", jobs)
			}
			if stats.Total != 1 {
				t.Fatalf("eligible samples = %d, want 1", stats.Total)
			}
			if recoverWarning {
				if stats.Up != 1 || len(alerts.events()) != 0 {
					t.Fatalf("warning recovery: %+v alerts %v", stats, alerts.events())
				}
			} else {
				if stats.Down != 1 || len(alerts.events()) != 1 {
					t.Fatalf("confirmation: %+v alerts %v", stats, alerts.events())
				}
			}
		})
	}
}

func TestWarningPauseRestartResetsStreak(t *testing.T) {
	ctx := t.Context()
	db := testDB(t)
	alerts := &alertRecorder{}
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "paused warning", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
	r.record(outcomeFor(m, m.Target, false))
	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := r.sch.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err != store.ErrNoOpenIncident {
		t.Fatalf("pause did not close warning: %v", err)
	}
	r = New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
	if err := r.restore(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}
	r.record(outcomeFor(m, m.Target, false))
	if got := string(r.Engine().Status(m.ID)); got != "warning" {
		t.Fatalf("pause/restart carried failure streak: %s", got)
	}
	if len(alerts.events()) != 0 {
		t.Fatalf("pause/restart notified: %v", alerts.events())
	}
	hbs, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hbs) != 2 || hbs[0].Assessment != "warning" || hbs[1].Assessment != "warning" {
		t.Fatalf("pause lost warning history: %+v", hbs)
	}
}

// The runner must reschedule the existing queue immediately; waiting for a
// registry reload leaves a five-minute monitor on its old due time.
func TestRunnerImmediatelySchedulesRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		db := testDB(t)
		m, err := db.CreateMonitor(ctx, store.Monitor{Name: "cadence", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 1, IntervalS: 300})
		if err != nil {
			t.Fatal(err)
		}
		r := New(Options{DB: db, Log: quietLogger()})
		r.record(outcomeFor(m, m.Target, true))
		var calls atomic.Int32
		r.sch = scheduler.New(scheduler.Options{Registry: scheduler.RegistryFunc(r.jobs), Checkers: map[checker.Type]checker.Checker{checker.TypeHTTP: recoveryChecker{&calls}}, OnResult: r.record, Workers: 1, JitterFraction: -1, ReloadInterval: time.Hour, Log: quietLogger()})
		done := make(chan struct{})
		go func() { defer close(done); _ = r.sch.Run(ctx) }()
		synctest.Wait()
		r.record(outcomeFor(m, m.Target, false))
		time.Sleep(61 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("confirmed down was not rescheduled immediately: %d recovery checks", calls.Load())
		}
		// The successful recovery must restore the configured 300-second cadence.
		time.Sleep(70 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("recovery kept fast cadence: %d checks", calls.Load())
		}
		cancel()
		<-done
	})
}

type recoveryChecker struct{ calls *atomic.Int32 }

func (c recoveryChecker) Check(context.Context, checker.Monitor) checker.Result {
	c.calls.Add(1)
	return checker.Result{OK: true, CheckedAt: time.Now()}
}

func TestWarningAssessmentPublishedAndStored(t *testing.T) {
	ctx := t.Context()
	db := testDB(t)
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "streamed", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(16)
	sub := bus.Subscribe()
	defer sub.Close()
	r := New(Options{DB: db, Log: quietLogger(), Bus: bus})
	for i, want := range []string{"warning", "down", "up"} {
		outcome := outcomeFor(m, m.Target, i == 2)
		if i < 2 {
			outcome.Result.Kind = checker.FailStatus
		}
		if err := r.recordOutcome(outcome); err != nil {
			t.Fatal(err)
		}
		var frame events.Event
		for {
			select {
			case frame = <-sub.C():
			default:
				t.Fatal("heartbeat was not published")
			}
			if frame.Kind == events.KindHeartbeat {
				break
			}
		}
		wire, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Data struct {
				Assessment  string `json:"assessment"`
				FailureKind string `json:"failure_kind"`
			} `json:"data"`
		}
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Data.Assessment != want {
			t.Fatalf("SSE assessment = %q, want %q", decoded.Data.Assessment, want)
		}
		hb, err := db.LatestHeartbeat(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if hb.Assessment != want || hb.FailureKind != decoded.Data.FailureKind {
			t.Fatalf("SSE disagrees with stored assessment: %s, %+v", wire, hb)
		}
		if i < 2 && decoded.Data.FailureKind != "status" {
			t.Fatalf("SSE lost failure cause: %s", wire)
		}
	}
}
