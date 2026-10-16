package main

import (
	"errors"
	"fmt"
	"os"
)

// requireDatabase refuses a data directory that holds no database, for the
// subcommands that act on the server's existing data: backup and
// reset-password. Both open the store, and store.Open creates and migrates a
// database where there is none, so a mistyped --data-dir or an unset
// SUBGLANCE_DATA_DIR would otherwise leave an empty database in the wrong
// directory and carry on as if it were the server's. For reset-password that
// ends in "no such account", which sends the operator looking for the wrong
// problem; for backup it ends in a successful backup of nothing, which a
// nightly job repeats until the day the copy is needed.
//
// It is checked before the data directory is locked or anything is opened,
// so a refusal leaves the directory exactly as it was.
func requireDatabase(dbPath string) error {
	_, err := os.Stat(dbPath) //nolint:gosec // G703: the configured database path, only stat'ed
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("there is no database at %s; point --data-dir (or SUBGLANCE_DATA_DIR) "+
			"at the directory the server uses", dbPath)
	}
	if err != nil {
		return fmt.Errorf("check database %s: %w", dbPath, err)
	}
	return nil
}
