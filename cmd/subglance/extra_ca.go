package main

import (
	"crypto/x509"
	"log/slog"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/fileaccess"
)

// extraRoots is the trust store checks verify against: nil, the host's own,
// unless --extra-ca-file names a file of certificate authorities to trust
// beside it. A file that cannot be read or holds no certificate refuses the
// start, because the alternative is every check behind that CA failing for a
// reason nobody connects with a typo in a path.
func extraRoots(cfg config.Config, log *slog.Logger) (*x509.CertPool, error) {
	if cfg.ExtraCAFile == "" {
		return nil, nil
	}
	if err := fileaccess.RequireReadable("--extra-ca-file (SUBGLANCE_EXTRA_CA_FILE)", cfg.ExtraCAFile); err != nil {
		return nil, err
	}
	pool, n, err := checker.ExtraRoots(cfg.ExtraCAFile)
	if err != nil {
		return nil, err
	}
	log.Info("checks trust extra certificate authorities", "file", cfg.ExtraCAFile, "certificates", n)
	return pool, nil
}
