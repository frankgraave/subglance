package store

import (
	"testing"
	"time"
)

// TestResetInstanceEmptiesMonitoringAndKeepsAccounts is the whole contract of
// the reset: every table that describes what is monitored ends up empty, and
// every table that describes who may sign in, or how the instance is
// configured, is untouched.
func TestResetInstanceEmptiesMonitoringAndKeepsAccounts(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	admin, err := db.CreateUser(ctx, "admin@example.com", "correct-horse-battery-staple", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(ctx, "viewer@example.com", "correct-horse-battery-staple", RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSession(ctx, admin.ID, "test", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.CreateAPIToken(ctx, admin.ID, "ci", nil); err != nil {
		t.Fatal(err)
	}
	raw := 3 * 24 * time.Hour
	if err := db.SetRetention(ctx, &raw, nil, RetentionPins{}); err != nil {
		t.Fatal(err)
	}

	m, err := db.CreateMonitor(ctx, Monitor{Name: "api", Type: "http", Target: "https://example.com",
		Tags: map[string]string{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.CreateChannel(ctx, Channel{Name: "ops", Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/hook"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: m.ID, TS: now, OK: false, Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenIncident(ctx, m.ID, now, "down", "boom"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch.ID, MonitorID: m.ID, Event: "down", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		`INSERT INTO heartbeat_hourly (monitor_id, bucket, up_count, down_count) VALUES (?, ?, 1, 0)`,
		m.ID, now.Truncate(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	// A window chosen by tag names no monitor, so the cascade from monitors
	// cannot be what removes it.
	if _, err := db.CreateMaintenance(ctx, MaintenanceWindow{Name: "prod", TagKey: "env", TagValue: "prod", StartsAt: now, EndsAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ResetInstance(ctx)
	if err != nil {
		t.Fatalf("ResetInstance: %v", err)
	}
	want := ResetCounts{Monitors: 1, Channels: 1, APITokens: 1, MaintenanceWindows: 1}
	if got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}

	count := func(table string) int {
		t.Helper()
		var n int
		if err := db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	for _, table := range []string{
		"monitors", "monitor_tags", "heartbeats", "heartbeat_hourly", "heartbeat_responses",
		"incidents", "notif_channels", "monitor_channels", "notif_outbox", "notif_quiet_hours",
		"maintenance_windows", "maintenance_channel_alerts", "api_tokens",
	} {
		if n := count(table); n != 0 {
			t.Errorf("%s has %d rows after a reset, want 0", table, n)
		}
	}
	if n := count("users"); n != 2 {
		t.Errorf("users = %d after a reset, want 2: accounts must survive", n)
	}
	if n := count("sessions"); n != 1 {
		t.Errorf("sessions = %d after a reset, want 1: the operator must stay signed in", n)
	}
	w, err := db.ResolveRetention(ctx, RetentionPins{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Raw.Value != raw {
		t.Errorf("raw retention = %v after a reset, want the saved %v", w.Raw.Value, raw)
	}
}

// A second reset on an empty instance is not an error: the operator asked for
// an empty instance and has one.
func TestResetInstanceOnAnEmptyInstance(t *testing.T) {
	db := openTestDB(t)
	got, err := db.ResetInstance(t.Context())
	if err != nil {
		t.Fatalf("ResetInstance: %v", err)
	}
	if got != (ResetCounts{}) {
		t.Errorf("counts = %+v, want all zero", got)
	}
}
