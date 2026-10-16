package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubcommandHelpIsTheSubcommands asks every subcommand that loads the
// server's configuration for its usage. Each must answer with its own usage
// line and its own flags, not with the server's list of settings, which is
// what `restore --help`, `healthcheck --help` and `backup <path> --help`
// printed before: the one place to learn about `restore --from` did not
// mention it.
func TestSubcommandHelpIsTheSubcommands(t *testing.T) {
	cases := []struct {
		name string
		run  func([]string, *bytes.Buffer) error
		args []string
		want []string
	}{
		{
			name: "restore",
			run:  func(a []string, b *bytes.Buffer) error { return runRestore(a, b) },
			args: []string{"--help"},
			want: []string{restoreUsage, "-from", "-force"},
		},
		{
			name: "restore -h after server flags",
			run:  func(a []string, b *bytes.Buffer) error { return runRestore(a, b) },
			args: []string{"--data-dir", "/srv/subglance", "-h"},
			want: []string{restoreUsage, "-from", "-force"},
		},
		{
			name: "healthcheck",
			run:  func(a []string, b *bytes.Buffer) error { return runHealthcheck(a, b) },
			args: []string{"--help"},
			want: []string{healthcheckUsage, "exits 0"},
		},
		{
			name: "backup without a path",
			run:  func(a []string, b *bytes.Buffer) error { return runBackup(a, b) },
			args: []string{"--help"},
			want: []string{backupUsage},
		},
		{
			name: "backup with a path",
			run:  func(a []string, b *bytes.Buffer) error { return runBackup(a, b) },
			args: []string{"/tmp/x.db", "--help"},
			want: []string{backupUsage},
		},
		{
			name: "reset-password",
			run: func(a []string, b *bytes.Buffer) error {
				return resetPassword(a, b, nil)
			},
			args: []string{"--help"},
			want: []string{accountResetUsage, "-email", "-generate", "-force"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := tc.run(tc.args, &out)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("err = %v, want flag.ErrHelp so the exit is clean", err)
			}
			got := out.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("usage does not contain %q:\n%s", w, got)
				}
			}
			if !strings.Contains(got, serverSettingsNote) {
				t.Errorf("usage does not point at subglance --help for the server's settings:\n%s", got)
			}
			// Flags only the server defines: their presence means the server's
			// usage was printed instead of, or as well as, the subcommand's.
			for _, server := range []string{"Usage of subglance", "-alert-group-window", "-backup-target", "-log-format"} {
				if strings.Contains(got, server) {
					t.Errorf("usage contains the server's %q:\n%s", server, got)
				}
			}
		})
	}
}

// TestRestoreHelpTouchesNothing asks restore for its usage with a data
// directory that does not exist yet. Help is answered before the
// configuration is read, so it must not create the directory, take its lock
// or refuse for want of a backup target.
func TestRestoreHelpTouchesNothing(t *testing.T) {
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "")
	dir := filepath.Join(t.TempDir(), "not-yet")
	var out bytes.Buffer
	if err := runRestore([]string{"--data-dir", dir, "--help"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore --help created %s (stat err %v)", dir, err)
	}
}

func TestAsksForHelp(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"-h"}, true},
		{[]string{"--h"}, true},
		{[]string{"-help"}, true},
		{[]string{"--help"}, true},
		{[]string{"--help=true"}, true},
		{[]string{"/tmp/x.db", "--data-dir", "/data", "--help"}, true},
		{[]string{"--data-dir", "/data"}, false},
		{[]string{"--helpful"}, false},
		{[]string{"help"}, false},
		{[]string{"--", "--help"}, false},
	}
	for _, tc := range cases {
		if got := asksForHelp(tc.args); got != tc.want {
			t.Errorf("asksForHelp(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
