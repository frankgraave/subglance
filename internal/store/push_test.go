package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/auth"
)

func mustCreatePushMonitor(t *testing.T, db *DB, name string, every, grace int) Monitor {
	t.Helper()
	m, err := db.CreateMonitor(context.Background(), Monitor{
		Name:          name,
		Type:          TypePush,
		PushIntervalS: every,
		PushGraceS:    grace,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create push monitor: %v", err)
	}
	return m
}

// TestCreatePushMonitorIssuesAToken is the whole credential contract in one
// test: a token exists exactly once, in the return value, and never again.
func TestCreatePushMonitorIssuesAToken(t *testing.T) {
	db := openTestDB(t)
	m := mustCreatePushMonitor(t, db, "nightly backup", 3600, 300)

	if m.PushToken == "" {
		t.Fatal("created push monitor has no token")
	}
	if m.PushTokenPrefix == "" {
		t.Error("created push monitor has no token prefix")
	}
	if len(m.PushToken) < 20 {
		t.Errorf("token is suspiciously short: %d characters", len(m.PushToken))
	}

	// The plaintext must not be reachable through any ordinary read.
	reread, err := db.GetMonitor(context.Background(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if reread.PushToken != "" {
		t.Error("GetMonitor returned the plaintext push token; it must only ever exist once")
	}
	if reread.PushTokenPrefix != m.PushTokenPrefix {
		t.Errorf("prefix = %q, want %q", reread.PushTokenPrefix, m.PushTokenPrefix)
	}
	if reread.PushIntervalS != 3600 || reread.PushGraceS != 300 {
		t.Errorf("window = %d/%d, want 3600/300", reread.PushIntervalS, reread.PushGraceS)
	}
}

// TestPushTokensAreUnique guards against a generator that is not actually
// random. A duplicate token would let one job report for another's monitor.
func TestPushTokensAreUnique(t *testing.T) {
	db := openTestDB(t)

	seen := map[string]bool{}
	for i := range 20 {
		m := mustCreatePushMonitor(t, db, "job", 60, 0)
		if seen[m.PushToken] {
			t.Fatalf("duplicate push token on monitor %d", i)
		}
		seen[m.PushToken] = true
	}
}

func TestMonitorByPushToken(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "importer", 600, 60)

	got, err := db.MonitorByPushToken(ctx, m.PushToken)
	if err != nil {
		t.Fatalf("MonitorByPushToken: %v", err)
	}
	if got.ID != m.ID {
		t.Errorf("resolved to monitor %d, want %d", got.ID, m.ID)
	}

	for name, token := range map[string]string{
		"empty":   "",
		"unknown": "sgu_notatokenatall",
		// The stored value is a hash, so handing the hash back must not work
		// either — that is the entire point of storing it hashed.
		"the stored hash": auth.HashToken(m.PushToken),
	} {
		if _, err := db.MonitorByPushToken(ctx, token); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s token: err = %v, want sql.ErrNoRows", name, err)
		}
	}
}

// TestPushDeadlineAddsGrace pins the direction of the grace period. Getting
// this backwards would mean raising the tolerance made the monitor stricter.
func TestPushDeadlineAddsGrace(t *testing.T) {
	last := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	m := Monitor{PushIntervalS: 3600, PushGraceS: 300}

	want := last.Add(65 * time.Minute)
	if got := m.PushDeadline(last); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got, want)
	}
}

// TestLastActivityFallsBackToCreation is what stops a brand-new push monitor
// being declared down before anyone has pasted the URL anywhere.
func TestLastActivityFallsBackToCreation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "fresh", 3600, 0)

	last, err := db.LastActivity(ctx, m)
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}
	if !last.Equal(m.CreatedAt) {
		t.Errorf("last activity = %v, want creation time %v", last, m.CreatedAt)
	}

	beat := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: m.ID, TS: beat, OK: true}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	last, err = db.LastActivity(ctx, m)
	if err != nil {
		t.Fatalf("LastActivity after beat: %v", err)
	}
	if !last.Equal(beat.UTC()) {
		t.Errorf("last activity = %v, want the heartbeat at %v", last, beat.UTC())
	}
}

