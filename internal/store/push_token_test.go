package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestReplacePushTokenRetiresTheOldURL is the contract the replace route is
// for: the leaked token stops resolving at once, the new one resolves to the
// same monitor, and the plaintext exists only in the return value.
func TestReplacePushTokenRetiresTheOldURL(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "nightly backup", 3600, 300)

	replaced, err := db.ReplacePushToken(ctx, m.ID)
	if err != nil {
		t.Fatalf("ReplacePushToken: %v", err)
	}
	if replaced.PushToken == "" || replaced.PushToken == m.PushToken {
		t.Fatalf("new token = %q, want a fresh one (old %q)", replaced.PushToken, m.PushToken)
	}
	if replaced.PushTokenPrefix == m.PushTokenPrefix || replaced.PushToken[:len(replaced.PushTokenPrefix)] != replaced.PushTokenPrefix {
		t.Errorf("prefix = %q, want the new token's (old %q)", replaced.PushTokenPrefix, m.PushTokenPrefix)
	}

	if _, err := db.MonitorByPushToken(ctx, m.PushToken); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("old token: err = %v, want sql.ErrNoRows", err)
	}
	got, err := db.MonitorByPushToken(ctx, replaced.PushToken)
	if err != nil || got.ID != m.ID {
		t.Fatalf("new token resolved to %d, %v; want monitor %d", got.ID, err, m.ID)
	}

	reread, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if reread.PushToken != "" {
		t.Error("GetMonitor returned the plaintext push token after a replace")
	}
	if reread.PushTokenPrefix != replaced.PushTokenPrefix {
		t.Errorf("stored prefix = %q, want %q", reread.PushTokenPrefix, replaced.PushTokenPrefix)
	}
}

// TestReplacePushTokenKeepsHistoryAndDeadline: a new URL is neither a report
// from the job nor a new monitor. History stays, and the window is measured
// from where it was, so an overdue job does not get a fresh window for free.
func TestReplacePushTokenKeepsHistoryAndDeadline(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	m := mustCreatePushMonitor(t, db, "nightly backup", 3600, 300)
	at := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: m.ID, TS: at, OK: true}); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := db.OpenIncident(ctx, m.ID, at.Add(time.Hour), "push", "no report"); err != nil {
		t.Fatalf("open incident: %v", err)
	}
	before, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	lastBefore, err := db.LastActivity(ctx, before)
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}

	if _, err := db.ReplacePushToken(ctx, m.ID); err != nil {
		t.Fatalf("ReplacePushToken: %v", err)
	}

	after, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updated_at = %v, want past %v: it is the version behind the ETag", after.UpdatedAt, before.UpdatedAt)
	}
	if !after.ResumedAt.Equal(before.ResumedAt) || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("window anchors moved: resumed %v -> %v, created %v -> %v",
			before.ResumedAt, after.ResumedAt, before.CreatedAt, after.CreatedAt)
	}
	lastAfter, err := db.LastActivity(ctx, after)
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}
	if !lastAfter.Equal(lastBefore) {
		t.Errorf("last activity = %v, want %v", lastAfter, lastBefore)
	}
	if !after.PushWindow(lastAfter, time.Time{}).Deadline.Equal(before.PushWindow(lastBefore, time.Time{}).Deadline) {
		t.Error("replacing the token moved the push deadline")
	}

	hbs, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil || len(hbs) != 1 {
		t.Errorf("heartbeats = %d, %v; want the 1 recorded before the replace", len(hbs), err)
	}
	if _, err := db.OpenIncidentFor(ctx, m.ID); err != nil {
		t.Errorf("open incident lost: %v", err)
	}
}

func TestReplacePushTokenRefusals(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	httpMon, err := db.CreateMonitor(ctx, Monitor{Name: "site", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create http monitor: %v", err)
	}
	if _, err := db.ReplacePushToken(ctx, httpMon.ID); !errors.Is(err, ErrNotPushMonitor) {
		t.Errorf("http monitor: err = %v, want ErrNotPushMonitor", err)
	}
	if _, err := db.ReplacePushToken(ctx, httpMon.ID+1000); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown id: err = %v, want sql.ErrNoRows", err)
	}
}
