package config

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/notifier"
)

func TestAlertGroupWindowDefaultsAndFlag(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AlertGroupWindow != notifier.DefaultGroupWindow {
		t.Errorf("AlertGroupWindow = %s, want %s", c.AlertGroupWindow, notifier.DefaultGroupWindow)
	}
	if got := c.NotifierGroupWindow(); got != notifier.DefaultGroupWindow {
		t.Errorf("NotifierGroupWindow() = %s, want %s", got, notifier.DefaultGroupWindow)
	}

	c, err = Load([]string{"--alert-group-window=10s"})
	if err != nil {
		t.Fatalf("Load with --alert-group-window: %v", err)
	}
	if c.AlertGroupWindow != 10*time.Second {
		t.Errorf("AlertGroupWindow = %s, want 10s", c.AlertGroupWindow)
	}
	if got := c.NotifierGroupWindow(); got != 10*time.Second {
		t.Errorf("NotifierGroupWindow() = %s, want 10s", got)
	}
}

func TestAlertGroupWindowFromEnvironment(t *testing.T) {
	t.Setenv("SUBGLANCE_ALERT_GROUP_WINDOW", "5m")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AlertGroupWindow != 5*time.Minute {
		t.Errorf("AlertGroupWindow = %s, want 5m", c.AlertGroupWindow)
	}

	// Flags still beat the environment, which is the documented precedence.
	c, err = Load([]string{"--alert-group-window=20s"})
	if err != nil {
		t.Fatalf("Load with flag over environment: %v", err)
	}
	if c.AlertGroupWindow != 20*time.Second {
		t.Errorf("AlertGroupWindow = %s, want 20s", c.AlertGroupWindow)
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

// A negative window is rejected rather than quietly treated as off. Someone
// typing -5m has made a mistake, and the setting that turns grouping off is
// documented as 0; accepting both spellings would leave two ways to say the
// same thing and no way to catch the typo.
func TestAlertGroupWindowNegativeIsRejected(t *testing.T) {
	if _, err := Load([]string{"--alert-group-window=-5m"}); err == nil {
		t.Error("Load accepted a negative alert-group-window")
	}
}
