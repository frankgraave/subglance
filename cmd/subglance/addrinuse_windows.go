//go:build windows

package main

import (
	"errors"
	"syscall"
)

// wsaeAddrInUse is WSAEADDRINUSE, the number Winsock reports when the address
// is already bound. On Windows the syscall package defines EADDRINUSE as one
// of Go's own invented numbers, which a real bind failure never carries, so
// matching that alone would never recognise the condition.
const wsaeAddrInUse syscall.Errno = 10048

// addrInUse reports whether a bind failed because another socket already
// holds the address.
func addrInUse(err error) bool {
	return errors.Is(err, wsaeAddrInUse) || errors.Is(err, syscall.EADDRINUSE)
}
