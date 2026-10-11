package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A monitor is never run twice at once, so a timeout longer than the gap
// between checks makes the scheduler skip every dispatch that falls inside a
// hanging check. Every way a monitor gets its interval and timeout refuses a
// timeout above 80% of the interval it ends up with.

func TestCreateMonitorRefusesATimeoutThatOutrunsTheInterval(t *testing.T) {
	cases := []struct {
		name, body, field, want string
	}{
		{"both sent", `"interval_s":20,"timeout_s":120`, "timeout_s",
			"timeout_s must be at most 16 for a 20-second interval"},
		{"timeout against the default interval", `"timeout_s":49`, "timeout_s",
			"timeout_s must be at most 48 for a 60-second interval"},
		{"tcp", `"interval_s":30,"timeout_s":25`, "timeout_s",
			"timeout_s must be at most 24 for a 30-second interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			target := `"type":"http","target":"https://example.com"`
			if tc.name == "tcp" {
				target = `"type":"tcp","target":"db.example.com:5432"`
			}
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", `{"name":"API",`+target+`,`+tc.body+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

// The bound itself is accepted, the default timeout fits the shortest
// interval, and a push monitor, which is never dialled,
// is not judged by it.
func TestCreateMonitorAcceptsATimeoutOfEightyPercent(t *testing.T) {
	for _, body := range []string{
		`{"name":"API","type":"http","target":"https://example.com","interval_s":20,"timeout_s":16}`,
		`{"name":"API","type":"http","target":"https://example.com","interval_s":150,"timeout_s":120}`,
		`{"name":"API","type":"http","target":"https://example.com","timeout_s":48}`,
		`{"name":"API","type":"http","target":"https://example.com","interval_s":20}`,
		`{"name":"Job","type":"push","push_interval_s":60,"interval_s":20,"timeout_s":120}`,
	} {
		srv, _ := testServerWithDB(t)
		rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", body)
		if rec.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, want 201: %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestPatchMonitorRefusesATimeoutThatOutrunsTheInterval(t *testing.T) {
	cases := []struct {
		name, body, field, want string
	}{
		{"longer timeout", `{"timeout_s":50}`, "timeout_s",
			"timeout_s must be at most 48 for a 60-second interval"},
		{"shorter interval", `{"interval_s":20}`, "interval_s",
			"interval_s must be at least 38 for a 30-second timeout"},
		{"both", `{"interval_s":30,"timeout_s":30}`, "timeout_s",
			"timeout_s must be at most 24 for a 30-second interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
				IntervalS: 60, TimeoutS: 30, Enabled: true})
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if got.IntervalS != 60 || got.TimeoutS != 30 {
				t.Errorf("a refused patch stored interval %d, timeout %d", got.IntervalS, got.TimeoutS)
			}
		})
	}
}

// Only a request that sends the interval or the timeout is judged. A monitor
// saved before the rule can still be renamed or paused; the edit that touches
// either value has to bring the pair within bounds.
func TestPatchMonitorWithoutTheScheduleIgnoresAStoredOverlap(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 20, TimeoutS: 120, Enabled: true})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"API v2","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"timeout_s":16}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("fixing the timeout: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// A file is checked as a create for a new key and as a patch for every key;
// both refuse, naming the entry.
func TestImportRefusesATimeoutThatOutrunsTheInterval(t *testing.T) {
	doc := func(timeout string) string {
		return "version: 1\nmonitors:\n  - key: ok\n    name: OK\n    type: tcp\n    target: a.example.com:1\n" +
			"  - key: api\n    name: API\n    type: http\n    target: https://example.com\n" +
			"    interval_s: 30\n    timeout_s: " + timeout + "\n"
	}
	for _, existing := range []bool{false, true} {
		label := "new"
		if existing {
			label = "existing"
		}
		t.Run(label, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			want := 0
			if existing {
				if code, _, body := importYAML(t, srv, doc("10"), false); code != http.StatusOK {
					t.Fatalf("seeding import: status = %d: %s", code, body)
				}
				want = 2
			}
			code, _, body := importYAML(t, srv, doc("25"), false)
			wantStatusProblem(t, code, body, "monitors[1].timeout_s",
				"timeout_s must be at most 24 for a 30-second interval")
			mons, err := db.ListMonitors(context.Background())
			must(t, err)
			if len(mons) != want {
				t.Fatalf("a refused file left %d monitors, want %d", len(mons), want)
			}
			for _, m := range mons {
				if m.Type == "http" && m.TimeoutS != 10 {
					t.Errorf("a refused file changed timeout_s to %d", m.TimeoutS)
				}
			}
		})
	}
}
