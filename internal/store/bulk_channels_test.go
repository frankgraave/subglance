package store

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
)

func ownChannelIDs(t *testing.T, db *DB, monitorID int64) []int64 {
	t.Helper()
	channels, err := db.ListMonitorChannels(t.Context(), monitorID)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, c := range channels {
		ids = append(ids, c.ID)
	}
	return ids
}

func seedLinkedMonitor(t *testing.T, db *DB, name string, channels ...int64) Monitor {
	t.Helper()
	m, err := db.CreateMonitor(t.Context(), Monitor{Name: name, Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMonitorChannels(t.Context(), m.ID, channels); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestChangeMonitorChannelsAddsOnlyWhereMissingAndKeepsOtherLinks(t *testing.T) {
	db := openTestDB(t)
	ops, mail := seedChannel(t, db, "ops"), seedChannel(t, db, "mail")
	a := seedLinkedMonitor(t, db, "a", mail.ID)
	b := seedLinkedMonitor(t, db, "b", ops.ID)
	c := seedLinkedMonitor(t, db, "c")
	untouched := seedLinkedMonitor(t, db, "untouched", mail.ID)
	op := ChannelOperation{Action: "add", MonitorIDs: []int64{c.ID, a.ID, b.ID}, ChannelID: ops.ID}

	preview, etag, err := db.ChangeMonitorChannels(t.Context(), op, "", true)
	if err != nil {
		t.Fatal(err)
	}
	want := ChannelOperationResult{Total: 3, Changed: 2, Unchanged: 1, ChannelEnabled: true}
	if preview != want {
		t.Fatalf("preview = %+v, want %+v", preview, want)
	}
	if got := ownChannelIDs(t, db, a.ID); !slices.Equal(got, []int64{mail.ID}) {
		t.Fatalf("a preview wrote links: %v", got)
	}
	before, _ := db.GetMonitor(t.Context(), b.ID)
	aBefore, _ := db.GetMonitor(t.Context(), a.ID)

	committed, _, err := db.ChangeMonitorChannels(t.Context(), op, etag, false)
	if err != nil {
		t.Fatal(err)
	}
	if committed != want {
		t.Fatalf("commit = %+v, want %+v", committed, want)
	}
	for _, check := range []struct {
		id   int64
		want []int64
	}{{a.ID, []int64{ops.ID, mail.ID}}, {b.ID, []int64{ops.ID}}, {c.ID, []int64{ops.ID}}, {untouched.ID, []int64{mail.ID}}} {
		if got := ownChannelIDs(t, db, check.id); !slices.Equal(got, check.want) {
			t.Fatalf("monitor %d links = %v, want %v", check.id, got, check.want)
		}
	}
	after, _ := db.GetMonitor(t.Context(), b.ID)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("an unchanged monitor's version moved")
	}
	// The version behind a monitor's ETag: an edit form still holding the
	// older one must fail its precondition instead of putting the links back.
	changed, _ := db.GetMonitor(t.Context(), a.ID)
	if !changed.UpdatedAt.After(aBefore.UpdatedAt) {
		t.Fatal("a changed monitor's version did not move")
	}
}

func TestChangeMonitorChannelsRemoveCountsMonitorsLeftWithoutOwnChannels(t *testing.T) {
	db := openTestDB(t)
	ops, mail := seedChannel(t, db, "ops"), seedChannel(t, db, "mail")
	only := seedLinkedMonitor(t, db, "only", ops.ID)
	both := seedLinkedMonitor(t, db, "both", ops.ID, mail.ID)
	none := seedLinkedMonitor(t, db, "none", mail.ID)
	op := ChannelOperation{Action: "remove", MonitorIDs: []int64{only.ID, both.ID, none.ID}, ChannelID: ops.ID}

	preview, etag, err := db.ChangeMonitorChannels(t.Context(), op, "", true)
	if err != nil {
		t.Fatal(err)
	}
	want := ChannelOperationResult{Total: 3, Changed: 2, Unchanged: 1, LeftWithoutOwn: 1, ChannelEnabled: true}
	if preview != want {
		t.Fatalf("preview = %+v, want %+v", preview, want)
	}
	if _, _, err := db.ChangeMonitorChannels(t.Context(), op, etag, false); err != nil {
		t.Fatal(err)
	}
	if got := ownChannelIDs(t, db, only.ID); len(got) != 0 {
		t.Fatalf("only links = %v", got)
	}
	if got := ownChannelIDs(t, db, both.ID); !slices.Equal(got, []int64{mail.ID}) {
		t.Fatalf("both links = %v", got)
	}
	if got := ownChannelIDs(t, db, none.ID); !slices.Equal(got, []int64{mail.ID}) {
		t.Fatalf("none links = %v", got)
	}
}

// A preview is a promise about specific counts. A link added by someone else,
// or the channel switched off, between preview and confirm must make the
// confirm fail rather than write a different change than the one reviewed.
func TestChangeMonitorChannelsRefusesAStalePreview(t *testing.T) {
	for name, interfere := range map[string]func(t *testing.T, db *DB, monitor, channel int64){
		"link changed": func(t *testing.T, db *DB, monitor, channel int64) {
			if err := db.SetMonitorChannels(t.Context(), monitor, []int64{channel}); err != nil {
				t.Fatal(err)
			}
		},
		"channel disabled": func(t *testing.T, db *DB, _, channel int64) {
			if _, err := db.Writer.ExecContext(t.Context(), "UPDATE notif_channels SET enabled = 0 WHERE id = ?", channel); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			ops := seedChannel(t, db, "ops")
			a := seedLinkedMonitor(t, db, "a")
			b := seedLinkedMonitor(t, db, "b")
			op := ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID, b.ID}, ChannelID: ops.ID}
			_, etag, err := db.ChangeMonitorChannels(t.Context(), op, "", true)
			if err != nil {
				t.Fatal(err)
			}
			interfere(t, db, a.ID, ops.ID)
			if _, _, err := db.ChangeMonitorChannels(t.Context(), op, etag, false); !errors.Is(err, ErrChannelPreviewChanged) {
				t.Fatalf("stale commit err = %v, want ErrChannelPreviewChanged", err)
			}
			if got := ownChannelIDs(t, db, b.ID); len(got) != 0 {
				t.Fatalf("a refused commit wrote b's links: %v", got)
			}
		})
	}
}

