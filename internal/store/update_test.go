package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestUpdateMonitorWritesEveryMutableColumn(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "before", Type: "http", Target: "https://a.example",
		IntervalS: 60, TimeoutS: 10, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	m.Name = "after"
	m.Type = "tcp"
	m.Target = "b.example:5432"
	m.IntervalS = 300
	m.TimeoutS = 25
	m.Retries = 4
	m.Method = "HEAD"
	m.ExpectedStatus = "200-204"
	m.Keyword = "ok"
	m.KeywordMode = "must_contain"
	m.FollowRedirects = false
	m.Headers = map[string]string{"X-A": "1"}
	m.Body = "payload"
	m.SSLWarnDays = 30
	m.Enabled = false

	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}

	// Compare the whole struct apart from the timestamps: field-by-field
	// assertions are exactly how a forgotten column in the UPDATE slips
	// through unnoticed.
	got.CreatedAt = m.CreatedAt
	got.UpdatedAt = m.UpdatedAt
	if !reflect.DeepEqual(got, m) {
		t.Errorf("stored monitor differs\n got: %+v\nwant: %+v", got, m)
	}
}

func TestUpdateMonitorMissingRow(t *testing.T) {
	db := openTestDB(t)

	_, err := db.UpdateMonitor(context.Background(), Monitor{
		ID: 4242, Name: "ghost", Type: "http", Target: "https://a.example",
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows so the API can answer 404", err)
	}
}

func TestUpdateMonitorBumpsUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "a", Type: "http", Target: "https://a.example", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// Backdate so the bump is observable without sleeping.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET updated_at = 1 WHERE id = ?", m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	m.Name = "b"
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.UpdatedAt.Unix() <= 1 {
		t.Errorf("updated_at = %v, want it bumped on write", got.UpdatedAt)
	}
}
