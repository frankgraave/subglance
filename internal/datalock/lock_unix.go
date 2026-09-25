//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package datalock

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock without blocking. held is true when
// another open file description already holds it.
func tryLock(f *os.File) (held bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}
