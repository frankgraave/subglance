package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/backup"
	"github.com/frankgraave/subglance/internal/config"
)

// restoreTimeout bounds download plus integrity check. Generous, because the
// download is the whole database over whatever uplink the host has.
const restoreTimeout = 30 * time.Minute

// runRestore downloads a backup from the configured S3 target and puts it in
// place of the database.
//
// It reads the same configuration as the server — the same target, endpoint,
// region and credentials from the same environment — so in a container it is
// `docker compose run --rm subglance restore` with nothing else to type.
//
// It refuses while something answers on the configured address. Replacing the
// database under a running server is the one way to lose data here: the
// server keeps writing to the file it has open, and the restored copy is
// silently overwritten or ignored. --force exists for the case where the
// thing answering is not SubGlance.
func runRestore(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("subglance restore", flag.ContinueOnError)
	from := fs.String("from", "", "object name of the backup to restore (default: the newest)")
	force := fs.Bool("force", false, "restore even though something answers on --addr")
	// Only the restore's own flags are parsed here; everything else is
	// server configuration and goes to config.Load unchanged.
	own, rest := splitRestoreFlags(args)
	if err := fs.Parse(own); err != nil {
		return err
	}

	cfg, err := config.Load(rest)
	if err != nil {
		return err
	}
	if cfg.BackupTarget == "" {
		return errors.New("no backup target is configured; set SUBGLANCE_BACKUP_TARGET and the backup credentials " +
			"the same way the server has them")
	}
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
// address, dialled the way the healthcheck dials it.
func listening(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
