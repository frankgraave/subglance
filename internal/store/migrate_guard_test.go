package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigrateRefusesUnknownMigration covers the rollback case: an operator
// upgrades, something goes wrong, and they put the previous container tag back.
// The older binary meets a schema a newer migration already rewrote. It must
// refuse to start rather than write rows against a shape that no longer exists.
func TestMigrateRefusesUnknownMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	ctx := context.Background()

	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Stand in for a migration shipped by a newer build. The name is what
	// matters: this binary's embedded set does not contain it.
	const future = "9999_future_feature.sql"
	if _, err := db.Writer.ExecContext(ctx,
		"INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)",
		future, time.Now().Unix(),
	); err != nil {
		t.Fatalf("record future migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening is what the older binary does on start.
	reopened, err := Open(ctx, Options{Path: path})
	if err == nil {
		_ = reopened.Close()
		t.Fatal("Open succeeded against a newer schema; it should have refused")
	}

	msg := err.Error()
	// The message has to be actionable on its own: an operator reading a
	// crash-looping container log gets nothing else.
	if !strings.Contains(msg, future) {
		t.Errorf("error does not name the unknown migration: %v", err)
	}
	if !strings.Contains(msg, "newer version") {
		t.Errorf("error does not say a newer binary is needed: %v", err)
	}
}

// A database this binary migrated itself must keep opening, which is the case
// that would break if the guard compared the wrong thing.
func TestMigrateAcceptsOwnSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.db")
	ctx := context.Background()

	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("reopen own database: %v", err)
	}
	_ = again.Close()
}
