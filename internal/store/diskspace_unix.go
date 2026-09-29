//go:build linux || darwin || freebsd

package store

import (
	"golang.org/x/sys/unix"
)

// availableBytes is the space an unprivileged process may still write on the
// filesystem holding dir: Bavail, not Bfree, because the blocks reserved for
// root are not this process's to count on.
func availableBytes(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(toUint64(st.Bavail) * toUint64(st.Bsize)), nil
}

// toUint64 widens a Statfs field. The field types differ per platform (Bsize
// is int64 on linux, uint32 on darwin, uint64 on freebsd), so a plain
// conversion is redundant on some of them and required on others.
func toUint64[T ~int64 | ~uint64 | ~uint32](v T) uint64 { return uint64(v) }

func onSameFilesystem(a, b string) bool {
	var sa, sb unix.Stat_t
	if unix.Stat(a, &sa) != nil || unix.Stat(b, &sb) != nil {
		// Unknown counts as shared: the combined check is the stricter one.
		return true
	}
	return sa.Dev == sb.Dev
}

func writableDir(dir string) bool {
	var st unix.Stat_t
	if unix.Stat(dir, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false
	}
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}
