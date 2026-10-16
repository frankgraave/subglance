package config

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

// A command line the flag parser refused comes back as a *FlagError, which
// is what lets a subcommand refuse it in its own words instead of printing
// the server's usage. Help and a stray word are not parse failures and keep
// their own errors.
func TestLoadReturnsAFlagErrorForAnUnparsedCommandLine(t *testing.T) {
	for _, args := range [][]string{
		{"--no-such-flag"},
		{"--addr"},
		{"--check-workers", "many"},
		{"--connectivity-check=maybe"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := LoadWithoutUsage(args)
			var fe *FlagError
			if !errors.As(err, &fe) {
				t.Fatalf("err = %v (%T), want a *FlagError", err, err)
			}
			if fe.Error() != fe.Err.Error() || fe.Error() == "" {
				t.Errorf("FlagError text %q, want the parser's own %q", fe.Error(), fe.Err.Error())
			}
		})
	}

	_, err := LoadWithoutUsage([]string{"--help"})
	var fe *FlagError
	if !errors.Is(err, flag.ErrHelp) || errors.As(err, &fe) {
		t.Errorf("--help: err = %v, want flag.ErrHelp and no *FlagError", err)
	}
	_, err = LoadWithoutUsage([]string{"--data-dir", "/srv", "serve"})
	if err == nil || errors.As(err, &fe) || !strings.Contains(err.Error(), `unexpected argument "serve"`) {
		t.Errorf("stray word: err = %v, want the stray-argument refusal and no *FlagError", err)
	}
}
