package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Building a database from nothing costs about a second under -race: every
// migration in order, then an argon2id hash for the admin account, which is
// 64 MiB of deliberately slow work. Nearly four hundred tests in this package
// want exactly that database, and building it for each of them took the
// package to go test's ten-minute limit in CI.
//
// So it is built once per test binary and copied. Each test still gets a file
// of its own and its own *store.DB opened through store.Open, so verification,
// the migration pass (a no-op on a current schema) and the channel-encryption
// preparation run as they always did. Only the identical work in front of
// them is shared.

// dbTemplates holds the two starting points the helpers hand out, as the
// bytes of a complete database file.
type dbTemplates struct {
	empty  []byte // migrated, no accounts: the setup flow's starting point
	seeded []byte // migrated, with templateAdmin and one API token for it
	token  string // that token
}

// templateAdmin is the account testServerWithDB has always seeded.
const (
	templateAdmin    = "admin@example.com"
	templatePassword = "correct-horse-battery-staple"
)

var (
	templateOnce sync.Once
	templateDBs  dbTemplates
	errTemplate  error
)

// templates builds the template databases on first use.
func templates(t *testing.T) dbTemplates {
	t.Helper()
	templateOnce.Do(func() { templateDBs, errTemplate = buildTemplates() })
	if errTemplate != nil {
		t.Fatalf("build template database: %v", errTemplate)
	}
	return templateDBs
}

func buildTemplates() (dbTemplates, error) {
	dir, err := os.MkdirTemp("", "subglance-api-template-")
	if err != nil {
		return dbTemplates{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "work.db")})
	if err != nil {
		return dbTemplates{}, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	empty, err := snapshot(ctx, db, filepath.Join(dir, "empty.db"))
	if err != nil {
		return dbTemplates{}, fmt.Errorf("snapshot empty: %w", err)
	}

	user, err := db.CreateUser(ctx, templateAdmin, templatePassword, store.RoleAdmin)
	if err != nil {
		return dbTemplates{}, fmt.Errorf("create admin: %w", err)
	}
	token, _, err := db.CreateAPIToken(ctx, user.ID, "test", nil)
	if err != nil {
		return dbTemplates{}, fmt.Errorf("create token: %w", err)
	}

	seeded, err := snapshot(ctx, db, filepath.Join(dir, "seeded.db"))
	if err != nil {
		return dbTemplates{}, fmt.Errorf("snapshot seeded: %w", err)
	}
	return dbTemplates{empty: empty, seeded: seeded, token: token}, nil
}

// snapshot copies the database the way a backup does, through VACUUM INTO,
// so the bytes are one self-contained file with nothing left in a WAL.
func snapshot(ctx context.Context, db *store.DB, path string) ([]byte, error) {
	if _, err := db.BackupTo(ctx, path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// openCopy writes a template to a new file in the test's own temporary
// directory and opens it, so no two tests ever share a database.
func openCopy(t *testing.T, image []byte, name string) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("copy template database: %v", err)
	}
	db, err := store.Open(context.Background(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
