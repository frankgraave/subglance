//go:build windows

package store

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// availableBytes is the space this process may still write on the volume
// holding dir, after any per-user quota.
func availableBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return int64(avail), nil
}

func onSameFilesystem(a, b string) bool {
	va, errA := filepath.Abs(a)
	vb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return true
	}
	return strings.EqualFold(filepath.VolumeName(va), filepath.VolumeName(vb))
}

func writableDir(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
