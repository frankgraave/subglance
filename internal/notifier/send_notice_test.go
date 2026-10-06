package notifier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func backupNotice() Alert {
	return Alert{MonitorName: "SubGlance", Target: "Scheduled database backup",
		Event: EventBackupFailed, LastError: "upload: AccessDenied"}
}

func TestSendNoticeGoesToTheDefaultChannel(t *testing.T) {
	db, _, ch := testDB(t)
	ctx := context.Background()
	sender := &fakeSender{}
	n := newTestNotifier(t, db, sender, time.Now)

	if err := n.SendNotice(ctx, backupNotice()); !errors.Is(err, ErrNoticeNotSent) {
		t.Fatalf("with no default channel: err = %v, want ErrNoticeNotSent", err)
	}
	if len(sender.delivered()) != 0 {
		t.Fatal("sent a notice with no default channel")
	}

	if err := db.SetDefaultChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := n.SendNotice(ctx, backupNotice()); err != nil {
		t.Fatalf("SendNotice: %v", err)
	}
	got := sender.delivered()
	if len(got) != 1 || got[0].Title() != "SubGlance could not back up its database" ||
		got[0].LastError != "upload: AccessDenied" || !got[0].Down() {
		t.Fatalf("delivered %+v", got)
	}
}

func TestSendNoticeWaitsOutQuietHours(t *testing.T) {
	db, _, ch := testDB(t)
	ctx := context.Background()
	if err := db.SetDefaultChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetQuietHours(ctx, store.QuietHours{ChannelID: ch.ID, Start: "22:00", End: "07:00",
		Timezone: "UTC", During: store.QuietHold}); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	night := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	n := newTestNotifier(t, db, sender, func() time.Time { return night })

	if err := n.SendNotice(ctx, backupNotice()); !errors.Is(err, ErrNoticeNotSent) {
		t.Fatalf("during quiet hours: err = %v, want ErrNoticeNotSent", err)
	}
	if len(sender.delivered()) != 0 {
		t.Fatal("a backup notice woke someone during quiet hours")
	}
}
