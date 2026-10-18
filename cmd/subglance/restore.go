package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/backup"
	"github.com/frankgraave/subglance/internal/datalock"
)

// restoreTimeout bounds download plus integrity check. Generous, because the
// download is the whole database over whatever uplink the host has.
const restoreTimeout = 30 * time.Minute

const (
	restoreUsage = "usage: subglance restore [--from NAME] [--force] [--data-dir DIR]"
	restoreAbout = "Downloads a backup from the configured backup target and puts it in place of the database. Stop the server first."
)

// runRestore downloads a backup from the configured S3 target and puts it in
// place of the database.
//
// It reads the same configuration as the server — the same target, endpoint,
// region and credentials from the same environment — so in a container it is
// `docker compose run --rm subglance restore` with nothing else to type.
//
// It refuses while a server holds the data directory. Replacing the database
// under a running server is the one way to lose data here: the server keeps
// writing to the file it has open, and the restored copy is silently
// overwritten or ignored. The server holds an exclusive lock on the data
// directory for as long as it runs; the restore takes the same lock and keeps
// it until the new database is in place, so a server cannot start halfway
// through either. --force does not bypass the lock: the lock is the check
// that works where the address check cannot, such as `docker compose run`,
// which gets its own network namespace.
//
// It also refuses while something answers on the configured address, as a
// second guard. --force skips only that one, for the case where the thing
// answering is not SubGlance.
func runRestore(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("subglance restore", flag.ContinueOnError)
	from := fs.String("from", "", "object name of the backup to restore (default: the newest)")
	force := fs.Bool("force", false, "restore even though something answers on --addr (never while a server holds the data directory)")
	// Silent: a flag that does not parse is refused in one line (see
	// flagRefusal), and usage is printed only when asked for.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if asksForHelp(args) {
		printUsage(out, restoreUsage, restoreAbout, fs)
		return flag.ErrHelp
	}
	// Only the restore's own flags are parsed here; everything else is
	// server configuration and goes to config unchanged.
	own, rest := splitRestoreFlags(args)
	if err := fs.Parse(own); err != nil {
		return flagRefusal("restore", err)
	}

	cfg, err := loadConfig("restore", rest)
	if err != nil {
		return err
	}
	if cfg.BackupTarget == "" {
		return errors.New("no backup target is configured; set SUBGLANCE_BACKUP_TARGET and the backup credentials " +
			"the same way the server has them")
	}
	// The database is renamed aside rather than written, so the directory
	// and the lock are what the restore itself needs (see requireDataAccess).
	if err := requireDataAccess(cfg.DataDir, []dataFile{lockFile(cfg.DataDir)}); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}
	lock, err := datalock.Acquire(cfg.DataDir)
	if errors.Is(err, datalock.ErrLocked) {
		return fmt.Errorf("a SubGlance server is using %s (it holds %s); stop it first, then run the restore again",
			cfg.DataDir, datalock.Path(cfg.DataDir))
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if !*force && listening(cfg.Addr) {
		return fmt.Errorf("something is answering on %s, which is probably SubGlance itself; stop it first, "+
			"or pass --force if that is not SubGlance", displayAddr(cfg.Addr))
	}
	target, err := cfg.BackupTargetParsed()
	if err != nil {
		return err
	}
	secret, err := cfg.BackupSecret()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), restoreTimeout)
	defer cancel()

	res, err := backup.Restore(ctx, backup.RestoreOptions{
		Target:          target,
		AccessKeyID:     cfg.BackupAccessKeyID,
		SecretAccessKey: secret,
		Name:            *from,
		DBPath:          cfg.DBPath(),
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "restored %s%s to %s\n", target.String(), res.Object, cfg.DBPath())
	for _, p := range res.SetAside {
		_, _ = fmt.Fprintf(out, "kept the previous file as %s\n", p)
	}
	return nil
}

// splitRestoreFlags separates --from and --force from the server flags.
func splitRestoreFlags(args []string) (own, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		name, _, hasValue := strings.Cut(name, "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case name == "force":
			own = append(own, a)
		case name == "from":
			own = append(own, a)
			if !hasValue && i+1 < len(args) {
				i++
				own = append(own, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return own, rest
}

// listening reports whether anything accepts a connection on the listen
// address, dialled the way the healthcheck dials it: each loopback address a
// wildcard listener could be answering on, until one connects.
func listening(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, h := range loopbackHosts(host) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(h, port))
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}
