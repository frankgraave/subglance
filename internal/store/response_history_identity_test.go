package store

import (
	"testing"
	"time"
)

func TestResponseHistorySameSecondNewestFirst(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "same-second")
	ts := time.Unix(1_700_000_000, 0)
	for range 3 {
		if err := db.RecordHeartbeat(t.Context(), Heartbeat{MonitorID: m.ID, TS: ts, Response: &ResponseSnapshot{Body: "identical failure"}}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListHeartbeats(t.Context(), m.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d", len(rows))
	}
	var latestID int64
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT MAX(id) FROM heartbeats WHERE monitor_id = ?", m.ID).Scan(&latestID); err != nil {
		t.Fatal(err)
	}
	if rows[0].ID != latestID || rows[1].ID != latestID-1 {
		t.Fatalf("same-second page must keep newest identities first: got %d, %d; latest %d", rows[0].ID, rows[1].ID, latestID)
	}
	for _, row := range rows {
		if !row.TS.Equal(ts) || row.Response == nil || row.Response.Body != "identical failure" {
			t.Fatalf("lost timestamp or snapshot: %+v", row)
		}
	}
}
