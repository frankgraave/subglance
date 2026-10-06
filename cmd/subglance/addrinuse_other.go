//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// addrInUse reports whether a bind failed because another socket already
// holds the address.
func addrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
