//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// requireDataAccess refuses a data directory, or a file in it, that this
// process cannot write, before anything in it is locked or opened.
//
// Without it the refusal comes from deep inside whatever touches the file
// first, and says nothing an operator can act on: "lock data dir: open lock
// file: permission denied" for a directory, and SQLite's "unable to open
// database file (14)" for a database copied in by root. The container runs as
// an unprivileged user, so a restore done with a borrowed root shell, a bind
// mount of a host directory, or a --user that differs from the one that
// created the files all end there. The refusal names the file, who owns it,
// who SubGlance runs as, and the chown that fixes it.
//
// Files that do not exist yet are skipped; the directory they will be
// created in is checked instead. Anything other than a permission refusal is
// left to the open that follows, which says it better.
func requireDataAccess(dataDir string, files []dataFile) error {
	dir := filepath.Clean(dataDir)
	if err := requireDirAccess(dir); err != nil {
		return err
	}
	for _, f := range files {
		mode := uint32(unix.R_OK)
		if f.write {
			mode |= unix.W_OK
		}
		if err := checkAccess(f.path, mode); err != nil {
			return accessRefusal(f.path, dir, f.write, err)
		}
	}
	return nil
}

// requireDirAccess checks that the data directory can have files created in
// it, or, when it does not exist yet, that the nearest directory above it
// can.
func requireDirAccess(dir string) error {
	existing := dir
	for {
		info, err := os.Stat(existing)
		if err == nil && !info.IsDir() {
			return nil // not a directory: the MkdirAll that follows says so
		}
		if err == nil {
			break
		}
		if errors.Is(err, os.ErrPermission) {
			return unreachableRefusal(dir, existing)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil // left to the MkdirAll that follows
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return nil
		}
		existing = parent
	}
	err := checkAccess(existing, unix.W_OK|unix.X_OK)
	if err == nil {
		return nil
	}
	if existing == dir {
		return accessRefusal(dir, dir, true, err)
	}
	if errors.Is(err, unix.EROFS) {
		return fmt.Errorf("cannot create the data directory %s: %s is on a read-only file system", dir, existing)
	}
	return fmt.Errorf("cannot create the data directory %s: %s %s, and SubGlance runs as %s; "+
		"create it and give it to that user (mkdir -p %s && chown %s %s, as root), "+
		"or point --data-dir somewhere it can write",
		dir, existing, ownerOf(existing), processUser(), dir, processOwner(), dir)
}

// unreachableRefusal names the directory above the data directory that
// this process may not pass through. A stat of path fails with a permission
// error only when a directory on the way to it lacks search permission, and
// the nearest one above path that can still be looked at is that directory:
// everything up to it is searchable, and it is not. A chown of the data
// directory does not fix that, so the refusal does not suggest one.
func unreachableRefusal(dir, path string) error {
	blocked := path
	var info os.FileInfo
	for {
		parent := filepath.Dir(blocked)
		if parent == blocked {
			break
		}
		blocked = parent
		var err error
		if info, err = os.Stat(blocked); err == nil {
			break
		}
	}
	fix := fmt.Sprintf("let that user search it (chmod o+x %s, as root)", blocked)
	if info != nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
			fix = fmt.Sprintf("let its owner search it (chmod u+x %s)", blocked)
		}
	}
	return fmt.Errorf("cannot reach the data directory %s: %s %s, and SubGlance runs as %s, "+
		"which may not pass through it; %s, or point --data-dir somewhere it can reach",
		dir, blocked, ownerOf(blocked), processUser(), fix)
}

// remedy is the one command that fixes a refusal of path. A file the
// process already owns has the wrong mode, which no chown changes; anything
// else belongs to another user, and the data directory as a whole goes to
// the process, because the files SQLite creates beside the database follow
// the directory they are created in.
func remedy(path, dataDir string) string {
	info, err := os.Stat(path)
	if err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
			bits := "u+rw"
			if info.IsDir() {
				bits = "u+rwx"
			}
			return fmt.Sprintf("make it usable by its owner (chmod %s %s)", bits, path)
		}
	}
	return fmt.Sprintf("give the data directory to that user (chown -R %s %s, as root), or run SubGlance as the owner",
		processOwner(), dataDir)
}

// checkAccess reports a permission refusal for path, and nothing else: a
// file that does not exist, or a check that fails for another reason, is
// for the open that follows.
func checkAccess(path string, mode uint32) error {
	err := unix.Access(path, mode)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS) {
		return err
	}
	return nil
}

func accessRefusal(path, dataDir string, write bool, err error) error {
	verb := "read"
	if write {
		verb = "write"
	}
	if errors.Is(err, unix.EROFS) {
		return fmt.Errorf("cannot %s %s: the data directory %s is on a read-only file system", verb, path, dataDir)
	}
	return fmt.Errorf("cannot %s %s: it %s, and SubGlance runs as %s; %s",
		verb, path, ownerOf(path), processUser(), remedy(path, dataDir))
}

// ownerOf describes who owns path and its mode, in the numbers chown takes:
// inside a container a name means nothing on the host.
func ownerOf(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "cannot be read"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Sprintf("has mode %v", info.Mode())
	}
	return fmt.Sprintf("belongs to %d:%d with mode %v", st.Uid, st.Gid, info.Mode())
}

func processUser() string {
	return fmt.Sprintf("user %d (group %d)", os.Getuid(), os.Getgid())
}

func processOwner() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}
