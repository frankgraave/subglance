package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The numbers on the diagnostics card must be the file on disk, not a figure
// derived from page counts: an operator comparing them with `ls -l` should
// find the same bytes.
func TestFileStatsReportsTheFilesOnDisk(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "diag.db")
	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st, err := db.FileStats(ctx)
	if err != nil {
		t.Fatalf("FileStats: %v", err)
	}
	if st.Path != path {
		t.Errorf("Path = %q, want %q", st.Path, path)
	}
	if st.JournalMode != "wal" {
		t.Errorf("JournalMode = %q, want wal", st.JournalMode)
	}

	main, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Bytes != main.Size() || st.Bytes == 0 {
		t.Errorf("Bytes = %d, want the file's %d", st.Bytes, main.Size())
	}
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatalf("a migrated WAL database should have a -wal file: %v", err)
	}
	if st.WALBytes != wal.Size() {
		t.Errorf("WALBytes = %d, want the -wal file's %d", st.WALBytes, wal.Size())
	}
}
