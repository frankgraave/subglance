package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/frankgraave/subglance/internal/kumaimport"
)

// importTimeout bounds reading the source database. Kuma's configuration is
// a few hundred rows at most; the bound is for a file on a network mount
// that stops answering.
const importTimeout = 2 * time.Minute

const importUsage = "usage: subglance import uptime-kuma <kuma.db or Kuma's data directory> [-o FILE]"

// runImport converts another monitor's configuration into a SubGlance
// configuration file.
//
// It writes a file and touches nothing else: no data directory, no database
// of its own, no running server. The file is then imported the ordinary way,
// with the dry run and report every import gets, so there is one import path
// and the user reads what will be created before it is. It is a subcommand
// rather than a separate tool because the shipped image has nothing else to
// run, and Kuma's data usually sits in a Docker volume.
func runImport(args []string, out io.Writer) error {
	return importTo(args, out, os.Stderr)
}

func importTo(args []string, out, report io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(report)
	dest := fs.String("o", "", "write the configuration to `FILE` instead of standard output")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(report, importUsage)
		fs.PrintDefaults()
	}

	// Positional arguments and -o in any order: Go's flag package stops at
	// the first non-flag, and `import uptime-kuma kuma.db -o x.yaml` is
	// the natural way to type it.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	if len(positional) == 0 {
		return errors.New("import needs a source\n\n" + importUsage)
	}
	if positional[0] != "uptime-kuma" {
		return fmt.Errorf("cannot import from %q; this build reads uptime-kuma\n\n%s", positional[0], importUsage)
	}
	if len(positional) != 2 {
		return errors.New("import uptime-kuma needs exactly one path\n\n" + importUsage)
	}
	src := positional[1]
	if *dest != "" && samePath(*dest, src) {
		return errors.New("-o names the Kuma database itself; write the configuration somewhere else")
	}

	ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
	defer cancel()
	res, err := kumaimport.Convert(ctx, src)
	if err != nil {
		return err
	}
	file, err := kumaimport.Render(res)
	if err != nil {
		return err
	}

	if *dest == "" {
		if _, err := out.Write(file); err != nil {
			return err
		}
	} else if err := os.WriteFile(*dest, file, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *dest, err)
	}

	_, err = fmt.Fprintf(report, "Converted %d of %d monitors and %d of %d channels from Uptime Kuma %s.\n",
		len(res.Document.Monitors), res.Monitors, len(res.Document.Channels), res.Channels, res.Schema)
	if err != nil {
		return err
	}
	if len(res.Skipped)+len(res.Changed) > 0 {
		_, err = fmt.Fprintf(report, "%d not imported and %d imported with a change to check: see the end of the file.\n",
			len(res.Skipped), len(res.Changed))
	}
	return err
}

// samePath reports whether two paths name the same file, or a directory
// and the kuma.db inside it.
func samePath(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	if ib.IsDir() {
		if ib, errB = os.Stat(filepath.Join(b, "kuma.db")); errB != nil {
			return false
		}
	}
	return os.SameFile(ia, ib)
}
