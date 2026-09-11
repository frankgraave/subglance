package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
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

func TestUpdateMonitorIfUnchanged(t *testing.T) {
	tests := []struct {
		name string
		// accepted derives the versions to defend from the monitor as stored.
		accepted func(m Monitor) []time.Time
		wantErr  error
		wantName string
	}{
		{
			name:     "current version wins",
			accepted: func(m Monitor) []time.Time { return []time.Time{m.UpdatedAt} },
			wantErr:  nil,
			wantName: "after",
		},
		{
			name:     "stale version is refused",
			accepted: func(m Monitor) []time.Time { return []time.Time{m.UpdatedAt.Add(-time.Hour)} },
			wantErr:  ErrVersionConflict,
			wantName: "before",
		},
		{
			name: "any one of several offered versions is enough",
			accepted: func(m Monitor) []time.Time {
				return []time.Time{m.UpdatedAt.Add(-time.Hour), m.UpdatedAt}
			},
			wantErr:  nil,
			wantName: "after",
		},
		{
			name:     "no acceptable version at all",
			accepted: func(m Monitor) []time.Time { return nil },
			wantErr:  ErrVersionConflict,
			wantName: "before",
		},
		{
			name:     "version from the future is refused",
			accepted: func(m Monitor) []time.Time { return []time.Time{m.UpdatedAt.Add(time.Hour)} },
			wantErr:  ErrVersionConflict,
			wantName: "before",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()

			m, err := db.CreateMonitor(ctx, Monitor{
				Name: "before", Type: "http", Target: "https://a.example", Enabled: true,
			})
			if err != nil {
				t.Fatalf("CreateMonitor: %v", err)
			}

			edit := m
			edit.Name = "after"
			_, err = db.UpdateMonitorIfUnchanged(ctx, edit, tc.accepted(m))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			got, err := db.GetMonitor(ctx, m.ID)
			if err != nil {
				t.Fatalf("GetMonitor: %v", err)
			}
			if got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
		})
	}
}

// A conditional write against a monitor that is gone cannot be reported as
// success, and the caller's premise is stale either way.
func TestUpdateMonitorIfUnchangedMissingRow(t *testing.T) {
	db := openTestDB(t)

	_, err := db.UpdateMonitorIfUnchanged(context.Background(), Monitor{
		ID: 4242, Name: "ghost", Type: "http", Target: "https://a.example",
	}, []time.Time{time.Unix(1, 0)})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
}

// Two writers working from the SAME snapshot must still get different stamps.
// This is the case a Go-side `max(now, snapshot+1)` cannot cover: both compute
// their floor from a row that the other has already superseded, so both land on
// the same value — and a client holding that tag can then overwrite an edit it
// never saw. The guarantee has to come from the UPDATE comparing against the
// row as stored.
func TestUpdateMonitorAdvancesAgainstStoredRowNotSnapshot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	created, err := db.CreateMonitor(ctx, Monitor{
		Name: "a", Type: "http", Target: "https://a.example", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// Both writers read the row, as two concurrent PATCH handlers would, and
	// neither sees the other's write.
	first, second := created, created

	first.Name = "first writer"
	afterFirst, err := db.UpdateMonitor(ctx, first)
	if err != nil {
		t.Fatalf("first UpdateMonitor: %v", err)
	}

	second.Name = "second writer"
	afterSecond, err := db.UpdateMonitor(ctx, second)
	if err != nil {
		t.Fatalf("second UpdateMonitor: %v", err)
	}

	if !afterSecond.UpdatedAt.After(afterFirst.UpdatedAt) {
		t.Errorf("second stamp = %d, want strictly after first stamp %d",
			afterSecond.UpdatedAt.Unix(), afterFirst.UpdatedAt.Unix())
	}

	// The returned stamp must be what is actually stored, or the ETag handed
	// to the client describes a version the database does not have.
	stored, err := db.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !stored.UpdatedAt.Equal(afterSecond.UpdatedAt) {
		t.Errorf("stored updated_at = %d, want %d as returned",
			stored.UpdatedAt.Unix(), afterSecond.UpdatedAt.Unix())
	}
}

// updated_at is the version stamp, so it must strictly advance. At second
// resolution two edits within the same second would otherwise leave it equal,
// and a tag read before the first edit would still satisfy the second.
func TestUpdateMonitorAlwaysAdvancesUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "a", Type: "http", Target: "https://a.example", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	prev := m.UpdatedAt
	for i := range 3 {
		m.Name = "edit" + strconv.Itoa(i)
		updated, err := db.UpdateMonitor(ctx, m)
		if err != nil {
			t.Fatalf("UpdateMonitor: %v", err)
		}
		if !updated.UpdatedAt.After(prev) {
			t.Fatalf("updated_at = %v after edit %d, want it after %v",
				updated.UpdatedAt, i, prev)
		}

		stored, err := db.GetMonitor(ctx, m.ID)
		if err != nil {
			t.Fatalf("GetMonitor: %v", err)
		}
		if !stored.UpdatedAt.Equal(updated.UpdatedAt) {
			t.Errorf("stored updated_at = %v, want %v", stored.UpdatedAt, updated.UpdatedAt)
		}

		prev = updated.UpdatedAt
		m = updated
	}
}

func TestSetMonitorEnabledAdvancesUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "a", Type: "http", Target: "https://a.example", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if err := db.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !got.UpdatedAt.After(m.UpdatedAt) {
		t.Errorf("updated_at = %v, want it after %v so a stale ETag cannot survive a pause",
			got.UpdatedAt, m.UpdatedAt)
	}
}
