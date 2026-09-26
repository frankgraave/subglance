package main

import (
	"errors"
	"fmt"

	"github.com/frankgraave/subglance/internal/datalock"
)

// lockDataDir takes the data directory for the server. A second server on the
// same directory would run every check twice and race the first one for the
// database, so it is refused with the lock file named: that is the thing an
// operator can look at to see who holds it.
func lockDataDir(dir string) (*datalock.Lock, error) {
	lock, err := datalock.Acquire(dir)
	if errors.Is(err, datalock.ErrLocked) {
		return nil, fmt.Errorf("another SubGlance process is using %s (it holds %s); "+
			"run one server per data directory", dir, datalock.Path(dir))
	}
	if err != nil {
		return nil, fmt.Errorf("lock data dir: %w", err)
	}
	return lock, nil
}
