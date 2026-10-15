//go:build unix

package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// withUmask sets the process umask for one test. 022 is the common default,
// under which SQLite on its own creates files readable by everyone; a CI
// runner with 077 would otherwise pass these tests without the fix.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %v, want %v", filepath.Base(path), got, want)
	}
}

// A new database and the -wal and -shm SQLite keeps beside it are readable
// by their owner only, whatever the umask leaves open.
func TestNewDatabaseFilesAreOwnerOnly(t *testing.T) {
	withUmask(t, 0o022)
	path := filepath.Join(t.TempDir(), "subglance.db")

	db, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// A write, so the WAL has content and is not only a header.
	if _, err := db.Writer.Exec("INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 0)"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		requireMode(t, p, 0o600)
	}
}

// Opening a database that already exists does not change its mode: the
// narrowing of an existing file is RestrictFileModes', at server startup,
// where it is logged.
func TestOpenLeavesAnExistingDatabaseModeAlone(t *testing.T) {
	withUmask(t, 0o022)
	path := filepath.Join(t.TempDir(), "subglance.db")
	db, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	db, err = Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	requireMode(t, path, 0o640)
}

// A backup holds everything the database does, so it is owner-only too.
func TestBackupFileIsOwnerOnly(t *testing.T) {
	withUmask(t, 0o022)
	db := openTestDB(t)
	dest := filepath.Join(t.TempDir(), "backup.db")

	if _, err := db.BackupTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	requireMode(t, dest, 0o600)
	// The empty file created first must not stop VACUUM INTO from writing
	// a whole database into it.
	var n int
	if err := openBackupCopy(t, dest).QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n == 0 {
		t.Errorf("backup has %d migrations (err %v), want the schema ledger", n, err)
	}
}

// A database left readable by others, by an older version or by a copy, is
// narrowed to owner-only, and the files changed are reported once.
func TestRestrictFileModesNarrowsReadableFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subglance.db")
	modes := map[string]os.FileMode{
		path:          0o644,
		path + "-wal": 0o640,
		path + "-shm": 0o400, // narrower already: left as it is
	}
	for p, m := range modes {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, m); err != nil {
			t.Fatal(err)
		}
	}

	changed, err := RestrictFileModes(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{path, path + "-wal"}; !slices.Equal(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	requireMode(t, path, 0o600)
	requireMode(t, path+"-wal", 0o600)
	requireMode(t, path+"-shm", 0o400)

	again, err := RestrictFileModes(path)
	if err != nil || len(again) != 0 {
		t.Errorf("second call: changed = %v, err = %v; want nothing to report", again, err)
	}
}

// No database yet is nothing to do, not an error: the first start creates it.
func TestRestrictFileModesWithoutADatabase(t *testing.T) {
	changed, err := RestrictFileModes(filepath.Join(t.TempDir(), "subglance.db"))
	if err != nil || len(changed) != 0 {
		t.Errorf("changed = %v, err = %v; want nothing", changed, err)
	}
}

// A database path that is a symlink to a file that does not exist yet gets
// that file created owner-only, rather than being taken for an existing
// database and left to SQLite to create.
func TestNewDatabaseBehindADanglingSymlinkIsOwnerOnly(t *testing.T) {
	withUmask(t, 0o022)
	dir := t.TempDir()
	target := filepath.Join(dir, "data", "real.db")
	if err := os.Mkdir(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	// Relative, and through a second link, as a hand-made layout might be.
	hop := filepath.Join(dir, "hop.db")
	if err := os.Symlink("data/real.db", hop); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "subglance.db")
	if err := os.Symlink("hop.db", link); err != nil {
		t.Fatal(err)
	}

	db, err := Open(context.Background(), Options{Path: link})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer.Exec("INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 0)"); err != nil {
		t.Fatal(err)
	}
	requireMode(t, target, 0o600)
}

// A symlink loop is an error from Open, not a hang.
func TestOpenRefusesASymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(context.Background(), Options{Path: a}); err == nil {
		_ = db.Close()
		t.Fatal("Open followed a symlink loop without an error")
	}
}

// A "file:" URI or a name with "?" would reach SQLite as a different file
// than the one created owner-only, so Open refuses it before creating
// anything.
func TestOpenRefusesAURIOrAQuery(t *testing.T) {
	withUmask(t, 0o022)
	dir := t.TempDir()
	for _, path := range []string{
		"file:" + filepath.Join(dir, "uri.db"),
		"file:" + filepath.Join(dir, "uri.db") + "?mode=rwc",
		filepath.Join(dir, "query.db") + "?_pragma=foreign_keys(ON)",
	} {
		if db, err := Open(context.Background(), Options{Path: path}); err == nil {
			_ = db.Close()
			t.Errorf("Open(%q) succeeded, want a refusal", path)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("Open left %s behind after refusing", e.Name())
	}
}