func TestListEnabledPushMonitorsExcludesOthers(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := mustCreatePushMonitor(t, db, "wanted", 60, 0)
	paused := mustCreatePushMonitor(t, db, "paused", 60, 0)
	if err := db.SetMonitorEnabled(ctx, paused.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	mustCreateMonitor(t, db, "an http monitor")

	got, err := db.ListEnabledPushMonitors(ctx)
	if err != nil {
		t.Fatalf("ListEnabledPushMonitors: %v", err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("got %d monitors, want only %d", len(got), want.ID)
	}
}

// TestMigrationPreservesChildRows is the test for the riskiest line in this
// change. 0005 rebuilds `monitors` by the twelve-step recipe, and dropping the
// old table with foreign keys enforced would cascade every heartbeat, incident
// and tag away with it.
//
// It has to migrate up to 0004, write child rows, and only then apply 0005 —
// data written after the migration proves nothing about what the migration did
// to data that was already there.
func TestMigrationPreservesChildRows(t *testing.T) {
	ctx := context.Background()
	db := openUnmigratedDB(t)

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(all) < 5 || all[4].name != "0005_push_monitors.sql" {
		t.Fatalf("expected 0005_push_monitors.sql fifth, got %d migrations", len(all))
	}

	if err := db.prepareMigrationTable(ctx); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	for _, m := range all[:4] {
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}

	// Raw SQL, because the Go layer already speaks the post-0005 schema and
	// the row has to be written the way the old code would have written it.
	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('survivor', 'http', 'https://example.com', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert monitor: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		`INSERT INTO heartbeats (monitor_id, ts, ok) VALUES (?, ?, 1)`, id, now); err != nil {
		t.Fatalf("insert heartbeat: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		`INSERT INTO monitor_tags (monitor_id, key, value) VALUES (?, 'env', 'prod')`,
		id); err != nil {
		t.Fatalf("insert tag: %v", err)
	}
	if _, err := db.OpenIncident(ctx, id, time.Now(), "status", "500"); err != nil {
		t.Fatalf("incident: %v", err)
	}

	// The migration under test.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("apply 0005: %v", err)
	}

	beats, err := db.ListHeartbeats(ctx, id, 10)
	if err != nil || len(beats) != 1 {
		t.Errorf("heartbeats = %d (err %v), want 1 — the rebuild cascaded them away",
			len(beats), err)
	}
	if _, err := db.OpenIncidentFor(ctx, id); err != nil {
		t.Errorf("open incident gone after the rebuild: %v", err)
	}
	reread, err := db.GetMonitor(ctx, id)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if reread.Tags["env"] != "prod" {
		t.Errorf("tags = %v, want env=prod", reread.Tags)
	}
	if reread.Name != "survivor" || reread.Target != "https://example.com" {
		t.Errorf("monitor row itself did not survive intact: %+v", reread)
	}
}

// TestForeignKeysStayOnAfterMigration is the other half of the same risk. The
// pragma is connection-scoped, so a migration that turned it off and did not
// turn it back on would hand the pool a connection with no cascade rules — and
// nothing else in the system would ever notice.
func TestForeignKeysStayOnAfterMigration(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Every connection in the pool, not just whichever one answers first.
	for range 8 {
		var fk int
		if err := db.Writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("read pragma: %v", err)
		}
		if fk != 1 {
			t.Fatal("foreign_keys is off on a pooled writer connection after migrating")
		}
	}

	// And prove it behaves, not just that it reports well.
	m := mustCreateMonitor(t, db, "cascade")
	if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: m.ID, TS: time.Now(), OK: true}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := db.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var orphans int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM heartbeats WHERE monitor_id = ?", m.ID).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d orphaned heartbeats survived the delete; ON DELETE CASCADE is not enforced", orphans)
	}
}

// TestSchemaRejectsTokenOnNonPushMonitor proves the CHECK is real, because the
// handlers above it are not the only way rows get written.
func TestSchemaRejectsTokenOnNonPushMonitor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, push_token_hash, created_at, updated_at)
		VALUES ('smuggled', 'http', 'https://example.com', 'deadbeef', 0, 0)`)
	if err == nil {
		t.Error("schema accepted a push token on an http monitor")
	}

	_, err = db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('tokenless', 'push', '', 0, 0)`)
	if err == nil {
		t.Error("schema accepted a push monitor with no token")
	}
}

// TestSchemaRequiresPushInterval guards the column the watchdog compares
// against. A push monitor with no expected interval is skipped on every sweep,
// so it would appear in the list as watched while nothing could ever declare it
// down — the one failure a dead man's switch must not have.
func TestSchemaRequiresPushInterval(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, push_token_hash, created_at, updated_at)
		VALUES ('windowless', 'push', '', 'cafebabe', 0, 0)`)
	if err == nil {
		t.Error("schema accepted a push monitor with no expected interval")
	}

	// And the mirror case: an interval on a monitor that is dialled would be a
	// window nothing consults.
	_, err = db.Writer.ExecContext(ctx, `
		INSERT INTO monitors (name, type, target, push_interval_s, created_at, updated_at)
		VALUES ('dialled', 'http', 'https://example.com', 3600, 0, 0)`)
	if err == nil {
		t.Error("schema accepted an expected push interval on an http monitor")
	}
}

// openUnmigratedDB opens the pools without running migrations, so a test can
// apply them one at a time.
//
// It duplicates the small amount of Open that is not migration, rather than
// adding a "skip migrations" option to the real constructor. A production
// entry point with a flag that produces an unusable database is a worse thing
// to carry than eight lines in a test file.
func openUnmigratedDB(t *testing.T) *DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "staged.db")
	writer, err := openPool(path, 1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	reader, err := openPool(path, 4)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}

	db := &DB{Writer: writer, Reader: reader, path: path}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.verify(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	return db
}
