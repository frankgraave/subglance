//go:build unix

package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/datalock"
	"github.com/frankgraave/subglance/internal/mutelink"
)

// skipAsRoot skips a test that takes a permission away: root is refused
// nothing a mode bit says, so the refusal under test cannot happen. CI runs
// these as an ordinary user.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes; run as an ordinary user")
	}
}

// chmod sets a mode and puts the owner's access back afterwards, so the
// test's temporary directory can still be removed.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

// wantRefusal checks that err is the data-access refusal for path: it names
// the file, its owner and mode, the user SubGlance runs as, and a command
// that fixes it.
func wantRefusal(t *testing.T, err error, path, fix string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no refusal; want one naming %s", path)
	}
	msg := err.Error()
	for _, want := range []string{path, "belongs to", "SubGlance runs as user", fix} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "unable to open database file") {
		t.Errorf("refusal is SQLite's, not this check's:\n%s", msg)
	}
}

// runServerOnce starts the server on dataDir with its port already taken,
// so a server that gets past the data directory stops at the bind instead
// of running for the rest of the test binary.
func runServerOnce(t *testing.T, dataDir string) error {
	t.Helper()
	taken, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"--data-dir", dataDir, "--addr", taken.Addr().String()}, &lockedBuffer{})
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the server kept running on a port another process holds")
		return nil
	}
}

// A database the server cannot write is refused before the data directory
// is locked or the database opened. This is a database copied in by another
// user, or by root, and left with a mode that shuts the server out: the
// open would otherwise fail with "unable to open database file (14)".
func TestServerRefusesADatabaseItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	clearBackupEnv(t)
	dataDir := seededDataDir(t)
	dbPath := filepath.Join(dataDir, "subglance.db")
	chmod(t, dbPath, 0o400)

	err := runServerOnce(t, dataDir)
	wantRefusal(t, err, "cannot write "+dbPath, "chmod u+rw "+dbPath)
	if _, statErr := os.Stat(datalock.Path(dataDir)); !os.IsNotExist(statErr) {
		t.Errorf("the lock file was created before the refusal (stat: %v)", statErr)
	}
}

// A data directory the server cannot create files in is refused with the
// directory named, instead of "open lock file: permission denied".
func TestServerRefusesADataDirItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	clearBackupEnv(t)
	dataDir := t.TempDir()
	chmod(t, dataDir, 0o500)

	err := runServerOnce(t, dataDir)
	wantRefusal(t, err, "cannot write "+dataDir, "chmod u+rwx "+dataDir)
}

// The -wal beside the database is the server's to write too; one left
// behind by another user would stop the first write after the open.
func TestServerRefusesAWALItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	clearBackupEnv(t)
	dataDir := seededDataDir(t)
	wal := filepath.Join(dataDir, "subglance.db-wal")
	if err := os.WriteFile(wal, nil, 0o400); err != nil {
		t.Fatal(err)
	}

	err := runServerOnce(t, dataDir)
	wantRefusal(t, err, "cannot write "+wal, "chmod u+rw "+wal)
}

