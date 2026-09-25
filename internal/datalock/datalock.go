// Package datalock keeps two processes from using one data directory at once.
//
// The server holds an exclusive lock on a file in its data directory for as
// long as it runs, and `subglance restore` takes the same lock before it
// replaces the database. The lock is the operating system's own (flock on
// Unix, LockFileEx on Windows), so the kernel releases it when the holder
// exits, crash included: there is no stale lock to clean up and no PID file
// to second-guess.
//
// The lock file itself is never deleted. Removing it on exit would let a
// second process lock a fresh file at the same path while a third still
// holds the old one, and the two would both believe they own the directory.
package datalock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the lock file inside the data directory.
const FileName = "subglance.lock"

// ErrLocked means another process holds the data directory.
var ErrLocked = errors.New("data directory is in use by another process")

// Lock is a held data directory lock.
type Lock struct {
	f    *os.File
	path string
}

// Path is the lock file, for messages that tell the operator what to look at.
func Path(dir string) string {
	return filepath.Join(dir, FileName)
}

// Acquire takes the lock on dir without waiting. When another process holds
// it, the error wraps ErrLocked and names the lock file.
func Acquire(dir string) (*Lock, error) {
	path := filepath.Clean(Path(dir))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	held, err := tryLock(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if held {
		_ = f.Close()
		return nil, fmt.Errorf("%w (lock file %s)", ErrLocked, path)
	}
	return &Lock{f: f, path: path}, nil
}

// Path is the lock file this lock holds.
func (l *Lock) Path() string { return l.path }

// Release gives the lock up. Closing the file is what releases it, on every
// platform; a process that exits without calling Release loses the lock the
// same way.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
