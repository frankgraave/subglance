package config

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

// usageOf runs load with args and returns the usage text it printed.
func usageOf(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := load(args, &out); err == nil {
		t.Fatalf("load(%v) succeeded, want the usage error", args)
	}
	return out.String()
}

// The usage text that --help and every mistyped flag print lists each flag's
// default, and a flag's default is what the environment set. It goes to
// stderr, which in a container is a log that is kept.
func TestUsageDoesNotPrintCredentialsFromTheEnvironment(t *testing.T) {
	const (
		key         = "5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2"
		previousKey = "cHJldmlvdXMga2V5IG1hdGVyaWFsIGZvciByb3RhdGlvbiE="
		watchdogURL = "https://hc-ping.com/0d1b2f3e-uuid-is-the-credential?create=1"
	)
	t.Setenv("SUBGLANCE_SECRET_KEY", key)
	t.Setenv("SUBGLANCE_SECRET_KEY_PREVIOUS", previousKey)
	t.Setenv("SUBGLANCE_WATCHDOG_URL", watchdogURL)
	t.Setenv("SUBGLANCE_ADDR", ":9123")

	for _, args := range [][]string{{"--help"}, {"-h"}, {"--no-such-flag"}, {"--addr"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			usage := usageOf(t, args...)
			if !strings.Contains(usage, "-secret-key") {
				t.Fatalf("usage does not list the flags:\n%s", usage)
			}
			for name, secret := range map[string]string{
				"SUBGLANCE_SECRET_KEY":          key,
				"SUBGLANCE_SECRET_KEY_PREVIOUS": previousKey,
				"the watchdog URL's path":       "0d1b2f3e-uuid-is-the-credential",
				"the watchdog URL's query":      "create=1",
			} {
				if strings.Contains(usage, secret) {
					t.Errorf("usage prints %s:\n%s", name, usage)
				}
			}
			if !strings.Contains(usage, `(default "https://hc-ping.com/…")`) {
				t.Errorf("usage does not show the watchdog URL as scheme and host:\n%s", usage)
			}
			if got := strings.Count(usage, "(set in the environment; not shown)"); got != 2 {
				t.Errorf("usage says %d keys are set and not shown, want 2:\n%s", got, usage)
			}
			// Every other default is still the effective one, which is
			// what makes --help worth reading in a configured container.
			if !strings.Contains(usage, `(default ":9123")`) {
				t.Errorf("usage no longer shows the address the environment set:\n%s", usage)
			}
		})
	}
}

func TestUsageDoesNotClaimAnUnsetKeyIsSet(t *testing.T) {
	t.Setenv("SUBGLANCE_SECRET_KEY", "")
	t.Setenv("SUBGLANCE_SECRET_KEY_PREVIOUS", "")
	t.Setenv("SUBGLANCE_WATCHDOG_URL", "")

	usage := usageOf(t, "--help")
	if strings.Contains(usage, "not shown") {
		t.Errorf("usage describes an unset key as set:\n%s", usage)
	}
	_, watchdogLine, _ := strings.Cut(usage, "-watchdog-url string\n")
	watchdogLine, _, _ = strings.Cut(watchdogLine, "\n")
	if strings.Contains(watchdogLine, "(default") {
		t.Errorf("usage gives an unset watchdog URL a default: %q", watchdogLine)
	}
}