// The mute-link key is only read, so a read-only key is fine, and one the
// server cannot read at all is refused.
func TestServerReadsTheMuteLinkKeyOnly(t *testing.T) {
	skipAsRoot(t)
	dataDir := t.TempDir()
	key := filepath.Join(dataDir, mutelink.KeyFile)
	if err := os.WriteFile(key, []byte("00\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	files := serverFiles(dataDir, filepath.Join(dataDir, "subglance.db"))
	if err := requireDataAccess(dataDir, files); err != nil {
		t.Fatalf("a read-only mute-link key was refused: %v", err)
	}
	chmod(t, key, 0o000)
	wantRefusal(t, requireDataAccess(dataDir, files), "cannot read "+key, "chmod u+rw "+key)
}

// A data directory that does not exist yet is created by the server, so the
// directory above it is what has to be writable. "/" belongs to root, so
// this is also the refusal for a path owned by another user: it says to
// create the directory and give it to the user SubGlance runs as.
func TestServerRefusesADataDirItCannotCreate(t *testing.T) {
	skipAsRoot(t)
	dataDir := filepath.Join("/", "subglance-data-access-test", "data")
	err := requireDataAccess(dataDir, serverFiles(dataDir, filepath.Join(dataDir, "subglance.db")))
	if err == nil {
		t.Fatal("no refusal for a data directory under / ")
	}
	msg := err.Error()
	for _, want := range []string{
		"cannot create the data directory " + dataDir,
		"/ belongs to 0:",
		"SubGlance runs as user",
		"mkdir -p " + dataDir + " && chown " + processOwner() + " " + dataDir,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
}

// A file that belongs to another user is not the process's to chmod; the
// fix is to give the whole data directory to the user SubGlance runs as.
func TestRefusalOfAnotherUsersFileSaysChown(t *testing.T) {
	skipAsRoot(t)
	got := remedy("/", "/data")
	want := "chown -R " + processOwner() + " /data"
	if !strings.Contains(got, want) {
		t.Errorf("remedy for a root-owned path = %q, want it to contain %q", got, want)
	}
}

// A data directory and database the process can use pass, including one
// whose database does not exist yet.
func TestDataAccessAllowsAUsableDataDir(t *testing.T) {
	dataDir := seededDataDir(t)
	if err := requireDataAccess(dataDir, serverFiles(dataDir, filepath.Join(dataDir, "subglance.db"))); err != nil {
		t.Errorf("a usable data directory was refused: %v", err)
	}
	fresh := filepath.Join(t.TempDir(), "not", "yet")
	if err := requireDataAccess(fresh, serverFiles(fresh, filepath.Join(fresh, "subglance.db"))); err != nil {
		t.Errorf("a data directory still to be created was refused: %v", err)
	}
}

// backup opens the store, which migrates and writes the -shm index, so a
// database it cannot write is refused before the open, not by SQLite.
func TestBackupRefusesADatabaseItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	dataDir := seededDataDir(t)
	dbPath := filepath.Join(dataDir, "subglance.db")
	chmod(t, dbPath, 0o400)
	dest := filepath.Join(t.TempDir(), "backup.db")

	err := runBackup([]string{dest, "--data-dir", dataDir}, &bytes.Buffer{})
	wantRefusal(t, err, "cannot write "+dbPath, "chmod u+rw "+dbPath)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("a backup file was written despite the refusal (stat: %v)", statErr)
	}
}

// reset-password is refused the same way, before it asks for a password.
func TestResetPasswordRefusesADatabaseItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	l := newLockedOut(t)
	dbPath := filepath.Join(l.dir, "subglance.db")
	chmod(t, dbPath, 0o400)
	secrets := typed(resetNewPassword, resetNewPassword)

	err := resetPassword(l.args(t), &bytes.Buffer{}, secrets)
	wantRefusal(t, err, "cannot write "+dbPath, "chmod u+rw "+dbPath)
	if len(secrets.prompts) != 0 {
		t.Errorf("asked for a password it could not set: %q", secrets.prompts)
	}
}

// A restore into a data directory it cannot write is refused before any
// byte is downloaded: the download is staged in that directory.
func TestRestoreRefusesADataDirItCannotWrite(t *testing.T) {
	skipAsRoot(t)
	var requests int
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s3.Close()
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://bucket")
	t.Setenv("SUBGLANCE_BACKUP_ENDPOINT", s3.URL)
	t.Setenv("SUBGLANCE_BACKUP_ACCESS_KEY_ID", "key-id")
	t.Setenv("SUBGLANCE_BACKUP_SECRET_ACCESS_KEY", "secret")

	dataDir := t.TempDir()
	chmod(t, dataDir, 0o500)

	err := runRestore([]string{"--data-dir", dataDir, "--addr", freeAddr(t)}, &bytes.Buffer{})
	wantRefusal(t, err, "cannot write "+dataDir, "chmod u+rwx "+dataDir)
	if requests != 0 {
		t.Errorf("the restore reached the bucket %d times", requests)
	}
}
