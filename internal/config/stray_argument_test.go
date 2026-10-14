package config

import (
	"strings"
	"testing"
)

// A word that is not a flag stops Go's flag parsing. Before the refusal every
// flag after it was dropped in silence and the process ran on defaults, so
// each case here names a flag after the stray word that would otherwise have
// been ignored.
func TestLoadRefusesAStrayArgument(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "a word before the flags",
			args: []string{"serve", "--addr", "127.0.0.1:9000"},
			want: []string{`unexpected argument "serve"`, "--help"},
		},
		{
			name: "a word between flags",
			args: []string{"--data-dir", "/srv/subglance", "latest", "--addr", "127.0.0.1:9000"},
			want: []string{`unexpected argument "latest"`},
		},
		{
			name: "a word after the last flag",
			args: []string{"--addr", "127.0.0.1:9000", "extra"},
			want: []string{`unexpected argument "extra"`},
		},
		{
			// A boolean flag takes no separate value, so `false` is the stray
			// word and the check stays on: the opposite of what was typed.
			name: "a boolean flag given its value after a space",
			args: []string{"--connectivity-check", "false", "--addr", "127.0.0.1:9000"},
			want: []string{`unexpected argument "false" after --connectivity-check`, "--connectivity-check=false"},
		},
		{
			// --data-dir takes --addr as its value, so the boolean flag after
			// them is read as a flag and the hint still applies.
			name: "a boolean flag after a flag that took a flag-like value",
			args: []string{"--data-dir", "--addr", "--connectivity-check", "false"},
			want: []string{`unexpected argument "false" after --connectivity-check`, "--connectivity-check=false"},
		},
		{
			name: "a single-dash boolean flag given its value after a space",
			args: []string{"-allow-private-targets", "true"},
			want: []string{"--allow-private-targets=true"},
		},
		{
			// The invalid level after the word was never read, so it was
			// never refused either.
			name: "an invalid value after the word",
			args: []string{"bogus", "--log-level", "loud"},
			want: []string{`unexpected argument "bogus"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args)
			if err == nil {
				t.Fatalf("Load(%q) succeeded, want it to refuse the stray argument", tc.args)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Load(%q) error = %q, want it to contain %q", tc.args, err, w)
				}
			}
		})
	}
}

// The boolean hint is only for a true/false word after a boolean flag. After
// a flag that takes a value, or for a word that is not a boolean, it would
// send the reader to the wrong fix.
func TestStrayArgumentHintOnlyAfterABooleanFlag(t *testing.T) {
	cases := [][]string{
		{"--connectivity-check", "maybe"},
		{"--connectivity-check=false", "true"},
		{"--addr", "127.0.0.1:9000", "false"},
		// --data-dir reads the next word as its value, so the boolean flag
		// here is a directory name, and --connectivity-check=false would
		// send the reader to a command that still sets the wrong directory.
		{"--data-dir", "--connectivity-check", "false"},
	}
	for _, args := range cases {
		_, err := Load(args)
		if err == nil {
			t.Fatalf("Load(%q) succeeded, want a refusal", args)
		}
		if strings.Contains(err.Error(), "equals sign") {
			t.Errorf("Load(%q) error = %q, want no boolean hint", args, err)
		}
	}
}

// Every way of writing a flag the package accepts still loads.
func TestLoadAcceptsEveryFlagSpelling(t *testing.T) {
	c, err := Load([]string{
		"--addr", "127.0.0.1:9000",
		"-data-dir=/srv/subglance",
		"--connectivity-check=false",
		"-allow-private-targets",
		"--log-level=debug",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != "127.0.0.1:9000" || c.DataDir != "/srv/subglance" || c.ConnectivityCheck ||
		!c.AllowPrivateTargets || c.LogLevel != "debug" {
		t.Errorf("Load read %+v, want every flag applied", c)
	}
}
