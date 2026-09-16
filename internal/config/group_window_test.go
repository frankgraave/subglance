package config

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
)

func TestAlertGroupWindowSources(t *testing.T) {
	cases := []struct {
		name string
		env  string
		args []string
		want time.Duration
	}{
		{name: "default", want: notifier.DefaultGroupWindow},
		{name: "flag", args: []string{"--alert-group-window=10s"}, want: 10 * time.Second},
		{name: "environment", env: "5m", want: 5 * time.Minute},
		// Flags still beat the environment, which is the documented precedence.
		{name: "flag over environment", env: "5m", args: []string{"--alert-group-window=20s"}, want: 20 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("SUBGLANCE_ALERT_GROUP_WINDOW", tc.env)
			}
			c, err := Load(tc.args)
			if err != nil {
				t.Fatalf("Load(%v): %v", tc.args, err)
			}
			if c.AlertGroupWindow != tc.want {
				t.Errorf("AlertGroupWindow = %s, want %s", c.AlertGroupWindow, tc.want)
			}
			if got := c.NotifierGroupWindow(); got != tc.want {
				t.Errorf("NotifierGroupWindow() = %s, want %s", got, tc.want)
			}
		})
	}
}

// A malformed or negative window is rejected rather than quietly replaced by
// the default. Someone typing "soon" or -5m has made a mistake, and the
// setting that turns grouping off is documented as 0; swallowing the typo
// would leave them running a window they never chose.
func TestAlertGroupWindowRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		env  string
		args []string
	}{
		{name: "negative flag", args: []string{"--alert-group-window=-5m"}},
		{name: "malformed environment", env: "soon"},
		{name: "negative environment", env: "-5m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("SUBGLANCE_ALERT_GROUP_WINDOW", tc.env)
			}
			if _, err := Load(tc.args); err == nil {
				t.Errorf("Load(%v) accepted a bad alert-group-window", tc.args)
			}
		})
	}
}

// TestAlertGroupWindowZeroDisablesGrouping pins the translation between the two
// meanings of zero. An operator writing 0 means "send immediately"; the
// notifier reads zero as "use the default", so the config layer has to hand it
// the disabled sentinel instead. Getting this backwards would silently keep
// grouping on for someone who asked for it off.
func TestAlertGroupWindowZeroDisablesGrouping(t *testing.T) {
	for _, args := range [][]string{
		{"--alert-group-window=0"},
		{"--alert-group-window=0s"},
	} {
		c, err := Load(args)
		if err != nil {
			t.Fatalf("Load(%v): %v", args, err)
		}
		if c.AlertGroupWindow != 0 {
			t.Errorf("Load(%v): AlertGroupWindow = %s, want 0", args, c.AlertGroupWindow)
		}
		got := c.NotifierGroupWindow()
		if got >= 0 {
			t.Errorf("Load(%v): NotifierGroupWindow() = %s, want a negative (grouping off) value", args, got)
		}
		if got != notifier.GroupingDisabled {
			t.Errorf("Load(%v): NotifierGroupWindow() = %s, want %s", args, got, notifier.GroupingDisabled)
		}
	}
}
