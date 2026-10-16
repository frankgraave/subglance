package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMain runs main() in a child copy of this test binary (see
// TestVersionSubprocessMain) with args and a data directory that does not
// exist, and returns its exit code, standard output and standard error.
// Running the real entry point is the point: the server's usage list was
// written to the process's stderr by the configuration loader, where an
// in-process test with a buffer for output never sees it.
func runMain(t *testing.T, dataDir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestVersionSubprocessMain$", "-test.v=false")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SUBGLANCE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		versionSubprocessEnv+"=1",
		"SUBGLANCE_TEST_ARGS="+strings.Join(args, " "),
		"SUBGLANCE_DATA_DIR="+dataDir,
	)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("run %v: %v", args, err)
	}
	return code, out.String(), errOut.String()
}

// A subcommand that cannot parse its command line says so in one line that
// names the flag and where its own flags are listed. Before, a flag the
// server's configuration could not read (a typo, an unknown flag, a value
// left off) printed the error, the server's list of some fifty flags, which
// does not hold the subcommand's own, and the error again: `restore --form x`
// answered a typo of --from with every flag except --from. A missing value
// for the subcommand's own flag printed the subcommand's usage on stdout.
func TestSubcommandRefusesAnUnparsedFlagInOneLine(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "backup.db")
	cases := []struct {
		sub  string
		args []string
		flag string
	}{
		{"restore", []string{"restore", "--form", "latest"}, "-form"},
		{"restore", []string{"restore", "--from"}, "-from"},
		{"healthcheck", []string{"healthcheck", "--nope"}, "-nope"},
		{"healthcheck", []string{"healthcheck", "--addr"}, "-addr"},
		{"healthcheck", []string{"healthcheck", "--check-workers", "many"}, "-check-workers"},
		{"backup", []string{"backup", dest, "--nope"}, "-nope"},
		{"backup", []string{"backup", "--nope"}, "-nope"},
		{"reset-password", []string{"reset-password", "--email", "owner@example.com", "--nope"}, "-nope"},
		{"reset-password", []string{"reset-password", "--nope"}, "-nope"},
		{"reset-password", []string{"reset-password", "--email"}, "-email"},
		{"import", []string{"import", "--nope"}, "-nope"},
		{"import", []string{"import", "uptime-kuma", "kuma.db", "-o"}, "-o"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "never-created")
			code, stdout, stderr := runMain(t, dataDir, tc.args...)
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if stdout != "" {
				t.Errorf("wrote to standard output:\n%s", stdout)
			}
			line := strings.TrimSuffix(stderr, "\n")
			if strings.Contains(line, "\n") {
				t.Errorf("stderr is %d lines, want one:\n%s", strings.Count(stderr, "\n"), stderr)
			}
			for _, want := range []string{
				"subglance " + tc.sub + ": ",
				tc.flag,
				"subglance " + tc.sub + " --help lists the flags",
			} {
				if !strings.Contains(line, want) {
					t.Errorf("stderr does not contain %q:\n%s", want, stderr)
				}
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the data directory was touched before the refusal (stat err %v)", err)
			}
		})
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup wrote %s before refusing (stat err %v)", dest, err)
	}
}

// The server's own usage is the right list for the server, so a flag it
// cannot parse still prints it; only the subcommands stopped borrowing it.
func TestServerAnswersAnUnparsedFlagWithItsUsage(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "never-created")
	code, _, stderr := runMain(t, dataDir, "--nope")
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	for _, want := range []string{"flag provided but not defined: -nope", "Usage of subglance", "-alert-group-window"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

// import's --help is answered by main() with its usage and a clean exit,
// now that its flag set no longer prints usage on its own.
func TestImportHelpIsItsUsage(t *testing.T) {
	var out, report bytes.Buffer
	err := importTo([]string{"uptime-kuma", "kuma.db", "--help"}, &out, &report)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if got := report.String(); !strings.Contains(got, importUsage) || !strings.Contains(got, "-o FILE") {
		t.Errorf("usage = %q, want the import usage line and -o", got)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q to the configuration output", out.String())
	}
}
