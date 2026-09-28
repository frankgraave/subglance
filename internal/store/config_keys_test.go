package store

import (
	"context"
	"errors"
	"testing"
)

func TestConfigKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := newTestMonitor(t, db, "a")
	b := newTestMonitor(t, db, "b")

	if err := db.SetMonitorConfigKey(ctx, a.ID, "shop"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.SetMonitorConfigKey(ctx, b.ID, "shop"); !errors.Is(err, ErrConfigKeyTaken) {
		t.Errorf("second monitor with the same key: %v, want ErrConfigKeyTaken", err)
	}
	if err := db.SetMonitorConfigKey(ctx, 9999, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("key for a missing monitor: %v, want ErrNotFound", err)
	}
	// Replacing a monitor's own key is allowed and leaves one row.
	if err := db.SetMonitorConfigKey(ctx, a.ID, "webshop"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	keys, err := db.MonitorConfigKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[a.ID] != "webshop" {
		t.Errorf("keys = %v, want only %d: webshop", keys, a.ID)
	}

	// Channels have their own namespace: the same key is free there.
	c, err := db.CreateChannel(ctx, Channel{Name: "c", Type: ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetChannelConfigKey(ctx, c.ID, "webshop"); err != nil {
		t.Errorf("channel key equal to a monitor key: %v", err)
	}

	// The key goes with its object.
	if err := db.DeleteMonitor(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteChannel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	mk, _ := db.MonitorConfigKeys(ctx)
	ck, _ := db.ChannelConfigKeys(ctx)
	if len(mk)+len(ck) != 0 {
		t.Errorf("keys outlived their objects: monitors %v, channels %v", mk, ck)
	}
}
