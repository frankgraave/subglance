package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestConnectivitySettingsDefaultToOnWithTheDefaultTargets(t *testing.T) {
	db := openTestDB(t)
	got, err := db.ResolveConnectivity(context.Background(), ConnectivityPins{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled.Value || got.Enabled.Source != RetentionSourceDefault {
		t.Fatalf("enabled = %+v, want on by default", got.Enabled)
	}
	want := []string{"1.1.1.1:53", "9.9.9.9:53"}
	if !reflect.DeepEqual(got.Targets.Value, want) || got.Targets.Source != RetentionSourceDefault {
		t.Fatalf("targets = %+v, want the defaults %v", got.Targets, want)
	}
}

func TestConnectivitySettingsSaveAndResolve(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	off := false
	v, err := db.SaveConnectivity(ctx, ConnectivityChange{Enabled: &off, Targets: []string{"gateway:443", "[2001:db8::1]:22"}})
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("first save returned version %d, want 1", v)
	}
	got, err := db.ResolveConnectivity(ctx, ConnectivityPins{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled.Value || got.Enabled.Source != RetentionSourceDatabase {
		t.Fatalf("enabled = %+v, want the saved off", got.Enabled)
	}
	if !reflect.DeepEqual(got.Targets.Value, []string{"gateway:443", "[2001:db8::1]:22"}) || got.Targets.Source != RetentionSourceDatabase {
		t.Fatalf("targets = %+v", got.Targets)
	}

	// A save of one field leaves the other as it was.
	on := true
	if _, err := db.SaveConnectivity(ctx, ConnectivityChange{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ResolveConnectivity(ctx, ConnectivityPins{})
	if !got.Enabled.Value || len(got.Targets.Value) != 2 {
		t.Fatalf("a save of enabled alone changed the targets: %+v", got)
	}

	// A pin wins over what is stored, and says which flag set it.
	got, err = db.ResolveConnectivity(ctx, ConnectivityPins{
		Enabled: &ConnectivityEnabledPin{Value: false, By: "--connectivity-check"},
		Targets: &ConnectivityTargetsPin{Value: []string{"router:80"}, By: "SUBGLANCE_CONNECTIVITY_TARGETS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled.Value || got.Enabled.Source != RetentionSourcePinned || got.Enabled.PinnedBy != "--connectivity-check" {
		t.Fatalf("pinned enabled = %+v", got.Enabled)
	}
	if !reflect.DeepEqual(got.Targets.Value, []string{"router:80"}) || got.Targets.PinnedBy != "SUBGLANCE_CONNECTIVITY_TARGETS" {
		t.Fatalf("pinned targets = %+v", got.Targets)
	}
}

// A list the canary would refuse is never written: the next start reads it
// back and would fail on it.
func TestConnectivitySettingsRefuseABadList(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, bad := range [][]string{{}, {"gateway"}, {"gateway:0"}, {"gate,way:53"}} {
		if _, err := db.SaveConnectivity(ctx, ConnectivityChange{Targets: bad}); err == nil {
			t.Fatalf("saved %q", bad)
		}
	}
	if v, _ := db.ConnectivityVersion(ctx); v != 0 {
		t.Fatalf("a refused save moved the version to %d", v)
	}
}

// A row this code did not write is refused rather than guessed.
func TestConnectivitySettingsRefuseAForeignRow(t *testing.T) {
	for key, value := range map[string]string{
		settingConnectivityEnabled: "maybe",
		settingConnectivityTargets: "gateway",
	} {
		t.Run(key, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			if err := db.putSetting(ctx, key, value); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ResolveConnectivity(ctx, ConnectivityPins{}); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("ResolveConnectivity with %s=%q: %v", key, value, err)
			}
		})
	}
}

func TestConnectivitySettingsConditionalSave(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	on := true
	v, err := db.SaveConnectivityIfVersion(ctx, ConnectivityChange{Enabled: &on}, []int64{0})
	if err != nil || v != 1 {
		t.Fatalf("save at the current version: %d, %v", v, err)
	}
	if _, err := db.SaveConnectivityIfVersion(ctx, ConnectivityChange{Targets: []string{"gateway:443"}}, []int64{0}); !errors.Is(err, ErrConnectivityVersion) {
		t.Fatalf("save at a stale version: %v, want ErrConnectivityVersion", err)
	}
	got, _ := db.ResolveConnectivity(ctx, ConnectivityPins{})
	if got.Targets.Source != RetentionSourceDefault {
		t.Fatalf("a refused save wrote the targets: %+v", got.Targets)
	}
}
