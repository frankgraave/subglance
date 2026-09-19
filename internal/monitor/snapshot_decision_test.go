package monitor

import (
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

func TestSnapshotReasonRequiresEvidenceOfTheSuppressionBranch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   checker.Result
		spent    int
		flapping bool
		want     store.CaptureReason
		stored   bool
	}{
		{"missing response while flapping remains unknown", checker.Result{}, 0, true, "", false},
		{"missing response after budget remains unknown", checker.Result{}, maxSnapshotsPerIncident, false, "", false},
		{"success never suppressed", checker.Result{OK: true, Response: &checker.ResponseSnapshot{Body: "success"}}, maxSnapshotsPerIncident, true, "", false},
		{"empty captured body is still a snapshot", checker.Result{Response: &checker.ResponseSnapshot{}}, 0, false, "", true},
		{"flapping takes existing precedence over budget", checker.Result{Response: &checker.ResponseSnapshot{}}, maxSnapshotsPerIncident, true, store.CaptureFlapping, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, reason := snapshotToStore(tc.result, tc.spent, tc.flapping)
			if reason != tc.want || (snap != nil) != tc.stored {
				t.Fatalf("snapshot=%v reason=%q, want stored=%v reason=%q", snap, reason, tc.stored, tc.want)
			}
		})
	}
}
