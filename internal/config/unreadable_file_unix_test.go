//go:build unix

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unreadableFile writes a file this process owns and then takes its read
// bit away: the closest a test running as one user gets to a root-owned,
// owner-only file mounted into a container that runs as another.
func unreadableFile(t *testing.T, name, contents string) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes; run as an ordinary user")
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	return path
}

func wantUnreadable(t *testing.T, err error, setting, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("an unreadable %s was accepted", setting)
	}
	msg := err.Error()
	for _, want := range []string{setting, path, "belongs to", "SubGlance runs as user", "chmod u+r " + path} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "neither 32 bytes") {
		t.Errorf("refusal is the key parser's guess, not a file refusal:\n%s", msg)
	}
}

// The documented way to give the key, a file mounted read-only, fails in
// the container as "permission denied" when the file is the host's and
// owner-only. The refusal names the file, its owner, the user SubGlance
// runs as and the fix, rather than suggesting the path might be key
// material.
func TestSecretKeyFileThatCannotBeReadIsNamed(t *testing.T) {
	for _, tc := range []struct {
		flag, setting string
		extra         []string
	}{
		{"--secret-key", "--secret-key (SUBGLANCE_SECRET_KEY)", nil},
		{"--secret-key-previous", "--secret-key-previous (SUBGLANCE_SECRET_KEY_PREVIOUS)", []string{"--secret-key", testHexKey}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			clearSecretKeyEnv(t)
			path := unreadableFile(t, "subglance.key", testHexKey+"\n")
			_, err := Load(append([]string{tc.flag, path}, tc.extra...))
			wantUnreadable(t, err, tc.setting, path)
		})
	}
}

func TestBackupSecretFileThatCannotBeReadIsNamed(t *testing.T) {
	path := unreadableFile(t, "s3.secret", "from-the-file\n")
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://bucket/nightly")
	t.Setenv("SUBGLANCE_BACKUP_ACCESS_KEY_ID", "key-id")
	t.Setenv("SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE", path)
	_, err := Load(nil)
	wantUnreadable(t, err, "SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE", path)
}
