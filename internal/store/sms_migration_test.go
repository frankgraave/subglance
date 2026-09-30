package store

import (
	"context"
	"testing"
	"time"
)

// TestSMSMigrationPreservesChannels covers the rebuild in 0024, the same way
// TestNtfyGotifyMigrationPreservesChannels covers 0021: migrate to just before
// it, write rows the old schema allowed (including a config key from 0023,
// which points at notif_channels too), then apply it.
func TestSMSMigrationPreservesChannels(t *testing.T) {
	ctx := context.Background()
	db := openUnmigratedDB(t)

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if err := db.prepareMigrationTable(ctx); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	applied := 0
	for _, m := range all {
		if m.name >= "0024" {
			break
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
		applied++
	}
	if applied == len(all) {
		t.Fatal("no 0024 migration found")
	}

	now := time.Now().Unix()
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := db.Writer.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}

	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO notif_channels (name, type, config_json, created_at, updated_at)
		VALUES ('text', 'sms', '{}', ?, ?)`, now, now); err == nil {
		t.Fatal("the pre-0024 schema accepted an sms channel; the test is not testing a rebuild")
	}

	monitorID := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('api', 'http', 'https://example.com', ?, ?)`, now, now)
	keep := exec(`INSERT INTO notif_channels (name, type, config_json, created_at, updated_at, is_default)
		VALUES ('push', 'ntfy', '{"topic":"alerts"}', ?, ?, 1)`, now, now)
	gone := exec(`INSERT INTO notif_channels (name, type, config_json, created_at, updated_at)
		VALUES ('old', 'webhook', '{}', ?, ?)`, now, now)
	exec(`INSERT INTO monitor_channels (monitor_id, channel_id) VALUES (?, ?)`, monitorID, keep)
	exec(`INSERT INTO channel_config_keys (channel_id, key) VALUES (?, 'push')`, keep)
	exec(`DELETE FROM notif_channels WHERE id = ?`, gone)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("apply 0024: %v", err)
	}

	ch, err := db.GetChannel(ctx, keep)
	if err != nil {
		t.Fatalf("channel gone after the rebuild: %v", err)
	}
	if ch.Type != ChannelNtfy || !ch.IsDefault || ch.Config["topic"] != "alerts" {
		t.Errorf("channel did not survive intact: %+v", ch)
	}
	var assigned, keys int
	if err := db.Reader.QueryRowContext(ctx,
		`SELECT count(*) FROM monitor_channels WHERE channel_id = ?`, keep).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRowContext(ctx,
		`SELECT count(*) FROM channel_config_keys WHERE channel_id = ?`, keep).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if assigned != 1 || keys != 1 {
		t.Errorf("assignments = %d, config keys = %d, want 1 and 1: the rebuild cascaded them away", assigned, keys)
	}

	created, err := db.CreateChannel(ctx, Channel{
		Name: "on call", Type: ChannelSMS, Config: map[string]string{"numbers": "+31612345678"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create sms channel after 0024: %v", err)
	}
	if created.ID <= gone {
		t.Errorf("sms channel got id %d, want above the deleted id %d", created.ID, gone)
	}
}
