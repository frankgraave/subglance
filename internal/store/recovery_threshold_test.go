package store

import (
	"context"
	"testing"
)

// A monitor created without a recovery threshold gets the schema's 2, whether
// it is built in Go or inserted bare, and the column refuses a zero.
func TestRecoveryThresholdDefaultsToTwo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{Name: "a", Type: "http", Target: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.RecoveryThreshold != 2 {
		t.Errorf("recovery threshold = %d, want 2", got.RecoveryThreshold)
	}

	var bare int
	if err := db.Writer.QueryRowContext(ctx, `
		INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('b', 'http', 'https://b.example', 0, 0)
		RETURNING recovery_threshold`).Scan(&bare); err != nil {
		t.Fatalf("bare insert: %v", err)
	}
	if bare != 2 {
		t.Errorf("bare insert recovery threshold = %d, want 2", bare)
	}

	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET recovery_threshold = 0 WHERE id = ?", m.ID); err == nil {
		t.Error("a recovery threshold of 0 was accepted; 1 is the floor")
	}
}
