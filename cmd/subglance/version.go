package main

import (
	"fmt"
	"io"

	"github.com/frankgraave/subglance/internal/buildinfo"
)

// versionArgs are the spellings that ask what this binary is.
//
// Both dash forms, because the flag package accepts both for every other
// option and a tool that answers `--data-dir` but not `--version` would be
// inconsistent about the one flag people type before they have read anything.
// The bare word as well, because `backup` and `healthcheck` already established
// that this binary takes subcommands, and someone who has typed those will try
// `subglance version`.
var versionArgs = map[string]bool{
	"--version": true,
	"-version":  true,
	"version":   true,
}

// wantsVersion reports whether the command line is asking what this binary is.
//
// Only the first argument counts, which is the same rule the other subcommands
// follow. Scanning the whole list would be friendlier to
// `subglance --data-dir /data --version`, but it cannot be done honestly: in
// `--data-dir --version` the second token is a directory that happens to be
// spelled like a flag, and nothing outside the flag package knows which
// options take a value. Answering the version there would mean guessing, and
// guessing wrong means silently not starting the server someone asked for.
func wantsVersion(args []string) bool {
	return len(args) > 0 && versionArgs[args[0]]
}

// runVersion prints what this binary is and returns.
//
// It is answered before configuration is loaded and before the data directory
// is touched, because the question it answers comes first in time: someone
// with a downloaded file wants to know what they have, and requiring them to
// choose a port and a writable directory to find out is the wrong order. The
// bug report template asks for this string, so producing it must not depend on
// the instance being runnable.
func runVersion(_ []string, out io.Writer) error {
	_, err := fmt.Fprintln(out, buildinfo.Full())
	return err
}