func TestChangeMonitorChannelsRejectsWithoutWriting(t *testing.T) {
	db := openTestDB(t)
	ops := seedChannel(t, db, "ops")
	a := seedLinkedMonitor(t, db, "a")
	for name, tc := range map[string]struct {
		op   ChannelOperation
		want error
	}{
		"unknown action":  {ChannelOperation{Action: "replace", MonitorIDs: []int64{a.ID}, ChannelID: ops.ID}, ErrInvalidChannelOperation},
		"no channel":      {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID}}, ErrInvalidChannelOperation},
		"empty selection": {ChannelOperation{Action: "add", ChannelID: ops.ID}, ErrInvalidChannelOperation},
		"duplicate id":    {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID, a.ID}, ChannelID: ops.ID}, ErrInvalidChannelOperation},
		"unknown channel": {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID}, ChannelID: ops.ID + 99}, ErrUnknownChannel},
		"missing monitor": {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID, a.ID + 99}, ChannelID: ops.ID}, sql.ErrNoRows},
		"non-positive id": {ChannelOperation{Action: "add", MonitorIDs: []int64{0}, ChannelID: ops.ID}, ErrInvalidChannelOperation},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := db.ChangeMonitorChannels(t.Context(), tc.op, "", true); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if got := ownChannelIDs(t, db, a.ID); len(got) != 0 {
		t.Fatalf("a rejected operation wrote links: %v", got)
	}
}

func TestValidChannelPreviewETag(t *testing.T) {
	good := `"channels-` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `"`
	if !ValidChannelPreviewETag(good) {
		t.Fatal("a preview validator was refused")
	}
	for _, bad := range []string{"", "*", `W/` + good, `"tags-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`, good[:len(good)-2] + `G"`} {
		if ValidChannelPreviewETag(bad) {
			t.Fatalf("%q was accepted", bad)
		}
	}
}
