//go:build !linux && !darwin && !freebsd && !windows

package store

import (
	"errors"
	"os"
)

// errDiskSpaceUnknown makes Compact refuse where free space cannot be read: a
// VACUUM that fills the disk also stops every heartbeat from being written, so
// not knowing is treated as not having room.
var errDiskSpaceUnknown = errors.New("free disk space cannot be read on this platform")

func availableBytes(string) (int64, error) { return 0, errDiskSpaceUnknown }

func onSameFilesystem(string, string) bool { return true }

func writableDir(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
