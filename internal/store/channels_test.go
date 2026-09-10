package store

import (
	"errors"
	"testing"
)

func seedChannel(t *testing.T, db *DB, name string) Channel {
	t.Helper()
	c, err := db.CreateChannel(t.Context(), Channel{
		Name: name, Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/" + name}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	return c
}

func TestChannelConfigSurvivesARoundTrip(t *testing.T) {
	db := openTestDB(t)

	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelTelegram,
		Config:  map[string]string{"bot_token": "abc123", "chat_id": "-100"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	got, err := db.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config["bot_token"] != "abc123" || got.Config["chat_id"] != "-100" {
		t.Errorf("config = %v, want both keys intact", got.Config)
	}
}

// A channel with no config must read back as an empty map, not nil. Callers
// index into Config directly; a nil map would read fine but panic on write,
// and the difference would only surface at delivery time.
func TestChannelWithoutConfigReadsAsEmptyMap(t *testing.T) {
	db := openTestDB(t)

	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "bare", Type: ChannelEmail, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	got, err := db.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config == nil {
		t.Fatal("Config is nil; callers expect an empty map")
	}
	if len(got.Config) != 0 {
		t.Errorf("Config = %v, want empty", got.Config)
	}
}

func TestGetChannelReportsNotFound(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.GetChannel(t.Context(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteChannel(t.Context(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := db.UpdateChannel(t.Context(), Channel{ID: 999, Name: "x", Type: ChannelWebhook}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Repeating an id in the request is the client being sloppy, not an error:
// the resulting set is identical either way, and rejecting it would make a
// UI that submits a checkbox list twice fail for no reason.
func TestSetMonitorChannelsToleratesDuplicates(t *testing.T) {
	db := openTestDB(t)

	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	c := seedChannel(t, db, "a")

	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{c.ID, c.ID}); err != nil {
		t.Fatalf("SetMonitorChannels: %v", err)
	}

	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("assignments = %+v, want exactly one", got)
	}
}

func TestSetMonitorChannelsRejectsUnknownChannel(t *testing.T) {
	db := openTestDB(t)

	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	err = db.SetMonitorChannels(t.Context(), m.ID, []int64{4242})
	if !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("err = %v, want ErrUnknownChannel", err)
	}
}

// Deleting a monitor must take its assignments with it. Without the cascade,
// the rows outlive the monitor and a reused id would inherit its alerting.
func TestDeletingAMonitorClearsItsChannelAssignments(t *testing.T) {
	db := openTestDB(t)

	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	c := seedChannel(t, db, "a")
	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{c.ID}); err != nil {
		t.Fatalf("SetMonitorChannels: %v", err)
	}

	if err := db.DeleteMonitor(t.Context(), m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	var n int
	if err := db.Reader.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM monitor_channels WHERE monitor_id = ?", m.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d assignment rows survived the monitor", n)
	}
}
