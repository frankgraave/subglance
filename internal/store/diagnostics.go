package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// FileStats describes the database as a file on disk, for the diagnostics
// card on the settings page.
type FileStats struct {
	// Path is where the database lives, without any DSN query string.
	Path string

	// Bytes is the size of the main database file. WALBytes is the
	// write-ahead log beside it, reported separately because a WAL that
	// keeps growing is its own finding (a reader holding a snapshot open
	// stops checkpoints), and adding the two together would hide it.
	Bytes    int64
	WALBytes int64

	// JournalMode is what SQLite reports, not what the DSN asked for. The
	// two differ on filesystems that cannot do WAL, which is exactly the
	// case worth seeing.
	JournalMode string
}

// FileStats reports the database file's size and journal mode.
//
// A missing WAL file is not an error: SQLite removes it on a clean close and
// recreates it on the next write, so zero is a real reading.
func (db *DB) FileStats(ctx context.Context) (FileStats, error) {
	st := FileStats{Path: db.Path()}

	if err := db.Reader.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&st.JournalMode); err != nil {
		return FileStats{}, fmt.Errorf("read journal_mode: %w", err)
	}

	main, err := os.Stat(st.Path)
	if err != nil {
		return FileStats{}, fmt.Errorf("stat database: %w", err)
	}
	st.Bytes = main.Size()

	wal, err := os.Stat(st.Path + "-wal")
	switch {
	case err == nil:
		st.WALBytes = wal.Size()
	case !errors.Is(err, fs.ErrNotExist):
		return FileStats{}, fmt.Errorf("stat wal: %w", err)
	}
	return st, nil
}
