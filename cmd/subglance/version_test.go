package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/buildinfo"
)

func TestRunVersionPrintsTheStampedBuild(t *testing.T) {
	var out bytes.Buffer
	if err := runVersion(nil, &out); err != nil {
		t.Fatalf("runVersion: %v", err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "subglance ") {
		t.Errorf("output does not name the program: %q", got)
	}
	if !strings.Contains(got, buildinfo.Short()) {
		t.Errorf("output %q does not contain the string /api/v1/health reports (%q)",
			got, buildinfo.Short())
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output is not newline-terminated: %q", got)
	}
}

// A build without ldflags must say what it honestly knows. "dev" is the
// stamped-in default and the right answer; a version invented from the module
// path or the tag of the checkout would read as a release in a bug report.
func TestRunVersionSaysDevWhenNothingWasStamped(t *testing.T) {
	var out bytes.Buffer
	if err := runVersion(nil, &out); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	if !strings.Contains(out.String(), "dev") {
		t.Errorf("an unstamped test build reports %q, want it to say dev", out.String())
	}
	// And it must not claim a build date it was never given.
	if strings.Contains(out.String(), "built ") {
		t.Errorf("an unstamped build claims a build date: %q", out.String())
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
