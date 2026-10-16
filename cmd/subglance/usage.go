package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// serverSettingsNote ends the usage of every subcommand that loads the
// server's configuration. Those subcommands read the data directory, the
// address and the backup settings exactly as the server does, so the list of
// those settings belongs to `subglance --help`; repeating it under each
// subcommand buried the few flags that are the subcommand's own.
const serverSettingsNote = "Every other setting is read the way the server reads it; subglance --help lists them."

// asksForHelp reports whether args ask for usage: -h, -help, --h or --help,
// with or without a value, before a "--" that ends the flags. Go's flag
// package answers those names with flag.ErrHelp whenever they are not defined
// flags, which is the case for the subcommands that have no flag set of their
// own; checking first lets them print their own usage instead of the
// server's.
func asksForHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		name, _, _ = strings.Cut(name, "=")
		if name == "h" || name == "help" {
			return true
		}
	}
	return false
}

// printUsage writes a subcommand's usage: the usage line, what it does, the
// flags that are its own (fs may be nil when there are none) and the note
// that the server's settings are listed elsewhere.
func printUsage(out io.Writer, line, about string, fs *flag.FlagSet) {
	_, _ = fmt.Fprintln(out, line)
	if about != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", about)
	}
	if fs != nil {
		_, _ = fmt.Fprintln(out)
		fs.SetOutput(out)
		fs.PrintDefaults()
	}
	_, _ = fmt.Fprintf(out, "\n%s\n", serverSettingsNote)
}
