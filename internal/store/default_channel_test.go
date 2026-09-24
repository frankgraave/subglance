package store

import (
	"errors"
	"testing"
)

func seedRoutedMonitor(t *testing.T, db *DB, name string) Monitor {
	t.Helper()
	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: name, Type: "http", Target: "https://example.com/" + name, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

// A monitor nobody routed alerts through the default. This is the whole
// feature: without it an empty channel list means an outage nobody hears of.
func TestAlertChannelsFallsBackToTheDefault(t *testing.T) {
	db := openTestDB(t)
	m := seedRoutedMonitor(t, db, "site")

	got, usedDefault, err := db.AlertChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("AlertChannels: %v", err)
	}
	if len(got) != 0 || usedDefault {
		t.Fatalf("with no default: channels = %+v, usedDefault = %v; want none, false", got, usedDefault)
	}

	def := seedChannel(t, db, "ops")
	if err := db.SetDefaultChannel(t.Context(), def.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	got, usedDefault, err = db.AlertChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("AlertChannels: %v", err)
	}
	if len(got) != 1 || got[0].ID != def.ID || !usedDefault {
		t.Fatalf("channels = %+v, usedDefault = %v; want the default and true", got, usedDefault)
	}
	if !got[0].IsDefault {
		t.Error("the default channel does not read back as the default")
	}
}

// Own channels win outright: the default does not join them. A monitor that
// was deliberately routed to one place keeps that routing when a default is
// added later.
func TestAlertChannelsPrefersTheMonitorsOwnChannels(t *testing.T) {
	db := openTestDB(t)
	m := seedRoutedMonitor(t, db, "site")
	own := seedChannel(t, db, "own")
	def := seedChannel(t, db, "default")
	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{own.ID}); err != nil {
		t.Fatalf("SetMonitorChannels: %v", err)
	}
	if err := db.SetDefaultChannel(t.Context(), def.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}

	got, usedDefault, err := db.AlertChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("AlertChannels: %v", err)
	}
	if len(got) != 1 || got[0].ID != own.ID || usedDefault {
		t.Fatalf("channels = %+v, usedDefault = %v; want only the monitor's own channel", got, usedDefault)
	}
}

// Setting a second default moves the flag rather than adding one.
func TestSetDefaultChannelMovesTheFlag(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	b := seedChannel(t, db, "b")

	if err := db.SetDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("SetDefaultChannel(a): %v", err)
	}
	if err := db.SetDefaultChannel(t.Context(), b.ID); err != nil {
		t.Fatalf("SetDefaultChannel(b): %v", err)
	}
	// Setting the current default again is a no-op, not a conflict with itself.
	if err := db.SetDefaultChannel(t.Context(), b.ID); err != nil {
		t.Fatalf("SetDefaultChannel(b) again: %v", err)
	}

	all, err := db.ListChannels(t.Context())
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	for _, c := range all {
		if c.IsDefault != (c.ID == b.ID) {
			t.Errorf("channel %s IsDefault = %v", c.Name, c.IsDefault)
		}
	}
}

// An unknown id must leave the current default in place: a typo in a request
// is not a reason for every unrouted monitor to go quiet.
func TestSetDefaultChannelKeepsTheOldDefaultOnAnUnknownID(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	if err := db.SetDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}

	if err := db.SetDefaultChannel(t.Context(), 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	def, ok, err := db.DefaultChannel(t.Context())
	if err != nil || !ok || def.ID != a.ID {
		t.Fatalf("default = %+v, %v, %v; want channel a still", def, ok, err)
	}
}

// The schema itself refuses a second default, whatever code path tries it.
func TestSchemaAllowsOnlyOneDefault(t *testing.T) {
	db := openTestDB(t)
	seedChannel(t, db, "a")
	seedChannel(t, db, "b")

	if _, err := db.Writer.ExecContext(t.Context(),
		"UPDATE notif_channels SET is_default = 1"); err == nil {
		t.Fatal("two channels were marked default; the unique index should refuse it")
	}
}

func TestClearDefaultChannel(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	b := seedChannel(t, db, "b")
	if err := db.SetDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}

	// Clearing a channel that is not the default leaves the real one alone.
	if err := db.ClearDefaultChannel(t.Context(), b.ID); err != nil {
		t.Fatalf("ClearDefaultChannel(b): %v", err)
	}
	if _, ok, err := db.DefaultChannel(t.Context()); err != nil || !ok {
		t.Fatalf("clearing a non-default channel removed the default: ok = %v, err = %v", ok, err)
	}

	if err := db.ClearDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("ClearDefaultChannel(a): %v", err)
	}
	if _, ok, err := db.DefaultChannel(t.Context()); err != nil || ok {
		t.Fatalf("the default survived being cleared: ok = %v, err = %v", ok, err)
	}
	if err := db.ClearDefaultChannel(t.Context(), 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Deleting the default channel takes the default with it. This is why the
// default is a flag on the row and not an id in the settings table.
func TestDeletingTheDefaultChannelClearsTheDefault(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	if err := db.SetDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	if err := db.DeleteChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, ok, err := db.DefaultChannel(t.Context()); ok || err != nil {
		t.Fatalf("default after delete: ok = %v, err = %v; want none", ok, err)
	}
}

// Editing a channel must not quietly drop or grant the default. Update takes a
// whole Channel, and a zero IsDefault in it is "not mentioned", not "false".
func TestUpdateChannelLeavesTheDefaultAlone(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	if err := db.SetDefaultChannel(t.Context(), a.ID); err != nil {
		t.Fatalf("SetDefaultChannel: %v", err)
	}
	a.Name = "renamed"
	a.IsDefault = false
	saved, err := db.UpdateChannel(t.Context(), a)
	if err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	// The returned row is what the API answers a PUT with, so it must carry
	// the stored flag, not the caller's stale copy.
	if !saved.IsDefault {
		t.Fatal("UpdateChannel returned is_default = false for the default channel")
	}
	def, ok, err := db.DefaultChannel(t.Context())
	if err != nil || !ok || def.ID != a.ID || def.Name != "renamed" {
		t.Fatalf("default = %+v, %v, %v; want the renamed channel", def, ok, err)
	}
}
