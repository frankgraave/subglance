package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// BackupTo writes a consistent snapshot of the database to dest and returns
// the size of the file it wrote.
//
// It exists because the obvious thing an operator does — `docker cp` or `tar`
// over the data volume while the container runs — is quietly wrong under WAL.
// The main .db file is only current up to the last checkpoint, and the -wal
// and -shm files that hold the rest are easy to miss, so the copy is either
// months stale or refuses to open. VACUUM INTO instead runs inside a read
// transaction and writes one self-contained file that already includes
// everything committed at the moment it started.
//
// VACUUM INTO also needs no external tooling: the shipped image is distroless
// with no shell and no sqlite3 binary, so a backup can only be taken by the
// binary that is already in the image.
//
// Edge cases, and the choices made about them:
//
//   - dest already exists. SQLite refuses to overwrite, and that is kept
//     rather than worked around. A backup command that silently truncates an
//     existing file will eventually destroy the one good copy someone had —
//     most likely when a cron job reuses a fixed filename and today's run
//     fails halfway. Refusing is recoverable; overwriting is not. The check
//     is done here as well so the message names the file instead of surfacing
//     a bare SQLITE_ERROR.
//
//   - dest is not writable, or its directory does not exist. Checked up front
//     for the same reason: SQLite reports this as "unable to open database
//     file", which reads like a problem with the source database rather than
//     with the destination the operator just typed.
//
//   - the database is busy. VACUUM INTO only takes a read transaction, and in
//     WAL mode readers do not block the writer, so a backup can be taken while
//     checks are being recorded. It runs on the reader pool so it does not
//     occupy the single writer connection for the length of the copy. If the
//     file is locked by something outside this process, busy_timeout (5s)
//     applies and the error is returned rather than retried forever — a
//     backup that hangs is worse than one that fails loudly in a cron log.
func (db *DB) BackupTo(ctx context.Context, dest string) (int64, error) {
	if dest == "" {
		return 0, errors.New("store: backup destination must not be empty")
	}

	// Resolve relative paths against this process's working directory, so the
	// error messages and the log line name the file SQLite will actually
	// write rather than something the operator has to guess at.
	abs, err := filepath.Abs(dest)
	if err != nil {
		return 0, fmt.Errorf("resolve backup path %s: %w", dest, err)
	}

	if _, err := os.Stat(abs); err == nil {
		return 0, fmt.Errorf("store: backup destination %s already exists; "+
			"pick a new filename or remove the old backup first", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("check backup destination %s: %w", abs, err)
	}

	dir := filepath.Dir(abs)
	info, err := os.Stat(dir)
	if err != nil {
		return 0, fmt.Errorf("backup directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("store: backup directory %s is not a directory", dir)
	}

	// SQLite takes the VACUUM INTO target as an ordinary expression, so it can
	// be bound rather than pasted into the statement. That keeps a path
	// containing a quote from being able to alter the statement at all.
	if _, err := db.Reader.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		// A failed VACUUM INTO can leave a partial file behind. Removing it
		// matters: otherwise the next attempt fails with "already exists" and
		// the operator is left holding a truncated database that looks like a
		// backup.
		_ = os.Remove(abs)
		return 0, fmt.Errorf("vacuum into %s: %w", abs, err)
	}

	// Report the size from here rather than letting the caller stat a path it
	// took from the command line.
	info, err = os.Stat(abs)
	if err != nil {
		return 0, fmt.Errorf("stat backup %s: %w", abs, err)
	}
	return info.Size(), nil
}
