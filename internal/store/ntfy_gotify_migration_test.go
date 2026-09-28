package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestNtfyGotifyMigrationPreservesChannels covers the rebuild in 0021. Like
// 0005, it recreates a table that other tables point at, so it has to migrate
// to just before 0021, write the rows the old schema allowed, and only then
// apply it: rows written afterwards prove nothing about what the rebuild did.
func TestNtfyGotifyMigrationPreservesChannels(t *testing.T) {
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
		if m.name >= "0021" {
			break
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
		applied++
	}
	if applied == len(all) {
		t.Fatal("no 0021 migration found")
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

	// Before 0021 the CHECK refuses the new types; that is the point of it.
	if _, err := db.Writer.ExecContext(ctx, `
		INSERT INTO notif_channels (name, type, config_json, created_at, updated_at)
		VALUES ('push', 'ntfy', '{}', ?, ?)`, now, now); err == nil {
		t.Fatal("the pre-0021 schema accepted an ntfy channel; the test is not testing a rebuild")
	}

	monitorID := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('api', 'http', 'https://example.com', ?, ?)`, now, now)
	keep := exec(`INSERT INTO notif_channels (name, type, config_json, created_at, updated_at, is_default)
		VALUES ('ops', 'slack', '{"url":"https://hooks.example/x"}', ?, ?, 1)`, now, now)
	gone := exec(`INSERT INTO notif_channels (name, type, config_json, created_at, updated_at)
		VALUES ('old', 'webhook', '{}', ?, ?)`, now, now)
	exec(`INSERT INTO monitor_channels (monitor_id, channel_id) VALUES (?, ?)`, monitorID, keep)
	ruleID := exec(`INSERT INTO routing_rules (tag_key, tag_value, created_at) VALUES ('env', 'prod', ?)`, now)
	exec(`INSERT INTO routing_rule_channels (rule_id, channel_id) VALUES (?, ?)`, ruleID, keep)
	// The highest id is deleted, so only sqlite_sequence remembers it.
	exec(`DELETE FROM notif_channels WHERE id = ?`, gone)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("apply 0021: %v", err)
	}

	ch, err := db.GetChannel(ctx, keep)
	if err != nil {
		t.Fatalf("channel gone after the rebuild: %v", err)
	}
	if ch.Name != "ops" || ch.Type != ChannelSlack || !ch.IsDefault ||
		ch.Config["url"] != "https://hooks.example/x" {
		t.Errorf("channel did not survive intact: %+v", ch)
	}

	var assigned, routed int
	if err := db.Reader.QueryRowContext(ctx,
		`SELECT count(*) FROM monitor_channels WHERE channel_id = ?`, keep).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRowContext(ctx,
		`SELECT count(*) FROM routing_rule_channels WHERE channel_id = ?`, keep).Scan(&routed); err != nil {
		t.Fatal(err)
	}
	if assigned != 1 || routed != 1 {
		t.Errorf("monitor assignments = %d, rule channels = %d, want 1 and 1 — the rebuild cascaded them away",
			assigned, routed)
	}

	// A new channel must not reuse the id of the one deleted before the
	// migration: an outbox row or log line naming that id would otherwise
	// start to mean a different channel.
	for _, typ := range []string{ChannelNtfy, ChannelGotify} {
		created, err := db.CreateChannel(ctx, Channel{
			Name: typ, Type: typ, Config: map[string]string{"url": "https://push.example"}, Enabled: true,
		})
		if err != nil {
			t.Fatalf("create %s channel after 0021: %v", typ, err)
		}
		if created.ID <= gone {
			t.Errorf("%s channel got id %d, want above the deleted id %d", typ, created.ID, gone)
		}
	}

	// The partial unique index came back with the table.
	_, err = db.Writer.ExecContext(ctx, `
		INSERT INTO notif_channels (name, type, config_json, created_at, updated_at, is_default)
		VALUES ('second default', 'slack', '{}', ?, ?, 1)`, now, now)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Errorf("a second default channel was accepted after the rebuild (err %v)", err)
	}
}
