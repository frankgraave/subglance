package monitor

import (
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
	"testing"
	"testing/synctest"
	"time"
)

func TestMaintenanceMeasurementsAndAlerts(t *testing.T) {
	for _, recovers := range []bool{true, false} {
		t.Run(map[bool]string{true: "quiet recovery", false: "still down after window"}[recovers], func(t *testing.T) {
			ctx := t.Context()
			db := testDB(t)
			alerts := &alertRecorder{}
			m, err := db.CreateMonitor(ctx, store.Monitor{Name: "deploy", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 2, RepeatAfterS: 60})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			w, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
			r.now = func() time.Time { return now.Add(10 * time.Minute) }
			for i := 0; i < 2; i++ {
				o := outcomeFor(m, m.Target, false)
				o.Result.CheckedAt = now
				r.record(o)
			}
			if r.Engine().Status(m.ID) != state.StatusDown {
				t.Fatal("maintenance stopped measurement or confirmation")
			}
			if len(alerts.events()) != 0 {
				t.Fatalf("maintenance alerted: %v", alerts.events())
			}
			r.sendDueReminders(ctx)
			if len(alerts.events()) != 0 {
				t.Fatal("maintenance reminder alerted")
			}
			hbs, err := db.ListHeartbeats(ctx, m.ID, 10)
			if err != nil || len(hbs) != 2 {
				t.Fatalf("measurements missing: %v %v", hbs, err)
			}
			stats, err := db.Uptime(ctx, m.ID, time.Hour)
			if err != nil || stats.Total != 0 {
				t.Fatalf("maintenance entered denominator: %+v %v", stats, err)
			}
			r = New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
			if err := r.restore(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.DeleteMaintenance(ctx, w.ID); err != nil {
				t.Fatal(err)
			}
			r.record(outcomeFor(m, m.Target, recovers))
			if recovers {
				if len(alerts.events()) != 0 {
					t.Fatal("unannounced maintenance recovery alerted")
				}
			} else {
				if got := alerts.events(); len(got) != 1 || got[0] != state.EventIncidentConfirmed {
					t.Fatalf("ongoing outage not announced after maintenance: %v", got)
				}
				r.record(outcomeFor(m, m.Target, false))
				if len(alerts.events()) != 1 {
					t.Fatal("duplicate post-maintenance initial alert")
				}
			}
		})
	}
}

func TestMaintenanceKeepsPausedMonitorPaused(t *testing.T) {
	ctx := t.Context()
	db := testDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "paused", Type: "http", Target: "https://example.invalid", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	r := New(Options{DB: db, Log: quietLogger()})
	jobs, err := r.jobs(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("maintenance resumed paused monitor: %+v %v", jobs, err)
	}
	if err := db.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}
	jobs, err = r.jobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("resume didn't schedule measurements: %+v %v", jobs, err)
	}
	r.record(outcomeFor(m, m.Target, true))
	s, err := db.Uptime(ctx, m.ID, time.Hour)
	if err != nil || s.Total != 0 || s.Maintenance != 1 {
		t.Fatalf("resumed maintenance %+v %v", s, err)
	}
}

func TestMaintenanceSuppressesExistingIncidentReminderAndRecovery(t *testing.T) {
	ctx := t.Context()
	db := testDB(t)
	alerts := &alertRecorder{}
	now := time.Now().UTC().Truncate(time.Second)
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "existing", Type: "http", Target: "https://example.invalid", Enabled: true, Retries: 1, RepeatAfterS: 60})
	if err != nil {
		t.Fatal(err)
	}
	r := New(Options{DB: db, Log: quietLogger(), Notify: alerts.record})
	r.now = func() time.Time { return now.Add(10 * time.Minute) }
	o := outcomeFor(m, m.Target, false)
	o.Result.CheckedAt = now
	r.record(o)
	if len(alerts.events()) != 1 {
		t.Fatal("initial alert missing")
	}
	_, err = db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	r.sendDueReminders(ctx)
	inc, err := db.OpenIncidentFor(ctx, m.ID)
	if err != nil || inc.ReminderCount != 0 || len(alerts.events()) != 1 {
		t.Fatalf("maintenance advanced reminder: %+v %v alerts %v", inc, err, alerts.events())
	}
	r.record(outcomeFor(m, m.Target, true))
	if len(alerts.events()) != 1 {
		t.Fatal("recovery during maintenance alerted")
	}
}

func TestMaintenancePushReportsAndWatchdog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		db := testDB(t)
		alerts := &alertRecorder{}
		m := mustPushMonitor(t, db, 60, 0)
		now := time.Now()
		_, err := db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "push deploy", MonitorID: m.ID, StartsAt: now, EndsAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		r := newPushRunner(t, db, alerts)
		if err := r.RecordPush(ctx, m, PushReport{OK: true}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			time.Sleep(61 * time.Second)
			r.sweepOverduePushMonitors(ctx)
		}
		beats, err := db.ListHeartbeats(ctx, m.ID, 10)
		if err != nil || len(beats) != 3 {
			t.Fatalf("push measurements %+v %v", beats, err)
		}
		for _, b := range beats {
			if !b.Maintenance {
				t.Fatal("push path lost maintenance")
			}
		}
		if len(alerts.events()) != 0 {
			t.Fatal("push watchdog alerted during maintenance")
		}
		stats, err := db.Uptime(ctx, m.ID, time.Hour)
		if err != nil || stats.Total != 0 || stats.Maintenance != 3 {
			t.Fatalf("push uptime %+v %v", stats, err)
		}
	})
}
