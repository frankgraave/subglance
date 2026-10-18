package main

import (
	"path/filepath"
	"strings"

	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/datalock"
	"github.com/frankgraave/subglance/internal/mutelink"
)

// dataFile is one file in the data directory a command opens, and whether it
// opens it for writing.
type dataFile struct {
	path  string
	write bool
}

// databaseFiles are the database and the two files SQLite keeps beside it in
// WAL mode. Every command that opens the store writes all three: opening runs
// the migrations, and a reader in WAL mode still writes the -shm index.
func databaseFiles(dbPath string) []dataFile {
	return []dataFile{
		{path: dbPath, write: true},
		{path: dbPath + "-wal", write: true},
		{path: dbPath + "-shm", write: true},
	}
}

// lockFile is the data directory lock, which is opened read-write to be held.
func lockFile(dataDir string) dataFile {
	return dataFile{path: datalock.Path(dataDir), write: true}
}

// serverFiles are the files the server opens in its data directory: the
// lock, the database, and, when readsKey is set, the mute-link key, which it
// only reads once it exists.
func serverFiles(dataDir, dbPath string, readsKey bool) []dataFile {
	files := append([]dataFile{lockFile(dataDir)}, databaseFiles(dbPath)...)
	if !readsKey {
		return files
	}
	return append(files, dataFile{path: filepath.Join(dataDir, mutelink.KeyFile)})
}

// readsMuteLinkKey says whether the server will read the mute-link key file
// in the data directory. It signs mute links only with --base-url, and with
// a secret key it derives the signing key from that instead of the file
// (mutelink.Load). A key file left over from another configuration is not
// opened, so it is not checked either.
func readsMuteLinkKey(cfg config.Config) bool {
	return cfg.BaseURL != "" && strings.TrimSpace(cfg.SecretKey) == ""
}
