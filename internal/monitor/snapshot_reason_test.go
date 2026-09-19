package monitor

import (
	"fmt"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Read the stored row, not today's engine state. Reading by column name also
// makes the pre-migration failure an assertion about missing evidence.
func recordedCaptureReasons(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.Reader.QueryContext(t.Context(), "SELECT * FROM heartbeats ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		reason := ""
		for i, name := range columns {
			if name == "response_capture_reason" && values[i] != nil {
				reason = fmt.Sprint(values[i])
			}
		}
		reasons = append(reasons, reason)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return reasons
}

func TestSnapshotReasonDistinguishesDisabledBudgetAndUnknown(t *testing.T) {
	db := testDB(t)
	r := recordingRunner(t, db)
	m := captureMonitorRow(t, db)

	// Missing response is not evidence of an empty body or of flapping.
	missing := failureWithBody(m, "")
	missing.Result.Response = nil
	r.record(missing)
	m.CaptureResponse = false
	missing.Monitor = toCheckerMonitor(m)
	r.record(missing)
	// An unrelated type has no HTTP response-capture setting to explain.
	missing.Monitor.Type = "tcp"
	r.record(missing)
	m.CaptureResponse = true
	for range maxSnapshotsPerIncident + 1 {
		r.record(failureWithBody(m, "failure"))
	}
	r.record(outcomeFor(m, m.Target, true))
	want := []string{"", "disabled", "", "", "", "", "budget", ""}
	got := recordedCaptureReasons(t, db)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("capture reasons = %#v, want %#v", got, want)
	}
}

func TestSnapshotReasonRecordsActualFlappingDecision(t *testing.T) {
	db := testDB(t)
	r := flappingRunner(t, db)
	m := captureMonitorRow(t, db)
	m.Retries = 1
	base := time.Now().Add(-2 * time.Hour)
	fail := failureWithBody(m, "first failure")
	fail.Result.CheckedAt = base
	r.record(fail)
	recovery := outcomeFor(m, m.Target, true)
	recovery.Result.CheckedAt = base.Add(time.Minute)
	r.record(recovery)
	fail.Result.CheckedAt = base.Add(2 * time.Minute)
	r.record(fail)
	if !r.engine.Flapping(m.ID) {
		t.Fatal("fixture did not reach flapping")
	}
	reasons := recordedCaptureReasons(t, db)
	if reasons[0] != "" {
		t.Errorf("first response incorrectly labelled suppressed: %q", reasons[0])
	}
	if reasons[2] != "flapping" {
		t.Fatalf("suppressed beat reason = %q, want flapping", reasons[2])
	}
	if got := storedSnapshots(t, db); got != 1 {
		t.Fatalf("snapshots = %d, want first failure only", got)
	}

	// Only Observe clears the flag. History must not change when it does.
	fail.Result.CheckedAt = base.Add(2 * time.Hour)
	r.record(fail)
	if r.engine.Flapping(m.ID) {
		t.Fatal("engine did not clear settled flapping")
	}
	reasons = recordedCaptureReasons(t, db)
	if reasons[2] != "flapping" {
		t.Fatalf("old suppression changed after recovery: %q", reasons[2])
	}
	if reasons[3] != "" || storedSnapshots(t, db) != 2 {
		t.Fatal("capture did not resume after engine cleared flapping")
	}
}
