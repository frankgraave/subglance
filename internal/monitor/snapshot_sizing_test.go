package monitor

// This opt-in study measures synthetic raw-history files, not production
// traffic or ingestion throughput. See docs/response-snapshot-sizing.md.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

type snapshotStudyCase struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Monitors    int    `json:"monitors"`
	Count       int    `json:"snapshot_allowance"`
	BodyBytes   int    `json:"body_bytes"`
	HeaderBytes int    `json:"header_value_bytes"`
}

type snapshotStudyResult struct {
	snapshotStudyCase
	Heartbeats          int64 `json:"heartbeats"`
	Snapshots           int64 `json:"snapshots"`
	PayloadBytes        int64 `json:"payload_bytes"`
	DatabaseBytes       int64 `json:"database_bytes"`
	WALBytes            int64 `json:"wal_bytes"`
	SHMBytes            int64 `json:"shm_bytes"`
	AllocatedBytes      int64 `json:"allocated_database_bytes"`
	AfterRetentionBytes int64 `json:"after_retention_database_bytes,omitempty"`
	RemainingSnapshots  int64 `json:"remaining_snapshots,omitempty"`
}

func TestSyntheticSnapshotSizing(t *testing.T) {
	report := os.Getenv("SUBGLANCE_SNAPSHOT_STUDY")
	if report == "" {
		t.Skip("opt-in disk study: set SUBGLANCE_SNAPSHOT_STUDY to a JSON output file")
	}
	cases := []snapshotStudyCase{
		{"healthy", "healthy", 200, 3, 2048, 64},
		{"daily-1", "daily", 200, 1, 2048, 64},
		{"daily-3", "daily", 200, 3, 2048, 64},
		{"daily-5", "daily", 200, 5, 2048, 64},
		{"daily-1024", "daily", 200, 3, 1024, 64},
		{"daily-4096", "daily", 200, 3, 4096, 64},
		{"continuous", "continuous", 200, 3, 2048, 64},
		{"flapping", "flapping", 200, 3, 2048, 64},
		// Smaller populations keep the opt-in study below a modest scratch budget.
		// Results are actual files for ten monitors, never relabelled as 200.
		{"settled-every-15m", "settled", 10, 3, 2048, 64},
		{"unconfirmed-alternating", "unconfirmed", 10, 3, 2048, 64},
		{"daily-large-header", "daily", 200, 3, 2048, 16384},
	}
	results := make([]snapshotStudyResult, 0, len(cases))
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			result := measureSnapshotStudy(t, c)
			results = append(results, result)
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(string(data))
		})
	}
	if t.Failed() {
		return
	}
	data, err := json.MarshalIndent(struct {
		Synthetic       bool                  `json:"synthetic"`
		Days            int                   `json:"days"`
		IntervalSeconds int                   `json:"interval_seconds"`
		Results         []snapshotStudyResult `json:"results"`
	}{true, 7, 60, results}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func measureSnapshotStudy(t *testing.T, c snapshotStudyCase) snapshotStudyResult {
	t.Helper()
	ctx := context.Background()
	// Remove each case on return, rather than keeping all large files until the
	// parent test exits. The SQLite driver and migrations are the product's own.
	dir, err := os.MkdirTemp("", "subglance-snapshot-study-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "study.db")
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.EnsureIncrementalVacuum(ctx); err != nil {
		t.Fatal(err)
	}
	monitors := make([]store.Monitor, c.Monitors)
	for i := range monitors {
		monitors[i], err = db.CreateMonitor(ctx, store.Monitor{Name: fmt.Sprintf("synthetic-%d", i), Type: "http", Target: "https://synthetic.invalid", Enabled: true, CaptureResponse: true, IntervalS: 60, Retries: 2})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Persist one actual week, then replicate only its raw rows to the other
	// monitors. This excludes incidents, notifications and scheduler timings.
	// Candidate allowances/sizes are experiments, not runtime configuration.
	engine := state.New(state.Options{})
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for minute := range 7 * 24 * 60 {
		ok := true
		switch c.Pattern {
		case "healthy":
		case "daily":
			ok = minute%1440 >= 5
		case "continuous":
			ok = false
		case "flapping":
			ok = minute%4 >= 2
		case "settled":
			ok = minute%15 >= 3
		case "unconfirmed":
			ok = minute%2 != 0
		default:
			t.Fatalf("unknown study pattern %q", c.Pattern)
		}
		at := start.Add(time.Duration(minute) * time.Minute)
		tr := engine.Observe(state.Observation{MonitorID: monitors[0].ID, At: at, OK: ok, FailureThreshold: 2})
		res := checker.Result{OK: ok}
		hb := store.Heartbeat{MonitorID: monitors[0].ID, TS: at, OK: ok, LatencyMS: 5, StatusCode: 200}
		if !ok {
			hb.StatusCode, hb.Error = 503, "status 503, expected 200-299"
			res.Response = &checker.ResponseSnapshot{Body: strings.Repeat("b", c.BodyBytes), Headers: map[string]string{"X-Request-Id": strings.Repeat("r", c.HeaderBytes)}}
		}
		snap, reason := snapshotToStore(res, tr.SnapshotsSpent, tr.Flapping)
		if c.Count != maxSnapshotsPerIncident {
			// Only the allowance varies. Use the real selection function with a
			// counter offset so all its success/flapping/reason rules still apply.
			snap, reason = snapshotToStore(res, tr.SnapshotsSpent+maxSnapshotsPerIncident-c.Count, tr.Flapping)
		}
		hb.Response = snap
		if err := db.RecordHeartbeatWithCaptureReason(ctx, hb, reason); err != nil {
			t.Fatal(err)
		}
		if snap != nil {
			engine.SpendSnapshot(monitors[0].ID)
		}
	}
	// One transaction per cloned monitor bounds transient WAL growth. Bodies
	// remain in the separate WITHOUT ROWID response table; no compression.
	for _, m := range monitors[1:] {
		tx, err := db.Writer.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms, status_code, error, response_capture_reason)
   SELECT ?, ts, ok, latency_ms, status_code, error, response_capture_reason FROM heartbeats WHERE monitor_id = ? ORDER BY id`, m.ID, monitors[0].ID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO heartbeat_responses (heartbeat_id, body, headers_json, truncated)
    SELECT copy.id, r.body, r.headers_json, r.truncated FROM heartbeat_responses r
    JOIN heartbeats original ON original.id = r.heartbeat_id
    JOIN heartbeats copy ON copy.ts = original.ts AND copy.monitor_id = ?
    WHERE original.monitor_id = ?`, m.ID, monitors[0].ID)
		}
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	result := snapshotStudyResult{snapshotStudyCase: c}
	for query, target := range map[string]*int64{
		"SELECT count(*) FROM heartbeats":          &result.Heartbeats,
		"SELECT count(*) FROM heartbeat_responses": &result.Snapshots,
		"SELECT coalesce(sum(length(CAST(body AS BLOB)) + coalesce(length(CAST(headers_json AS BLOB)), 0)), 0) FROM heartbeat_responses": &result.PayloadBytes,
	} {
		if err := db.Reader.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if result.Heartbeats != int64(c.Monitors*7*24*60) {
		t.Fatalf("incomplete fixture: %d heartbeats", result.Heartbeats)
	}
	checkpointStudy(t, db)
	result.DatabaseBytes = studyFileSize(t, path)
	result.WALBytes = studyFileSize(t, path+"-wal")
	result.SHMBytes = studyFileSize(t, path+"-shm")
	var pageCount, pageSize, freePages int64
	for pragma, target := range map[string]*int64{"page_count": &pageCount, "page_size": &pageSize, "freelist_count": &freePages} {
		if err := db.Reader.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	result.AllocatedBytes = (pageCount - freePages) * pageSize
	if c.Name == "daily-3" {
		// Dates are fixed in the past: actual production retention must delete all
		// study raw rows and their snapshots, leaving hourly aggregates behind.
		if _, err := db.ApplyRetention(ctx, store.RetentionPolicy{Raw: store.DefaultRawRetention}); err != nil {
			t.Fatal(err)
		}
		checkpointStudy(t, db)
		result.AfterRetentionBytes = studyFileSize(t, path)
		if err := db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM heartbeat_responses").Scan(&result.RemainingSnapshots); err != nil {
			t.Fatal(err)
		}
		if result.RemainingSnapshots != 0 {
			t.Fatalf("retention left %d snapshots", result.RemainingSnapshots)
		}
		if result.AfterRetentionBytes >= result.DatabaseBytes {
			t.Fatal("retention did not shrink synthetic file")
		}
	}
	return result
}

func checkpointStudy(t *testing.T, db *store.DB) {
	t.Helper()
	var busy, log, checkpointed int
	if err := db.Writer.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		t.Fatal(err)
	}
	if busy != 0 {
		t.Fatal("measurement requires a completed checkpoint")
	}
}

func studyFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
