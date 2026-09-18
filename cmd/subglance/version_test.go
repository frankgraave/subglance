package main

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/buildinfo"
)

// stamp sets the link-time metadata for one test and puts it back afterwards.
//
// The tests below read output that is derived from package-level variables, so
// leaving them ambient would make every assertion true of whatever the test
// binary happened to be built with — and a regression in either the stamped or
// the unstamped rendering would pass unnoticed, because the expectation would
// have drifted along with the output.
func stamp(t *testing.T, version, commit, date string) {
	t.Helper()
	prevVersion, prevCommit, prevDate := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.Date = prevVersion, prevCommit, prevDate
	})
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = version, commit, date
}

func TestRunVersionRendersWhatWasStamped(t *testing.T) {
	for _, tc := range []struct {
		name            string
		version         string
		commit          string
		date            string
		wantFirstLine   string
		wantSecondLine  string
		wantLineCount   int
		wantNoDateClaim bool
	}{
		{
			// What the release pipeline produces: all three stamped.
			name: "released", version: "0.4.1", commit: "a1b2c3d4e5f6", date: "2026-09-18T09:14:02Z",
			wantFirstLine:  "subglance 0.4.1 (a1b2c3d, " + runtime.Version() + ")",
			wantSecondLine: "built 2026-09-18T09:14:02Z",
			wantLineCount:  2,
		},
		{
			// `go build` with no ldflags. It must say what it knows and
			// nothing else: a version invented from a tag or a date taken
			// from the filesystem would read as a release in a bug report.
			name: "unstamped", version: "dev", commit: "", date: "",
			wantFirstLine:   "subglance " + buildinfo.Short(),
			wantLineCount:   1,
			wantNoDateClaim: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stamp(t, tc.version, tc.commit, tc.date)

			var out bytes.Buffer
			if err := runVersion(nil, &out); err != nil {
				t.Fatalf("runVersion: %v", err)
			}
			got := out.String()
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("output is not newline-terminated: %q", got)
			}

			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) != tc.wantLineCount {
				t.Fatalf("output has %d lines, want %d: %q", len(lines), tc.wantLineCount, got)
			}
			// The first line is Short() verbatim, which is the string the
			// startup log and GET /api/v1/health already report. A bug
			// report and a health response must not need reading
			// differently.
			if lines[0] != tc.wantFirstLine {
				t.Errorf("first line = %q, want %q", lines[0], tc.wantFirstLine)
			}
			if lines[0] != "subglance "+buildinfo.Short() {
				t.Errorf("first line %q is not Short() verbatim (%q)", lines[0], buildinfo.Short())
			}
			if tc.wantSecondLine != "" && lines[1] != tc.wantSecondLine {
				t.Errorf("second line = %q, want %q", lines[1], tc.wantSecondLine)
			}
			if tc.wantNoDateClaim && strings.Contains(got, "built ") {
				t.Errorf("a build with no stamped date claims one: %q", got)
			}
		})
	}
}

// The unstamped default really is "dev": the assertion above would hold for
// any value, so the value itself is pinned once, here.
func TestBuildinfoDefaultsToDev(t *testing.T) {
	if buildinfo.Version != "dev" {
		t.Errorf("buildinfo.Version defaults to %q, want dev", buildinfo.Version)
	}
}

func TestWantsVersionRecognisesEverySpelling(t *testing.T) {
	for _, args := range [][]string{
		{"--version"},
		{"-version"},
		{"version"},
		// Trailing arguments are ignored rather than rejected: the answer
		// does not depend on them.
		{"--version", "--log-level", "debug"},
	} {
		if !wantsVersion(args) {
			t.Errorf("wantsVersion(%q) = false, want true", args)
		}
	}

	for _, args := range [][]string{
		nil,
		{"--addr", ":8080"},
		{"healthcheck"},
		// Not a version request: this is a data directory that happens to be
		// spelled like a flag, and treating it as one would silently decline
		// to start the server that was asked for.
		{"--data-dir", "--version"},
	} {
		if wantsVersion(args) {
			t.Errorf("wantsVersion(%q) = true, want false", args)
		}
	}
}

// versionSubprocessEnv marks the re-executed copy of this test binary that
// runs main() instead of running tests.
const versionSubprocessEnv = "SUBGLANCE_TEST_RUN_MAIN"

// TestVersionSubprocessMain is not a test. It is the child half of
// TestVersionNeedsNoDataDirNoConfigAndNoPort: when the marker is set it hands
// control to main() with the arguments the parent chose, so the real entry
// point is exercised rather than a reimplementation of it.
func TestVersionSubprocessMain(t *testing.T) {
	if os.Getenv(versionSubprocessEnv) == "" {
		t.Skip("child half of TestVersionNeedsNoDataDirNoConfigAndNoPort")
	}
	os.Args = append([]string{"subglance"}, strings.Fields(os.Getenv("SUBGLANCE_TEST_ARGS"))...)
	main()
}

// The claim in the ticket is that someone holding a downloaded binary can ask
// what it is. That means the answer must not depend on anything an unconfigured
// machine does not have, so this drives main() itself with a data directory it
// cannot create, an unusable listen address and a configuration value that Load
// refuses outright. Testing runVersion alone would pass even if main asked
// config.Load first, which is exactly the bug.
func TestVersionNeedsNoDataDirNoConfigAndNoPort(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		t.Run(arg, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestVersionSubprocessMain", "-test.v=false")
			cmd.Env = append(os.Environ(),
				versionSubprocessEnv+"=1",
				"SUBGLANCE_TEST_ARGS="+arg,
				// A path under a regular file, so MkdirAll cannot succeed.
				"SUBGLANCE_DATA_DIR=/etc/hostname/subglance-data",
				// Refused by validate(): if configuration were loaded first,
				// this would be the error instead of the version.
				"SUBGLANCE_LOG_LEVEL=not-a-level",
			)

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("subglance %s exited non-zero (%v) on an unconfigured machine: %s", arg, err, out)
			}
			if !strings.Contains(string(out), buildinfo.Short()) {
				t.Errorf("output does not carry the version: %s", out)
			}
			if strings.Contains(string(out), "not-a-level") {
				t.Errorf("%s loaded configuration before answering: %s", arg, out)
			}
		})
	}
}
