// Package buildinfo exposes version metadata stamped in at link time.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These are overridden at build time via -ldflags -X.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Short returns a one-line version string, e.g. "0.1.0 (a1b2c3d, go1.24.0)".
func Short() string {
	commit := Commit
	if commit == "" {
		commit = vcsRevision()
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if commit == "" {
		return fmt.Sprintf("%s (%s)", Version, runtime.Version())
	}
	return fmt.Sprintf("%s (%s, %s)", Version, commit, runtime.Version())
}

// vcsRevision recovers the git commit from the build info embedded by the Go
// toolchain, so that `go build` without ldflags still reports something useful.
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
