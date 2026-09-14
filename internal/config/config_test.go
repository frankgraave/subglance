package config

import (
	"errors"
	"flag"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
	"github.com/frankgraave/subglance/internal/watchdog"
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

func TestWatchdogDefaultsToOff(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WatchdogURL != "" {
		t.Fatalf("WatchdogURL = %q, want it off by default", c.WatchdogURL)
	}
	if c.WatchdogInterval != watchdog.DefaultInterval {
		t.Fatalf("WatchdogInterval = %s, want %s", c.WatchdogInterval, watchdog.DefaultInterval)
	}
}

func TestWatchdogFlagsAndEnv(t *testing.T) {
	t.Setenv("SUBGLANCE_WATCHDOG_URL", "https://hc-ping.com/from-env")
	t.Setenv("SUBGLANCE_WATCHDOG_INTERVAL", "90s")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WatchdogURL != "https://hc-ping.com/from-env" {
		t.Fatalf("WatchdogURL = %q", c.WatchdogURL)
	}
	if c.WatchdogInterval != 90*time.Second {
		t.Fatalf("WatchdogInterval = %s, want 90s", c.WatchdogInterval)
	}

	c, err = Load([]string{"--watchdog-url", "https://hc-ping.com/from-flag", "--watchdog-interval", "2m"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WatchdogURL != "https://hc-ping.com/from-flag" {
		t.Fatalf("flag should beat environment, got %q", c.WatchdogURL)
	}
	if c.WatchdogInterval != 2*time.Minute {
		t.Fatalf("WatchdogInterval = %s, want 2m", c.WatchdogInterval)
	}
}

func TestWatchdogURLIsValidated(t *testing.T) {
	// A typo here is silent otherwise: the operator believes they are
	// covered and nothing ever pings.
	if _, err := Load([]string{"--watchdog-url", "hc-ping.com/abc"}); err == nil {
		t.Fatal("a URL without a scheme should be rejected")
	}
	if _, err := Load([]string{"--watchdog-url", "https://hc-ping.com/abc", "--watchdog-interval", "0"}); err == nil {
		t.Fatal("a zero interval with a configured URL should be rejected")
	}
	// An interval is irrelevant while the watchdog is off, so it must not
	// block startup.
	if _, err := Load([]string{"--watchdog-interval", "0"}); err != nil {
		t.Fatalf("interval should not be validated while the watchdog is off: %v", err)
	}
}

func TestRetentionDefaultsAndFlags(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.RawRetention != store.DefaultRawRetention {
		t.Errorf("RawRetention = %s, want %s", c.RawRetention, store.DefaultRawRetention)
	}
	if c.RollupRetention != store.DefaultRollupRetention {
		t.Errorf("RollupRetention = %s, want %s", c.RollupRetention, store.DefaultRollupRetention)
	}

	c, err = Load([]string{"--raw-retention=48h", "--rollup-retention=720h"})
	if err != nil {
		t.Fatalf("Load with retention flags: %v", err)
	}
	if c.RawRetention != 48*time.Hour {
		t.Errorf("RawRetention = %s, want 48h", c.RawRetention)
	}
	if c.RollupRetention != 720*time.Hour {
		t.Errorf("RollupRetention = %s, want 720h", c.RollupRetention)
	}
}

func TestRetentionFromEnvironment(t *testing.T) {
	t.Setenv("SUBGLANCE_RAW_RETENTION", "24h")
	t.Setenv("SUBGLANCE_ROLLUP_RETENTION", "0s")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.RawRetention != 24*time.Hour {
		t.Errorf("RawRetention = %s, want 24h", c.RawRetention)
	}
	if c.RollupRetention != 0 {
		t.Errorf("RollupRetention = %s, want 0 (keep forever)", c.RollupRetention)
	}
}

func TestRetentionValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"raw retention zero", []string{"--raw-retention=0"}},
		{"raw retention negative", []string{"--raw-retention=-1h"}},
		{"rollup retention negative", []string{"--rollup-retention=-1h"}},
		// A rollup window inside the raw one would delete buckets whose own
		// heartbeats are still present, so history would flicker.
		{"rollup inside raw", []string{"--raw-retention=168h", "--rollup-retention=24h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(tt.args); err == nil {
				t.Errorf("Load(%v) accepted an invalid retention window", tt.args)
			}
		})
	}
}
