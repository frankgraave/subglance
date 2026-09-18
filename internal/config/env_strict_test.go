package config

import (
	"strings"
	"testing"
	"time"
)

// A malformed environment variable must refuse to start, exactly as an invalid
// flag does.
//
// The two examples in the ticket are the ones that hurt most. "yes" is not a Go
// bool, so an operator who meant to turn on LAN monitoring silently kept the
// SSRF guard and then spent an afternoon on why every internal monitor fails.
// "300" has no unit, so an operator who believes they have a dead man's switch
// on a five minute timer has the default instead. Neither produced a line at
// any log level.
//
// The error has to name the variable and the text that was read: "invalid
// duration" alone, from a process with six duration settings, tells the person
// reading the container log nothing they can act on.
func TestMalformedEnvRefusesToStart(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"bool that is not a Go bool", "SUBGLANCE_ALLOW_PRIVATE_TARGETS", "yes"},
		{"duration with no unit", "SUBGLANCE_WATCHDOG_INTERVAL", "300"},
		{"duration that is a word", "SUBGLANCE_SHUTDOWN_TIMEOUT", "soon"},
		{"raw retention with no unit", "SUBGLANCE_RAW_RETENTION", "7"},
		{"rollup retention with no unit", "SUBGLANCE_ROLLUP_RETENTION", "365"},
		{"worker count that is not a number", "SUBGLANCE_CHECK_WORKERS", "lots"},
		{"worker count with a unit", "SUBGLANCE_CHECK_WORKERS", "8x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)

			_, err := Load(nil)
			if err == nil {
				t.Fatalf("%s=%q started the process on the default instead of failing",
					tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error does not name the variable: %v", err)
			}
			if !strings.Contains(err.Error(), tc.value) {
				t.Errorf("error does not quote the bad value %q: %v", tc.value, err)
			}
		})
	}
}

// Every malformed variable is reported in one run. An operator fixing a
// compose file should not have to restart once per typo to discover the next.
func TestMalformedEnvReportsEveryVariable(t *testing.T) {
	t.Setenv("SUBGLANCE_ALLOW_PRIVATE_TARGETS", "yes")
	t.Setenv("SUBGLANCE_WATCHDOG_INTERVAL", "300")

	_, err := Load(nil)
	if err == nil {
		t.Fatal("two malformed variables started the process")
	}
	for _, key := range []string{"SUBGLANCE_ALLOW_PRIVATE_TARGETS", "SUBGLANCE_WATCHDOG_INTERVAL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("only some variables were reported; %s is missing from: %v", key, err)
		}
	}
}

// Well-formed values still load, and an unset or empty variable still means
// "use the default" — the strictness must not turn an absent setting into an
// error.
func TestWellFormedEnvStillLoads(t *testing.T) {
	t.Setenv("SUBGLANCE_ALLOW_PRIVATE_TARGETS", "true")
	t.Setenv("SUBGLANCE_WATCHDOG_INTERVAL", "5m")
	t.Setenv("SUBGLANCE_CHECK_WORKERS", "32")
	t.Setenv("SUBGLANCE_RAW_RETENTION", "")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.AllowPrivateTargets {
		t.Error("AllowPrivateTargets = false, want true")
	}
	if c.WatchdogInterval != 5*time.Minute {
		t.Errorf("WatchdogInterval = %s, want 5m", c.WatchdogInterval)
	}
	if c.CheckWorkers != 32 {
		t.Errorf("CheckWorkers = %d, want 32", c.CheckWorkers)
	}
	if c.RawRetention != defaults().RawRetention {
		t.Errorf("an empty variable overrode the default: RawRetention = %s", c.RawRetention)
	}
}
