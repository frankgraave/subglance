package backup

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	// The driver store uses, registered for the integrity check below.
	_ "modernc.org/sqlite"
)

// maxRestoredBytes caps how much a download may decompress to. The bucket is
// the operator's own, but a wrong prefix or a shared bucket can hold anything,
// and a small object that expands until the disk is full would take the
// current database down with it. 64 GiB is far beyond any database one
// instance produces.
const maxRestoredBytes = 64 << 30

// RestoreOptions configures Restore.
type RestoreOptions struct {
	Target          Target
	AccessKeyID     string
	SecretAccessKey string

	// Name picks one backup by object name. Empty means the newest.
	Name string

	// DBPath is the database to replace.
	DBPath string

	Now func() time.Time
}

// RestoreResult says what Restore did.
type RestoreResult struct {
	// Object is the backup that was restored.
	Object string
	// SetAside lists the files that were moved out of the way rather than
	// deleted: the database that was there, and its -wal and -shm files.
	SetAside []string
}

// Restore downloads one backup and puts it in place of the database.
//
// Nothing is deleted. The database that was there, and its -wal and -shm
// files, are renamed with a .before-restore-<time> suffix, because restoring
// the wrong backup must be something an operator can undo. The -wal and -shm
// files have to move in any case: they belong to the old database, and SQLite
// would replay them over the restored one.
//
// The download is checked before anything is moved. A backup that does not
// decompress or does not pass SQLite's quick_check leaves the existing
// database exactly where it was.
//
// Restore does not know whether the server is running; the caller checks,
// because replacing the file under a live process is the one way to lose data
// here.
func Restore(ctx context.Context, opts RestoreOptions) (RestoreResult, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	c := newClient(opts.Target, credentials{AccessKeyID: opts.AccessKeyID, SecretAccessKey: opts.SecretAccessKey})
	c.now = opts.Now

	names, err := listBackups(ctx, c, opts.Target)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("list backups in %s: %w", opts.Target, err)
	}
	if len(names) == 0 {
		return RestoreResult{}, fmt.Errorf("no backups found in %s", opts.Target)
	}
	name := names[len(names)-1]
	if opts.Name != "" {
		name = ""
		for _, n := range names {
			if n == opts.Name {
				name = n
			}
		}
		if name == "" {
			return RestoreResult{}, fmt.Errorf("no backup named %s in %s; the newest is %s",
				opts.Name, opts.Target, names[len(names)-1])
		}
	}

	dir := filepath.Dir(opts.DBPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return RestoreResult{}, err
	}
	stamp := opts.Now().UTC().Format(objectTimeLayout)
	staged := filepath.Join(dir, ".restore-"+stamp+".db")
	defer func() { _ = os.Remove(staged) }()

	if err := Download(ctx, c, opts.Target, name, staged); err != nil {
		return RestoreResult{}, err
	}
	if err := quickCheck(ctx, staged); err != nil {
		return RestoreResult{}, fmt.Errorf("backup %s is not a usable database: %w", name, err)
	}

	res := RestoreResult{Object: name}
	// The suffix goes after the stamp, so the set-aside files keep SQLite's
	// pairing: the WAL of db.before-restore-<stamp> is looked for at
	// db.before-restore-<stamp>-wal, and a crashed server's WAL can hold
	// committed transactions the main file does not have yet.
	var moved []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := opts.DBPath + suffix
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		aside := opts.DBPath + ".before-restore-" + stamp + suffix
		if err := os.Rename(path, aside); err != nil {
			putBack(res.SetAside, moved)
			return RestoreResult{Object: name}, fmt.Errorf("move %s aside: %w", path, err)
		}
		res.SetAside = append(res.SetAside, aside)
		moved = append(moved, path)
	}
	if err := os.Rename(staged, opts.DBPath); err != nil {
		putBack(res.SetAside, moved)
		return RestoreResult{Object: name}, fmt.Errorf("put the restored database in place: %w", err)
	}
	return res, nil
}

// putBack undoes the renames of a restore that failed partway, newest first,
// so a failure leaves the database where it was instead of leaving no
// database at all — which the next start would silently replace with an
// empty one.
func putBack(aside, original []string) {
	for i := len(aside) - 1; i >= 0; i-- {
		_ = os.Rename(aside[i], original[i])
	}
}

// Download fetches one backup and writes it, decompressed, to dest. It
// refuses to overwrite dest, for the same reason `subglance backup` does.
func Download(ctx context.Context, c *client, t Target, name, dest string) error {
	body, err := c.get(ctx, t.Prefix+name)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("backup %s is not in %s", name, t)
		}
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer func() { _ = body.Close() }()

	zr, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("download %s: not a gzip file: %w", name, err)
	}
	// dest is the operator's data directory or a test's temp dir.
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-configured path
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(zr, maxRestoredBytes+1))
	if err == nil && n > maxRestoredBytes {
		err = fmt.Errorf("decompresses to more than %d GiB, which is not a SubGlance backup", maxRestoredBytes>>30)
	}
	if err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("download %s: %w", name, err)
	}
	// On disk before it replaces the database: after a crash, a renamed but
	// unsynced file can come back empty, with the old database already moved.
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("download %s: %w", name, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func quickCheck(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if !strings.EqualFold(result, "ok") {
		return errors.New(result)
	}
	// SQLite opens a zero-length file as a valid, empty database, and
	// quick_check passes it. A backup with no tables would replace the real
	// database with nothing.
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return errors.New("it holds no tables")
	}
	return nil
}