// The values behind the usage text are the configured ones: only what the
// usage prints changes.
func TestHidingCredentialDefaultsKeepsTheValues(t *testing.T) {
	const key = "5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2e7c0ffee5ec2"
	t.Setenv("SUBGLANCE_SECRET_KEY", key)
	t.Setenv("SUBGLANCE_WATCHDOG_URL", "https://hc-ping.com/uuid")

	c, err := load(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.SecretKey != key {
		t.Errorf("SecretKey = %q, want the environment's", c.SecretKey)
	}
	if c.WatchdogURL != "https://hc-ping.com/uuid" {
		t.Errorf("WatchdogURL = %q, want the environment's", c.WatchdogURL)
	}

	var out bytes.Buffer
	if _, err := load([]string{"--help"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("load(--help) error = %v, want flag.ErrHelp", err)
	}
}

// A backup endpoint written with a key pair is refused at startup, but the
// usage text is printed before startup gets that far.
func TestUsageDoesNotPrintABackupEndpointsUserPart(t *testing.T) {
	t.Setenv("SUBGLANCE_BACKUP_ENDPOINT", "https://AKIAEXAMPLEKEY:s3cr3tpa55@minio.local:9000")

	for _, args := range [][]string{{"--help"}, {"--no-such-flag"}} {
		usage := usageOf(t, args...)
		for _, part := range []string{"AKIAEXAMPLEKEY", "s3cr3tpa55"} {
			if strings.Contains(usage, part) {
				t.Errorf("%v: usage prints the backup endpoint's %q:\n%s", args, part, usage)
			}
		}
		if !strings.Contains(usage, `(default "https://minio.local:9000")`) {
			t.Errorf("%v: usage does not show the backup endpoint as scheme and host:\n%s", args, usage)
		}
	}
}

// An endpoint is a scheme, a host and a port, which the shown form keeps, so
// a correct one reads in --help exactly as it was set.
func TestUsageShowsAPlainBackupEndpointAsSet(t *testing.T) {
	t.Setenv("SUBGLANCE_BACKUP_ENDPOINT", "https://s3.eu-central-003.backblazeb2.com")
	if usage := usageOf(t, "--help"); !strings.Contains(usage, `(default "https://s3.eu-central-003.backblazeb2.com")`) {
		t.Errorf("usage does not show the backup endpoint as set:\n%s", usage)
	}
}

// The base URL and the backup target are refused at startup with a user name
// and password in them, but the usage text is printed before startup gets
// that far. A secret access key often holds a "/", which ends the URL's
// authority early, so the target below does not even parse as one.
func TestUsageDoesNotPrintAUserPartOfTheBaseURLOrBackupTarget(t *testing.T) {
	t.Setenv("SUBGLANCE_BASE_URL", "https://admin:hunter2pass@status.example.com/sub")
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://AKIAEXAMPLEKEY:wJalr/XUtnFEMI+K7MDENG@backups/subglance")

	for _, args := range [][]string{{"--help"}, {"--no-such-flag"}} {
		usage := usageOf(t, args...)
		for _, part := range []string{"admin", "hunter2pass", "AKIAEXAMPLEKEY", "wJalr", "XUtnFEMI"} {
			if strings.Contains(usage, part) {
				t.Errorf("%v: usage prints %q:\n%s", args, part, usage)
			}
		}
		for _, want := range []string{
			`(default "https://***@status.example.com/sub")`,
			`(default "s3://***@backups/subglance")`,
		} {
			if !strings.Contains(usage, want) {
				t.Errorf("%v: usage does not show %s:\n%s", args, want, usage)
			}
		}
	}
}

// Without a user part the two are printed as set: the path is the prefix
// links and backups go under, which is what --help is read for.
func TestUsageShowsAPlainBaseURLAndBackupTargetAsSet(t *testing.T) {
	t.Setenv("SUBGLANCE_BASE_URL", "https://status.example.com/sub")
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://backups/subglance/prod")

	usage := usageOf(t, "--help")
	for _, want := range []string{
		`(default "https://status.example.com/sub")`,
		`(default "s3://backups/subglance/prod")`,
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage does not show %s:\n%s", want, usage)
		}
	}
}

func TestMaskedUserPart(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"", ""},
		{"https://status.example.com/sub", "https://status.example.com/sub"},
		{"https://user:pass@status.example.com", "https://***@status.example.com"},
		// A password holding "/", "#" or "@" runs to the last "@".
		{"s3://KEYID:abc/def@bucket/prefix", "s3://***@bucket/prefix"},
		{"https://user:pa#ss@host/path", "https://***@host/path"},
		{"https://user:p@ss@host", "https://***@host"},
		// Without a scheme there is nothing to keep before the user part.
		{"user:pass@host", "***@host"},
	} {
		if got := maskedUserPart(tc.raw); got != tc.want {
			t.Errorf("maskedUserPart(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
