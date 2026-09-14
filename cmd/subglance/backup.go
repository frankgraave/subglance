package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/store"
)

// backupTimeout bounds the whole snapshot. VACUUM INTO rewrites the database
// page by page, so a large one takes a while; ten minutes is generous for any
// database a single instance produces, and still short enough that a stuck
// backup fails inside a cron window instead of running until the next one
// starts.
const backupTimeout = 10 * time.Minute

// runBackup writes a consistent snapshot of the database to a file.
//
// It exists as a subcommand for the same reason healthcheck does: the shipped
// image is distroless static, with no shell and no sqlite3 binary, so the only
// thing that can take a backup inside the container is the binary already in
// it.
//
// Copying the data volume while SubGlance runs — `docker cp`, `tar`, a
// volume-level snapshot — is not a backup. Under WAL the .db file lags behind
// to the last checkpoint and the recent writes live in the -wal file next to
// it, so a copy of the one file is stale, and a copy of all three taken at
// different instants can be inconsistent. store.BackupTo avoids both.
//
// It reads the same configuration as the server so --data-dir and
// SUBGLANCE_DATA_DIR point it at the same database, and a backup cannot
// quietly snapshot the wrong file.
func runBackup(args []string, out io.Writer) error {
	// The destination is taken as the first argument and split off before
	// config.Load, because Go's flag package stops parsing at the first
	// non-flag: leaving the path in the list would make
	// `backup /tmp/x.db --data-dir /data` silently ignore the flag and
	// snapshot the default directory instead.
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return errors.New("backup needs a destination path\n\nusage: subglance backup <path> [--data-dir DIR]")
	}
	dest := args[0]

	cfg, err := config.Load(args[1:])
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()

	// Opening the store runs migrations, which is wanted: a snapshot of a
	// half-migrated database is not something anyone should be handed. It also
	// means a backup taken with an older binary than the database refuses
	// rather than producing a misleading copy.
	db, err := store.Open(ctx, store.Options{Path: cfg.DBPath()})
	if err != nil {
		return fmt.Errorf("open database %s: %w", cfg.DBPath(), err)
	}
	defer func() { _ = db.Close() }()

	size, err := db.BackupTo(ctx, dest)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "wrote %s (%d bytes)\n", dest, size)
	return nil
}
