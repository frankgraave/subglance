package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupOffByDefault(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.BackupTarget != "" || c.BackupInterval != 24*time.Hour || c.BackupKeep != 14 {
		t.Errorf("defaults: target %q, interval %s, keep %d", c.BackupTarget, c.BackupInterval, c.BackupKeep)
	}
}

func TestBackupConfiguredFromEnvWithASecretFile(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "s3.secret")
	if err := os.WriteFile(secret, []byte("from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBGLANCE_BACKUP_TARGET", "s3://bucket/nightly")
	t.Setenv("SUBGLANCE_BACKUP_ENDPOINT", "https://s3.eu-central-003.backblazeb2.com")
	t.Setenv("SUBGLANCE_BACKUP_REGION", "eu-central-003")
	t.Setenv("SUBGLANCE_BACKUP_ACCESS_KEY_ID", "key-id")
	t.Setenv("SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE", secret)
	t.Setenv("SUBGLANCE_BACKUP_INTERVAL", "6h")
	t.Setenv("SUBGLANCE_BACKUP_KEEP", "28")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := c.BackupSecret(); got != "from-the-file" {
		t.Errorf("secret = %q, want the trimmed file contents", got)
	}
	target, err := c.BackupTargetParsed()
	if err != nil || target.Bucket != "bucket" || target.Prefix != "nightly/" || target.Region != "eu-central-003" {
		t.Errorf("target = %+v, %v", target, err)
	}
	if c.BackupInterval != 6*time.Hour || c.BackupKeep != 28 {
		t.Errorf("interval %s keep %d", c.BackupInterval, c.BackupKeep)
	}
}

// Every way the settings can be half right refuses to start, naming what is
// wrong. Starting anyway would be an operator who believes they have off-site
// backups and has none.
func TestBackupMisconfigurationRefusesToStart(t *testing.T) {
	full := map[string]string{
		"SUBGLANCE_BACKUP_TARGET":            "s3://bucket",
		"SUBGLANCE_BACKUP_ACCESS_KEY_ID":     "key-id",
		"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY": "secret",
	}
	cases := []struct {
		name     string
		override map[string]string
		want     string
	}{
		{"credentials but no target", map[string]string{"SUBGLANCE_BACKUP_TARGET": ""}, "backup-target is empty"},
		{"only a secret but no target", map[string]string{"SUBGLANCE_BACKUP_TARGET": "",
			"SUBGLANCE_BACKUP_ACCESS_KEY_ID": ""}, "backup-target is empty"},
		{"only a secret file but no target", map[string]string{"SUBGLANCE_BACKUP_TARGET": "",
			"SUBGLANCE_BACKUP_ACCESS_KEY_ID": "", "SUBGLANCE_BACKUP_SECRET_ACCESS_KEY": "",
			"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE": "/nonexistent/s3.secret"}, "backup-target is empty"},
		{"not an s3 URL", map[string]string{"SUBGLANCE_BACKUP_TARGET": "https://bucket"}, "want s3://bucket"},
		{"no access key", map[string]string{"SUBGLANCE_BACKUP_ACCESS_KEY_ID": ""}, "ACCESS_KEY_ID is empty"},
		{"no secret", map[string]string{"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY": ""}, "no secret access key"},
		{"both secret forms", map[string]string{"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE": "/x"}, "not both"},
		{"missing secret file", map[string]string{"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY": "",
			"SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE": "/nonexistent/s3.secret"}, "SECRET_ACCESS_KEY_FILE"},
		{"interval too short", map[string]string{"SUBGLANCE_BACKUP_INTERVAL": "5m"}, "at least 1h"},
		{"keep zero", map[string]string{"SUBGLANCE_BACKUP_KEEP": "0"}, "at least 1"},
		{"endpoint with a path", map[string]string{"SUBGLANCE_BACKUP_ENDPOINT": "https://minio.example/bucket"}, "without a path"},
		{"interval without a unit", map[string]string{"SUBGLANCE_BACKUP_INTERVAL": "24"}, "SUBGLANCE_BACKUP_INTERVAL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range full {
				t.Setenv(k, v)
			}
			for k, v := range c.override {
				t.Setenv(k, v)
			}
			_, err := Load(nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// The credentials must not be settable as flags, where they would show up in
// `ps` for every user on the host.
func TestBackupCredentialsAreNotFlags(t *testing.T) {
	for _, flag := range []string{"--backup-access-key-id", "--backup-secret-access-key", "--backup-secret-access-key-file"} {
		if _, err := Load([]string{flag, "x"}); err == nil {
			t.Errorf("%s was accepted as a flag", flag)
		}
	}
}
