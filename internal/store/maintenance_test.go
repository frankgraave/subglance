package store

import (
	"testing"
	"time"
)

func TestMaintenanceRecurrenceBoundaries(t *testing.T) {
	cases := []struct {
		name, zone, clock, at string
		duration              int
		active                bool
	}{
		{"before", "Europe/Amsterdam", "02:30", "2026-10-18T00:29:59Z", 60, false},
		{"start inclusive", "Europe/Amsterdam", "02:30", "2026-10-18T00:30:00Z", 60, true},
		{"end exclusive", "Europe/Amsterdam", "02:30", "2026-10-18T01:30:00Z", 60, false},
		{"spring missing skipped", "Europe/Amsterdam", "02:30", "2026-03-29T01:45:00Z", 60, false},
		{"fall earlier occurrence", "Europe/Amsterdam", "02:30", "2026-10-25T00:30:00Z", 30, true},
		{"fall later occurrence skipped", "Europe/Amsterdam", "02:30", "2026-10-25T01:30:00Z", 30, false},
		{"elapsed duration across fold", "Europe/Amsterdam", "02:30", "2026-10-25T01:45:00Z", 120, true},
		{"Lord Howe earlier", "Australia/Lord_Howe", "01:45", "2026-04-04T14:45:00Z", 15, true},
		{"Lord Howe later skipped", "Australia/Lord_Howe", "01:45", "2026-04-04T15:15:00Z", 15, false},
		{"overnight", "UTC", "23:30", "2026-09-21T00:15:00Z", 60, true},
		{"wrong weekday", "UTC", "23:30", "2026-09-22T00:15:00Z", 60, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := MaintenanceWindow{Timezone: c.zone, LocalTime: c.clock, Weekdays: []int{0}, DurationMinutes: c.duration}
			at, err := time.Parse(time.RFC3339, c.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.Active(at); got != c.active {
				t.Fatalf("active=%v want %v", got, c.active)
			}
		})
	}
}
func TestMaintenanceRollupPreservesExclusions(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "maintenance")
	now := time.Now()
	old := now.Add(-48 * time.Hour).Truncate(time.Hour)
	for _, maintained := range []bool{false, true} {
		for i, a := range []string{"up", "down", "warning", ""} {
			if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: id, TS: old.Add(time.Duration(i) * time.Second), OK: a == "up", Assessment: a, Maintenance: maintained}); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func() {
		t.Helper()
		s, err := db.Uptime(ctx, id, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if s.Up != 1 || s.Down != 1 || s.Total != 2 || s.Warning != 1 || s.Legacy != 1 || s.Maintenance != 4 || s.Percentage != 50 {
			t.Fatalf("uptime %+v", s)
		}
	}
	check()
	if _, err := db.rollupAt(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	check()
	// Capture suppression takes a separate INSERT path and must preserve the flag.
	if err := db.RecordHeartbeatWithCaptureReason(ctx, Heartbeat{MonitorID: id, TS: old, Assessment: "down", Maintenance: true}, CaptureBudget); err != nil {
		t.Fatal(err)
	}
	if _, err := db.rollupAt(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	s, err := db.Uptime(ctx, id, 72*time.Hour)
	if err != nil || s.Maintenance != 5 || s.Total != 2 {
		t.Fatalf("late merge %+v %v", s, err)
	}
}
func TestMaintenanceScopeOverlapAndPersistence(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "tagged")
	m, err := db.GetMonitor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	m.Tags = map[string]string{"env": "prod"}
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	a, err := db.CreateMaintenance(ctx, MaintenanceWindow{Name: "one", MonitorID: id, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateMaintenance(ctx, MaintenanceWindow{Name: "tag", TagKey: "env", TagValue: "prod", StartsAt: now, EndsAt: now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		at   time.Time
		want bool
	}{{a.StartsAt.Add(-time.Second), false}, {b.CreatedAt, true}, {a.EndsAt, true}, {b.EndsAt, false}} {
		v, e := db.InMaintenance(ctx, id, step.at)
		if e != nil || v != step.want {
			t.Fatalf("at %s active %v err %v", step.at, v, e)
		}
	}
	if err := db.DeleteMaintenance(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if yes, err := db.InMaintenance(ctx, id, b.CreatedAt); err != nil || !yes {
		t.Fatal("overlap cancellation removed other window")
	}
	stored, err := db.ListMaintenance(ctx)
	if err != nil || len(stored) != 1 || stored[0].ID != b.ID || stored[0].TagValue != "prod" {
		t.Fatalf("persisted %+v %v", stored, err)
	}
	m.Tags = map[string]string{"env": "dev"}
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if yes, err := db.InMaintenance(ctx, id, b.CreatedAt); err != nil || yes {
		t.Fatal("tag membership was not re-evaluated")
	}
}

func TestMaintenanceSurvivesDatabaseReopen(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/maintenance.db"
	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	id := seedMonitor(t, db, "persisted")
	now := time.Now().UTC().Truncate(time.Second)
	_, err = db.CreateMaintenance(ctx, MaintenanceWindow{Name: "once", MonitorID: id, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CreateMaintenance(ctx, MaintenanceWindow{Name: "weekly", MonitorID: id, Timezone: "UTC", Weekdays: []int{int(now.Weekday())}, LocalTime: now.Format("15:04"), DurationMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	windows, err := db.ListMaintenance(ctx)
	if err != nil || len(windows) != 2 {
		t.Fatalf("lost windows: %+v %v", windows, err)
	}
	for _, w := range windows {
		if !w.Active(w.CreatedAt) {
			t.Fatalf("reopened schedule inactive: %+v", w)
		}
	}
}

func TestMaintenanceDeletedMonitorCannotTransferSchedule(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "old")
	now := time.Now()
	_, err := db.CreateMaintenance(ctx, MaintenanceWindow{Name: "old scope", MonitorID: id, StartsAt: now, EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMonitor(ctx, id); err != nil {
		t.Fatal(err)
	}
	windows, err := db.ListMaintenance(ctx)
	if err != nil || len(windows) != 0 {
		t.Fatalf("deleted monitor left schedule: %+v %v", windows, err)
	}
	next := seedMonitor(t, db, "replacement")
	if muted, err := db.InMaintenance(ctx, next, now); err != nil || muted {
		t.Fatal("old schedule transferred to a replacement monitor")
	}
}

func TestMaintenanceCancellationIDIsNotReused(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "scope")
	now := time.Now()
	spec := MaintenanceWindow{Name: "deploy", MonitorID: id, StartsAt: now, EndsAt: now.Add(time.Hour)}
	first, err := db.CreateMaintenance(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMaintenance(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	next, err := db.CreateMaintenance(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID <= first.ID {
		t.Fatal("stale cancellation could remove a replacement schedule")
	}
}

func TestMaintenanceSuppressedDeliveriesFollowTerminalRetention(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	monitor := seedMonitor(t, db, "retention")
	channel := seedChannel(t, db, "retention")
	var suppressedID int64
	for _, suppress := range []bool{true, false} {
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: channel.ID, MonitorID: monitor, Event: "incident_confirmed", Payload: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		if suppress {
			suppressedID = d.ID
			if err := db.SuppressDelivery(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := time.Now().Add(time.Hour)
	removed, err := db.PruneDeliveries(ctx, before)
	if err != nil || removed != 1 {
		t.Fatalf("pruned %d deliveries, want only suppressed terminal row: %v", removed, err)
	}
	var remaining int
	if err := db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM notif_outbox WHERE id=?`, suppressedID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("suppressed row retained: %d %v", remaining, err)
	}
	due, err := db.DueDeliveries(ctx, before, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("pending work lost: %+v %v", due, err)
	}
}
