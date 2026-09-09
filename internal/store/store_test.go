package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{
		Path: filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := []string{
		"users", "monitors", "heartbeats", "heartbeat_hourly",
		"incidents", "notif_channels", "monitor_channels", "settings",
		"schema_migrations",
	}
	for _, table := range want {
		var name string
		err := db.Reader.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestPragmasTakeEffect(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var journalMode string
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	// Both pools must have foreign keys on, not just the writer: the pragma is
	// per-connection, so a reader without it would silently skip constraints.
	var fkWriter, fkReader int
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkWriter); err != nil {
		t.Fatalf("writer foreign_keys: %v", err)
	}
	if err := db.Reader.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkReader); err != nil {
		t.Fatalf("reader foreign_keys: %v", err)
	}
	if fkWriter != 1 {
		t.Error("foreign_keys is off on the writer pool")
	}
	if fkReader != 1 {
		t.Error("foreign_keys is off on the reader pool")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Open already migrated; running again must be a no-op rather than an error.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("third Migrate: %v", err)
	}

	var count int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM schema_migrations WHERE name = '0001_init.sql'",
	).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("0001_init.sql recorded %d times, want 1", count)
	}
}

func TestReopenExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.db")
	ctx := context.Background()

	db1, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	now := int64(1700000000)
	if _, err := db1.Writer.ExecContext(ctx,
		`INSERT INTO monitors (name, type, target, created_at, updated_at)
		 VALUES ('example', 'http', 'https://example.com', ?, ?)`, now, now,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	var name string
	if err := db2.Reader.QueryRowContext(ctx, "SELECT name FROM monitors").Scan(&name); err != nil {
		t.Fatalf("select after reopen: %v", err)
	}
	if name != "example" {
		t.Errorf("name = %q, want example", name)
	}
}

// ON DELETE CASCADE is load-bearing: without it, deleting a monitor would
// leave its heartbeats behind forever and the database would grow without end.
func TestForeignKeyCascade(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := int64(1700000000)

	res, err := db.Writer.ExecContext(ctx,
		`INSERT INTO monitors (name, type, target, created_at, updated_at)
		 VALUES ('cascade', 'http', 'https://example.com', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert monitor: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	for i := range 3 {
		if _, err := db.Writer.ExecContext(ctx,
			"INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms) VALUES (?, ?, 1, ?)",
			id, now+int64(i), 42,
		); err != nil {
			t.Fatalf("insert heartbeat: %v", err)
		}
	}

	if _, err := db.Writer.ExecContext(ctx, "DELETE FROM monitors WHERE id = ?", id); err != nil {
		t.Fatalf("delete monitor: %v", err)
	}

	var remaining int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT count(*) FROM heartbeats WHERE monitor_id = ?", id,
	).Scan(&remaining); err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d orphaned heartbeats survived the cascade", remaining)
	}
}

func TestRejectsInvalidForeignKey(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Writer.ExecContext(context.Background(),
		"INSERT INTO heartbeats (monitor_id, ts, ok) VALUES (99999, 1700000000, 1)")
	if err == nil {
		t.Error("insert with a dangling monitor_id succeeded; foreign keys are not enforced")
	}
}

// The state engine relies on this index to guarantee it can never open two
// incidents for the same monitor, which would alert the user twice.
func TestOnlyOneOpenIncidentPerMonitor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := int64(1700000000)

	res, err := db.Writer.ExecContext(ctx,
		`INSERT INTO monitors (name, type, target, created_at, updated_at)
		 VALUES ('inc', 'http', 'https://example.com', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert monitor: %v", err)
	}
	id, _ := res.LastInsertId()

	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)", id, now,
	); err != nil {
		t.Fatalf("first incident: %v", err)
	}

	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)", id, now+10,
	); err == nil {
		t.Error("a second open incident was allowed; the partial unique index is not working")
	}

	// Once resolved, a new incident must be allowed again.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE incidents SET resolved_at = ? WHERE monitor_id = ?", now+20, id,
	); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)", id, now+30,
	); err != nil {
		t.Errorf("new incident after resolution rejected: %v", err)
	}
}

func TestCheckConstraints(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := int64(1700000000)

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"unknown monitor type",
			`INSERT INTO monitors (name, type, target, created_at, updated_at) VALUES ('x', 'carrier-pigeon', 't', ?, ?)`,
			[]any{now, now},
		},
		{
			"interval below the floor",
			`INSERT INTO monitors (name, type, target, interval_s, created_at, updated_at) VALUES ('x', 'http', 't', 5, ?, ?)`,
			[]any{now, now},
		},
		{
			"interval above a day",
			`INSERT INTO monitors (name, type, target, interval_s, created_at, updated_at) VALUES ('x', 'http', 't', 999999, ?, ?)`,
			[]any{now, now},
		},
		{
			"unknown user role",
			`INSERT INTO users (email, password_hash, role, created_at, updated_at) VALUES ('a@b.c', 'h', 'superuser', ?, ?)`,
			[]any{now, now},
		},
		{
			"unknown channel type",
			`INSERT INTO notif_channels (name, type, config_json, created_at, updated_at) VALUES ('x', 'carrier-pigeon', '{}', ?, ?)`,
			[]any{now, now},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := db.Writer.ExecContext(ctx, tt.sql, tt.args...); err == nil {
				t.Error("invalid row was accepted; the CHECK constraint is missing or wrong")
			}
		})
	}
}

func TestEmailIsCaseInsensitivelyUnique(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := int64(1700000000)

	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO users (email, password_hash, created_at, updated_at) VALUES ('Frank@Example.com', 'h', ?, ?)",
		now, now,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO users (email, password_hash, created_at, updated_at) VALUES ('frank@example.com', 'h', ?, ?)",
		now, now,
	); err == nil {
		t.Error("the same email in different case was accepted twice")
	}
}

func TestPathStripsDSN(t *testing.T) {
	db := &DB{path: "/data/subglance.db?_pragma=foreign_keys(ON)"}
	if got := db.Path(); got != "/data/subglance.db" {
		t.Errorf("Path() = %q, want /data/subglance.db", got)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), Options{Path: ""}); err == nil {
		t.Error("Open with an empty path succeeded, want error")
	}
}
