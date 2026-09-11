package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Embedding heartbeats in the monitor listing is what lets the dashboard be
// drawn from one request. Two things about it are easy to get wrong and
// expensive to notice: the order (the beat bar reads oldest to newest, the
// store returns newest first) and the opt-in (adding the field unconditionally
// would change the response for every existing client).

// listMonitorsBody is the decoded listing response. Heartbeats is a pointer so
// "field absent" stays distinguishable from "field present but empty" — the
// whole point of the opt-in.
type listMonitorsBody struct {
	Monitors []struct {
		ID         int64 `json:"id"`
		Heartbeats *[]struct {
			TS         time.Time `json:"ts"`
			OK         bool      `json:"ok"`
			LatencyMS  int       `json:"latency_ms"`
			StatusCode int       `json:"status_code"`
			Error      string    `json:"error"`
		} `json:"heartbeats"`
	} `json:"monitors"`
}

func decodeListMonitors(t *testing.T, body []byte) listMonitorsBody {
	t.Helper()
	var got listMonitorsBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode listing: %v (body: %s)", err, body)
	}
	return got
}

// seedHeartbeats records n heartbeats one second apart and returns the
// timestamps, oldest first.
func seedHeartbeats(t *testing.T, db *store.DB, monitorID int64, base time.Time, n int) []time.Time {
	t.Helper()

	ts := make([]time.Time, 0, n)
	for i := range n {
		at := base.Add(time.Duration(i) * time.Second).UTC()
		err := db.RecordHeartbeat(t.Context(), store.Heartbeat{
			MonitorID: monitorID, TS: at, OK: true, LatencyMS: 10 + i,
		})
		if err != nil {
			t.Fatalf("RecordHeartbeat %d: %v", i, err)
		}
		ts = append(ts, at)
	}
	return ts
}

func TestListMonitorsEmbedsHeartbeatsOldestFirst(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	quiet := seedMonitor(t, db, store.Monitor{
		Name: "quiet", Type: "http", Target: "https://quiet.example.com", Enabled: true,
	})

	// More beats than requested, so the handler has to pick the newest five
	// and then flip them.
	all := seedHeartbeats(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), 8)
	wantTS := all[len(all)-5:] // newest five, oldest first

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors?heartbeats=5", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got := decodeListMonitors(t, rec.Body.Bytes())
	if len(got.Monitors) != 2 {
		t.Fatalf("got %d monitors, want 2", len(got.Monitors))
	}

	for _, mon := range got.Monitors {
		switch mon.ID {
		case m.ID:
			if mon.Heartbeats == nil {
				t.Fatalf("monitor %d has heartbeats but the field is absent", mon.ID)
			}
			beats := *mon.Heartbeats
			if len(beats) != 5 {
				t.Fatalf("got %d heartbeats, want 5", len(beats))
			}
			for i, hb := range beats {
				if !hb.TS.Equal(wantTS[i]) {
					t.Errorf("heartbeat %d: ts = %s, want %s (oldest first)",
						i, hb.TS.UTC(), wantTS[i])
				}
			}
		case quiet.ID:
			// Never checked: the field stays out rather than shipping [].
			if mon.Heartbeats != nil {
				t.Errorf("monitor %d was never checked but carries %d heartbeats",
					mon.ID, len(*mon.Heartbeats))
			}
		default:
			t.Errorf("unexpected monitor id %d", mon.ID)
		}
	}
}

func TestListMonitorsOmitsHeartbeatsWithoutParameter(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	seedHeartbeats(t, db, m.ID, time.Unix(1_700_000_000, 0).UTC(), 3)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Check the raw bytes too: an existing client must see the response it
	// has always seen, and `omitempty` on a nil slice is what guarantees it.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	monitors, ok := raw["monitors"].([]any)
	if !ok || len(monitors) != 1 {
		t.Fatalf("monitors = %#v, want one entry", raw["monitors"])
	}
	first, ok := monitors[0].(map[string]any)
	if !ok {
		t.Fatalf("monitor entry = %#v, want an object", monitors[0])
	}
	if v, present := first["heartbeats"]; present {
		t.Errorf("heartbeats field present without the query parameter: %#v", v)
	}
}

func TestListMonitorsRejectsInvalidHeartbeatsParameter(t *testing.T) {
	srv, _ := testServerWithDB(t)

	for _, value := range []string{"0", "abc", "-1", "1001"} {
		rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors?heartbeats="+value, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("heartbeats=%s: status = %d, want 400: %s",
				value, rec.Code, rec.Body.String())
			continue
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("heartbeats=%s: decode: %v", value, err)
			continue
		}
		if body.Error != "heartbeats must be between 1 and 1000" {
			t.Errorf("heartbeats=%s: error = %q", value, body.Error)
		}
	}
}
