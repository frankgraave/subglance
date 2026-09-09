package config

import (
	"errors"
	"flag"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if c.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", c.DataDir)
	}
	if c.AllowPrivateTargets {
		t.Error("AllowPrivateTargets must default to false: an open default turns SubGlance into an SSRF proxy")
	}
}

func TestDBPath(t *testing.T) {
	tests := []struct {
		dataDir string
		want    string
	}{
		{"/data", "/data/subglance.db"},
		{"/data/", "/data/subglance.db"},
		{"/var/lib/subglance///", "/var/lib/subglance/subglance.db"},
	}
	for _, tt := range tests {
		if got := (Config{DataDir: tt.dataDir}).DBPath(); got != tt.want {
			t.Errorf("DBPath(%q) = %q, want %q", tt.dataDir, got, tt.want)
		}
	}
}

func TestFlagsOverrideEnv(t *testing.T) {
	t.Setenv("SUBGLANCE_ADDR", ":9999")
	t.Setenv("SUBGLANCE_LOG_LEVEL", "warn")

	c, err := Load([]string{"-addr", ":7777"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":7777" {
		t.Errorf("Addr = %q, want :7777 (flag must beat env)", c.Addr)
	}
	if c.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn (env must beat default)", c.LogLevel)
	}
}

func TestEnvParsing(t *testing.T) {
	t.Setenv("SUBGLANCE_SHUTDOWN_TIMEOUT", "45s")
	t.Setenv("SUBGLANCE_CHECK_WORKERS", "64")
	t.Setenv("SUBGLANCE_ALLOW_PRIVATE_TARGETS", "true")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ShutdownTimeout != 45*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 45s", c.ShutdownTimeout)
	}
	if c.CheckWorkers != 64 {
		t.Errorf("CheckWorkers = %d, want 64", c.CheckWorkers)
	}
	if !c.AllowPrivateTargets {
		t.Error("AllowPrivateTargets = false, want true")
	}
}

func TestMalformedEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("SUBGLANCE_CHECK_WORKERS", "not-a-number")
	t.Setenv("SUBGLANCE_SHUTDOWN_TIMEOUT", "banana")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.CheckWorkers != 0 {
		t.Errorf("CheckWorkers = %d, want 0 (default)", c.CheckWorkers)
	}
	if c.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 15s (default)", c.ShutdownTimeout)
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"empty addr", []string{"-addr", ""}},
		{"empty data-dir", []string{"-data-dir", ""}},
		{"bad log level", []string{"-log-level", "loud"}},
		{"bad log format", []string{"-log-format", "xml"}},
		{"zero shutdown timeout", []string{"-shutdown-timeout", "0s"}},
		{"negative workers", []string{"-check-workers", "-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(tt.args); err == nil {
				t.Errorf("Load(%v) succeeded, want error", tt.args)
			}
		})
	}
}

func TestHelpReturnsErrHelp(t *testing.T) {
	_, err := Load([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("Load(-h) error = %v, want flag.ErrHelp", err)
	}
}
