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
	commit := ShortCommit()
	if commit == "" {
		return fmt.Sprintf("%s (%s)", Version, runtime.Version())
	}
	return fmt.Sprintf("%s (%s, %s)", Version, commit, runtime.Version())
}

// ShortCommit returns the seven-character commit this binary was built from:
// the stamped one if there is one, otherwise the revision the Go toolchain
// embedded. Empty when neither is known, which callers must show as unknown
// rather than filling in.
func ShortCommit() string {
	commit := Commit
	if commit == "" {
		commit = vcsRevision()
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	return commit
}

// Full returns the multi-line form a person pastes into a bug report.
//
// Its first line is exactly what Short() produces, which is what the startup
// log and GET /api/v1/health already report. That is deliberate: a maintainer
// reading a bug report and a maintainer reading a health response should not
// have to learn two renderings of the same fact, and the extra lines here are
// additions rather than a different format.
//
// The build date is only printed when it was stamped in. A `go build` without
// ldflags has no date to report, and inventing one — the file's mtime, the
// current time — would put a confident wrong answer in a bug report. Saying
// nothing is the honest version of not knowing, the same way Version already
// says "dev" rather than guessing at a release number.
func Full() string {
	s := "subglance " + Short()
	if Date != "" {
		s += "\nbuilt " + Date
	}
	return s
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
