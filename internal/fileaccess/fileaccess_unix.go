//go:build unix

package fileaccess

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// RequireReadable refuses path, which the setting named setting points at,
// when something is there and this process may not read it.
//
// Anything else is left to the read that follows, which says it better: an
// empty setting, a name with nothing behind it (for --secret-key that is
// key material rather than a path, and must never be repeated, so nothing
// about it is said here), a dangling link, or a directory on the way that
// cannot be searched. Only a file that demonstrably exists is named.
//
// It checks with access(2) rather than by opening the file: an open of a
// FIFO blocks, and this runs before anything else at startup.
func RequireReadable(setting, path string) error {
	if path == "" || !present(path) {
		return nil
	}
	err := unix.Access(path, unix.R_OK)
	if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) {
		return nil
	}
	return fmt.Errorf("%s names %s, which SubGlance cannot read: it %s, and SubGlance runs as %s; %s",
		setting, path, Owner(path), ProcessUser(), readFix(path))
}

// present says whether something is at path, a dangling link included. A
// stat that fails for any reason is not a file this check can name.
func present(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readFix is the one command that makes path readable. A file the process
// already owns has lost its owner's read bit, which no chown changes;
// anything else goes to the process, and stays readable by its owner only.
// In a container the path is the one inside it, so the chown is for the
// file on the host that is mounted there.
func readFix(path string) string {
	owned, ownerReads := false, true
	if info, err := os.Stat(path); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		owned = ok && int(st.Uid) == os.Getuid()
		ownerReads = info.Mode().Perm()&0o400 != 0
	}
	return fixFor(path, owned, ownerReads)
}

// fixFor is readFix once the file's owner and mode are known. A file that
// goes to the process without its owner's read bit is still unreadable
// after the chown, so that remedy adds the chmod; a file whose owner can
// already read it is not touched beyond the chown.
func fixFor(path string, owned, ownerReads bool) string {
	chmod := "chmod u+r " + path
	if owned {
		return fmt.Sprintf("make it readable by its owner (%s)", chmod)
	}
	cmd := fmt.Sprintf("chown %s %s", ProcessOwner(), path)
	runAsOwner := "run SubGlance as its owner"
	if !ownerReads {
		cmd += " && " + chmod
		runAsOwner = fmt.Sprintf("make it readable by its owner (%s) and run SubGlance as that owner", chmod)
	}
	return fmt.Sprintf("give it to that user (%s, as root; in a container, on the host file "+
		"mounted there), or %s", cmd, runAsOwner)
}

// Owner describes who owns path and its mode, in the numbers chown takes:
// inside a container a name means nothing on the host.
func Owner(path string) string {
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

// ProcessUser names the user and group this process runs as, for a message.
func ProcessUser() string {
	return fmt.Sprintf("user %d (group %d)", os.Getuid(), os.Getgid())
}

// ProcessOwner is the user and group this process runs as, as chown takes
// them.
func ProcessOwner() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}
