package store

import (
	"fmt"
	"testing"
	"time"
)

func TestCaptureReasonsStayWithTheirHeartbeatAndLegacyIsUnknown(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "history")
	other := newTestMonitor(t, db, "other")
	for i, reason := range []CaptureReason{"", CaptureDisabled, CaptureFlapping, CaptureBudget} {
		if err := db.RecordHeartbeatWithCaptureReason(t.Context(), Heartbeat{MonitorID: m.ID, TS: time.Unix(int64(i), 0)}, reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordHeartbeatWithCaptureReason(t.Context(), Heartbeat{MonitorID: other.ID, TS: time.Unix(99, 0)}, CaptureFlapping); err != nil {
		t.Fatal(err)
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	reasons, err := db.HeartbeatCaptureReasons(t.Context(), hbs)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []CaptureReason{CaptureBudget, CaptureFlapping, CaptureDisabled, ""} {
		if got := reasons[hbs[i].ID]; got != want {
			t.Errorf("beat %d reason = %q, want %q", hbs[i].ID, got, want)
		}
	}
	if len(reasons) != 3 {
		t.Fatalf("reasons = %v, want only this page's three known decisions", reasons)
	}
	if err := db.DeleteMonitor(t.Context(), m.ID); err != nil {
		t.Fatal(err)
	}
	reasons, err = db.HeartbeatCaptureReasons(t.Context(), hbs)
	if err != nil || len(reasons) != 0 {
		t.Fatalf("deleted history left capture reasons: %v, %v", reasons, err)
	}
}

func TestCaptureReasonCannotContradictASnapshotOrSuccessfulCheck(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "history")
	for _, hb := range []Heartbeat{
		{MonitorID: m.ID, TS: time.Now(), OK: true},
		{MonitorID: m.ID, TS: time.Now(), Response: &ResponseSnapshot{Body: "captured"}},
	} {
		if err := db.RecordHeartbeatWithCaptureReason(t.Context(), hb, CaptureFlapping); err == nil {
			t.Fatal("accepted contradictory capture reason")
		}
	}
	if err := db.RecordHeartbeatWithCaptureReason(t.Context(), Heartbeat{MonitorID: m.ID, TS: time.Now()}, "invented"); err == nil {
		t.Fatal("accepted unrecognised capture reason")
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil || len(hbs) != 0 {
		t.Fatalf("invalid capture decision partly stored: %v, %v", hbs, err)
	}
}

func TestCaptureReasonWriteFailureDoesNotPartlyStoreHeartbeat(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "history")
	_, err := db.Writer.ExecContext(t.Context(), `CREATE TRIGGER reject_reason BEFORE INSERT ON heartbeats
		WHEN NEW.response_capture_reason IS NOT NULL BEGIN SELECT RAISE(ABORT, 'reason rejected'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordHeartbeatWithCaptureReason(t.Context(), Heartbeat{MonitorID: m.ID, TS: time.Now()}, CaptureFlapping); err == nil {
		t.Fatal("reason failure was swallowed")
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil || len(hbs) != 0 {
		t.Fatalf("partially committed failed write: %v, %v", hbs, err)
	}
}

func TestCaptureReasonMigrationLeavesOldRowsUnknown(t *testing.T) {
	db := openTestDB(t)
	m := newTestMonitor(t, db, "old history")
	// Exercise the new migration against the pre-column schema, not just a
	// brand new empty database. The name ledger supports the missing 0010.
	if _, err := db.Writer.ExecContext(t.Context(), "ALTER TABLE heartbeats DROP COLUMN response_capture_reason"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE name = '0011_heartbeat_capture_reason.sql'"); err != nil {
		t.Fatal(err)
	}
	failedBeat(t, db, m.ID, time.Now(), nil)
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var reason any
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT response_capture_reason FROM heartbeats").Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != nil {
		t.Fatal(fmt.Sprintf("migration invented historical evidence: %v", reason))
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
}
