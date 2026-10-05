package store

import (
	"context"
	"testing"
	"time"
)

func TestChannelFailureSpell(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ch, err := db.CreateChannel(ctx, Channel{Name: "ops", Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/hook"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	via, err := db.CreateChannel(ctx, Channel{Name: "mail", Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/other"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)

	if err := db.RecordChannelFailure(ctx, ch.ID, start, "first"); err != nil {
		t.Fatal(err)
	}
	// A later failure in the same spell refreshes the message, not the start.
	if err := db.RecordChannelFailure(ctx, ch.ID, start.Add(time.Hour), "second"); err != nil {
		t.Fatal(err)
	}
	got, err := db.ChannelFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := got[ch.ID]
	if !f.FailedAt.Equal(start) || f.LastError != "second" || !f.NoticedAt.IsZero() {
		t.Fatalf("spell = %+v, want start %v, message second, not noticed", f, start)
	}

	// A notice for a spell that has since been replaced does not mark the
	// new one as told.
	stale := f
	stale.FailedAt = start.Add(-time.Hour)
	if err := db.MarkChannelFailureNoticed(ctx, stale, via.ID, start); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ChannelFailures(ctx); !got[ch.ID].NoticedAt.IsZero() {
		t.Fatal("a notice about an older spell marked the current one as told")
	}
	if err := db.MarkChannelFailureNoticed(ctx, f, via.ID, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ChannelFailures(ctx); got[ch.ID].NoticeChannelID != via.ID || got[ch.ID].NoticedAt.IsZero() {
		t.Fatalf("noticed spell = %+v", got[ch.ID])
	}

	// Deleting the channel that carried the notice keeps the notice sent.
	if err := db.DeleteChannel(ctx, via.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ChannelFailures(ctx); got[ch.ID].NoticeChannelID != 0 || got[ch.ID].NoticedAt.IsZero() {
		t.Fatalf("after deleting the carrier: %+v", got[ch.ID])
	}

	// A delivery ends the spell; deleting the channel removes it outright.
	if err := db.ClearChannelFailure(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ChannelFailures(ctx); len(got) != 0 {
		t.Fatalf("after a delivery: %+v", got)
	}
	if err := db.RecordChannelFailure(ctx, ch.ID, start, "again"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ChannelFailures(ctx); len(got) != 0 {
		t.Fatalf("a deleted channel still reads failing: %+v", got)
	}
}

func TestNoticeCandidates(t *testing.T) {
	channels := []Channel{
		{ID: 1, Enabled: true},
		{ID: 2, Enabled: true},
		{ID: 3, Enabled: false},
		{ID: 4, Enabled: true, IsDefault: true},
		{ID: 5, Enabled: true},
	}
	failing := map[int64]ChannelFailure{1: {ChannelID: 1}, 5: {ChannelID: 5}}

	var ids []int64
	for _, c := range NoticeCandidates(channels, failing, 1) {
		ids = append(ids, c.ID)
	}
	// Not itself (1), not disabled (3), not another failing channel (5);
	// the default first, then by id.
	if len(ids) != 2 || ids[0] != 4 || ids[1] != 2 {
		t.Fatalf("candidates = %v, want [4 2]", ids)
	}

	// Only failing or disabled channels left: nowhere to report it.
	if got := NoticeCandidates(channels[:3], map[int64]ChannelFailure{1: {}, 2: {}}, 1); len(got) != 0 {
		t.Fatalf("candidates = %+v, want none", got)
	}
}
