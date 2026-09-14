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
	"database/sql/driver"
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
//
// It also refuses to run against a schema from the future; see
// checkNotDowngrade.
func (db *DB) Migrate(ctx context.Context) error {
	if err := db.prepareMigrationTable(ctx); err != nil {
		return err
	}

	applied, err := db.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	all, err := loadMigrations()
	if err != nil {
		return err
	}

	if err := checkNotDowngrade(applied, all); err != nil {
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

// checkNotDowngrade refuses to run when the ledger records a migration this
// binary does not know about.
//
// Rolling the container tag back is the first thing most self-hosters try when
// an upgrade goes wrong, and without this the older binary starts happily. It
// sees a schema that a newer migration has already rewritten — 0005 drops and
// renames the monitors table, for instance — and keeps writing against the
// shape it remembers: INSERTs missing columns, SELECTs against columns that
// moved. Nothing errors. It just writes wrong rows, and by the time anyone
// notices, the good data is gone.
//
// Failing to start is the only safe answer. The database still holds correct
// data at that point, and the operator can go forward again to the newer image
// or restore a backup; both are recoverable, and silent corruption is not.
//
// The check is on names rather than a PRAGMA user_version counter because the
// ledger is already the authoritative record and names survive out-of-order
// merges, which a single integer does not.
func checkNotDowngrade(applied map[string]bool, known []migration) error {
	embedded := make(map[string]bool, len(known))
	for _, m := range known {
		embedded[m.name] = true
	}

	var unknown []string
	for name := range applied {
		if !embedded[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown) // deterministic message, whatever map order gave

	return fmt.Errorf("store: database was migrated by a newer version of SubGlance "+
		"(unknown migration%s: %s); "+
		"this binary would write rows against a schema it does not understand, so it refuses to start. "+
		"Run a build that includes %s, or restore a backup taken before the upgrade",
		plural(len(unknown)), strings.Join(unknown, ", "), unknown[len(unknown)-1])
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// prepareMigrationTable creates the ledger Migrate reads and writes.
//
// Separate from Migrate so a test can apply migrations one at a time and
// observe what a particular one does to data that already exists — which is
// the only way to test a migration that rewrites a table.
func (db *DB) prepareMigrationTable(ctx context.Context) error {
	if _, err := db.Writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT    PRIMARY KEY,
			applied_at INTEGER NOT NULL
		) STRICT, WITHOUT ROWID;
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
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

// deferForeignKeysDirective marks a migration that rebuilds a table other rows
// point at.
//
// SQLite cannot widen a CHECK constraint in place, so such a migration follows
// the documented twelve-step recipe: create the new table, copy the rows, drop
// the old one, rename. With foreign_keys ON that sequence is impossible —
// dropping the old table would cascade every child row away, taking the
// heartbeats and incidents with it.
//
// The pragma is a no-op inside a transaction, so it has to be set before BEGIN
// and restored after COMMIT, and it is connection-scoped, so every statement
// has to travel on one connection. applyMigration therefore pins a connection
// for the whole migration rather than borrowing from the pool per statement.
const deferForeignKeysDirective = "-- +subglance defer-foreign-keys"

// applyMigration runs one migration and records it, atomically.
//
// Everything happens on a single pinned connection. That is required for the
// foreign-key directive above to mean anything, and harmless otherwise: the
// writer pool has exactly one connection anyway.
func (db *DB) applyMigration(ctx context.Context, m migration) error {
	deferFK := strings.Contains(m.sql, deferForeignKeysDirective)

	conn, err := db.Writer.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if deferFK {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("disable foreign keys: %w", err)
		}
		// Restoring the pragma is not optional: this connection goes back to
		// the pool and would otherwise serve the rest of the process with
		// foreign keys silently off — the exact guarantee verify() checks at
		// startup and would no longer be able to catch.
		//
		// Closing the connection on failure is the safe answer rather than a
		// nicety. A pooled connection whose constraints are off is worse than
		// no connection at all, and the pool simply opens a fresh one.
		defer func() {
			if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
	}

	tx, err := conn.BeginTx(ctx, nil)
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

	// A rebuild that left a dangling child row would otherwise commit
	// unnoticed, because the constraint that would have caught it was off.
	// Checking inside the transaction means a mistake rolls back instead of
	// becoming permanent damage.
	if deferFK {
		if err := checkForeignKeys(ctx, tx); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// checkForeignKeys fails when any row violates a foreign key.
//
// PRAGMA foreign_key_check returns one row per violation and nothing at all
// when the database is sound, so the presence of a first row is the failure.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if rows.Next() {
		return errors.New("store: migration left rows violating a foreign key")
	}
	return rows.Err()
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
