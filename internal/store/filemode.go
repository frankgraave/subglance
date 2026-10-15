package store

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

// ownerOnly is the mode of every file that holds the database. It holds
// password hashes and sessions, and without --secret-key every channel
// credential in plain text, so nobody but the account running SubGlance has
// a reason to read it. The files around it (the scheduled backup archive, the
// restore staging file, the mute-link key, the data-directory lock) were
// already created this way.
const ownerOnly os.FileMode = 0o600

// walSuffixes are the files SQLite keeps beside the database in WAL mode.
// The WAL holds recent writes in full, so it is as sensitive as the database.
var walSuffixes = []string{"-wal", "-shm"}

// createOwnerOnly creates path as an empty file readable by its owner only.
// A file that already exists is left alone and reported as os.ErrExist, so
// the caller decides whether that is fine (the database) or a refusal (a
// backup destination).
//
// SQLite would otherwise create the database with 0644 minus the umask, and
// it gives -wal and -shm the mode of the database file, so creating the
// database first is what sets the mode of all three. SQLite treats an empty
// file as a new database, and VACUUM INTO accepts an empty target.
func createOwnerOnly(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ownerOnly) //nolint:gosec // the configured database path or a backup destination
	if err != nil {
		return err
	}
	return f.Close()
}

// RestrictFileModes makes the database at path, and its -wal and -shm when
// they exist, readable by the owner only. It returns the files it changed,
// so the caller can say so once; a file that is already owner-only, or that
// does not exist, is left out.
//
// It is for a database created before SubGlance created it owner-only, or
// copied into place by hand. It does nothing on Windows, where a file's Go
// mode only says whether it is read-only.
func RestrictFileModes(path string) ([]string, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	var changed []string
	var errs []error
	for _, p := range append([]string{path}, path+walSuffixes[0], path+walSuffixes[1]) {
		info, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.Mode().Perm()&^ownerOnly == 0 {
			continue
		}
		if err := os.Chmod(p, info.Mode().Perm()&ownerOnly); err != nil {
			errs = append(errs, fmt.Errorf("restrict %s: %w", p, err))
			continue
		}
		changed = append(changed, p)
	}
	return changed, errors.Join(errs...)
}
