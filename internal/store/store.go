// Package store owns the SubGlance database: connection setup, schema
// migrations and query access.
//
// SQLite is the default and needs no configuration at all, which is a product
// principle rather than a convenience (product principle §3.2).
//
// # The two-pool design
//
// SQLite allows many concurrent readers but only one writer. Rather than
// letting callers discover that through intermittent SQLITE_BUSY errors, the
// DB exposes two pools: Writer is capped at a single connection so writes
// queue in Go instead of fighting in the driver, while Reader carries several
// connections for concurrent reads. In WAL mode readers never block the
// writer and the writer never blocks readers.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so cross-compiling stays trivial
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB holds the read and write connection pools.
type DB struct {
	// Writer has exactly one connection. Every INSERT/UPDATE/DELETE goes here.
	Writer *sql.DB
	// Reader carries several connections for concurrent SELECTs.
	Reader *sql.DB

	path string
}

// Options configures Open.
type Options struct {
	// Path is the SQLite file. Use ":memory:" for tests.
	Path string
	// MaxReaders caps concurrent read connections. Zero means 4.
	MaxReaders int
}

// Open connects to the database, applies the required pragmas and runs any
// outstanding migrations.
func Open(ctx context.Context, opts Options) (*DB, error) {
	if opts.Path == "" {
		return nil, errors.New("store: path must not be empty")
	}
	if opts.MaxReaders <= 0 {
		opts.MaxReaders = 4
	}

	memory := opts.Path == ":memory:"
	if memory {
		// A shared cache plus a named in-memory database lets both pools see
		// the same data. Without this, the reader pool would open its own
		// empty database and every test would fail in a confusing way.
		opts.Path = "file:subglance-test?mode=memory&cache=shared"
	}

	writer, err := openPool(opts.Path, 1)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	// An in-memory database vanishes when its last connection closes, so the
	// writer connection must never be recycled.
	if memory {
		writer.SetConnMaxLifetime(0)
		writer.SetConnMaxIdleTime(0)
		writer.SetMaxIdleConns(1)
	}

	reader, err := openPool(opts.Path, opts.MaxReaders)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}

	db := &DB{Writer: writer, Reader: reader, path: opts.Path}

	if err := db.verify(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// pragmas are applied to every new connection through the DSN, so they hold
// for pooled connections too — setting them once after Open would only affect
// whichever connection happened to serve that statement.
//
//	journal_mode=WAL   readers and the writer stop blocking each other
//	busy_timeout=5000  wait rather than fail instantly on a locked database
//	synchronous=NORMAL safe under WAL, far fewer fsyncs than FULL
//	foreign_keys=ON    SQLite disables these by default; our ON DELETE CASCADE
//	                   rules are load-bearing
var pragmas = []string{
	"_pragma=journal_mode(WAL)",
	"_pragma=busy_timeout(5000)",
	"_pragma=synchronous(NORMAL)",
	"_pragma=foreign_keys(ON)",
}

func openPool(path string, maxConns int) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	dsn := path + sep + strings.Join(pragmas, "&")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

// verify confirms the connection works and the pragmas actually took effect.
// A silently ignored foreign_keys pragma would let orphaned rows accumulate
// for months before anyone noticed.
func (db *DB) verify(ctx context.Context) error {
	if err := db.Writer.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	var fk int
	if err := db.Writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign_keys pragma did not take effect")
	}
	return nil
}

// Close shuts down both pools.
func (db *DB) Close() error {
	var errs []error
	if db.Reader != nil {
		if err := db.Reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close reader: %w", err))
		}
	}
	if db.Writer != nil {
		if err := db.Writer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close writer: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Path returns the database path, with any DSN query string stripped.
func (db *DB) Path() string {
	if i := strings.IndexByte(db.path, '?'); i >= 0 {
		return db.path[:i]
	}
	return db.path
}

type migration struct {
	name string
	sql  string
}

// Migrate applies every migration that has not run yet, in filename order.
//
// Each migration runs inside a transaction together with the row recording it,
// so a failure halfway leaves neither a partial schema nor a false record of
// success.
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.Writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT    PRIMARY KEY,
			applied_at INTEGER NOT NULL
		) STRICT, WITHOUT ROWID;
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := db.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	all, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range all {
		if applied[m.name] {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
	}
	return nil
}

func (db *DB) appliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := db.Writer.QueryContext(ctx, "SELECT name FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)",
		m.name, time.Now().Unix(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, migration{name: e.Name(), sql: string(b)})
	}

	// Filenames are zero-padded and numbered, so lexical order is apply order.
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
