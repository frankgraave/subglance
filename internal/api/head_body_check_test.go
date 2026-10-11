package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// A HEAD response has no body. Saved with a keyword or a JSON assertion, an
// http monitor either fails every check against a healthy server
// (must_contain, an assertion) or can never fail at all (must_not_contain),
// so every way a monitor gets one refuses the combination, on method.

// headBodyChecks are the body checks a HEAD monitor cannot perform, as JSON
// fields of a monitor, each with a fragment of the message that says why.
var headBodyChecks = map[string]struct{ fields, want string }{
	"keyword must be present": {`"keyword":"ok","keyword_mode":"must_contain"`, "every check would fail"},
	"keyword must be absent":  {`"keyword":"error","keyword_mode":"must_not_contain"`, "could never fail"},
	"json assertion": {`"json_assertion":{"path":"status","operator":"equals","expected":"ok"}`,
		"JSON assertion has no document"},
}

func TestCreateMonitorRefusesAHeadMonitorThatChecksTheBody(t *testing.T) {
	for name, tc := range headBodyChecks {
		for _, method := range []string{"HEAD", "head"} {
			t.Run(name+"/"+method, func(t *testing.T) {
				srv, db := testServerWithDB(t)
				rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
					`{"name":"API","type":"http","target":"https://example.com","method":"`+method+`",`+tc.fields+`}`)
				wantStatusProblem(t, rec.Code, rec.Body.String(), "method", tc.want)
				if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
					t.Errorf("a refused create stored %d monitors", len(mons))
				}
			})
		}
	}
}

// HEAD on its own is a fine check of a server that answers, and a keyword
// left on "ignore" reads nothing; GET reads the body the checks need.
func TestCreateMonitorAcceptsHeadWithoutABodyCheck(t *testing.T) {
	for name, body := range map[string]string{
		"head alone":          `"method":"HEAD"`,
		"keyword ignored":     `"method":"HEAD","keyword":"ok","keyword_mode":"absent_ok"`,
		"mode without word":   `"method":"HEAD","keyword_mode":"must_contain"`,
		"get with a keyword":  `"method":"GET","keyword":"ok","keyword_mode":"must_contain"`,
		"get with assertion":  `"method":"GET",` + headBodyChecks["json assertion"].fields,
		"default with a word": `"keyword":"ok","keyword_mode":"must_not_contain"`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"API","type":"http","target":"https://example.com",`+body+`}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Either half may arrive alone: a keyword sent to a HEAD monitor, or HEAD
// sent to a monitor with a keyword. Both are judged on the merged monitor.
func TestPatchMonitorRefusesAHeadMonitorThatChecksTheBody(t *testing.T) {
	assertion := &store.JSONAssertion{Path: "status", Operator: "equals", Expected: `"ok"`}
	cases := map[string]struct {
		seed store.Monitor
		body string
		want string
	}{
		"keyword onto head": {store.Monitor{Method: "HEAD"},
			`{"keyword":"ok","keyword_mode":"must_contain"}`, "every check would fail"},
		"mode onto head with a word": {store.Monitor{Method: "HEAD", Keyword: "error", KeywordMode: "absent_ok"},
			`{"keyword_mode":"must_not_contain"}`, "could never fail"},
		"head onto keyword": {store.Monitor{Method: "GET", Keyword: "ok", KeywordMode: "must_contain"},
			`{"method":"HEAD"}`, "every check would fail"},
		"assertion onto head": {store.Monitor{Method: "HEAD"},
			`{` + headBodyChecks["json assertion"].fields + `}`, "JSON assertion has no document"},
		"head onto assertion": {store.Monitor{Method: "GET", JSONAssertion: assertion},
			`{"method":"head"}`, "JSON assertion has no document"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			seed := tc.seed
			seed.Name, seed.Type, seed.Target = "API", "http", "https://example.com"
			seed.IntervalS, seed.TimeoutS, seed.Enabled = 60, 10, true
			m := seedMonitor(t, db, seed)
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "method", tc.want)
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if got.Method != seed.Method || got.Keyword != seed.Keyword || got.KeywordMode != m.KeywordMode ||
				(got.JSONAssertion == nil) != (seed.JSONAssertion == nil) {
				t.Errorf("a refused patch changed the monitor: %+v", got)
			}
		})
	}
}

// Only an edit that sends one of the settings is judged. A HEAD monitor saved
// with a keyword before the rule can still be renamed or paused, and sending
// GET alone repairs it.
func TestPatchMonitorWithoutTheSettingsIgnoresAStoredHeadBodyCheck(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, Method: "HEAD", Keyword: "ok", KeywordMode: "must_contain"})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"API v2","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"method":"GET"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switching to GET: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// A file is checked as a create for a new key and as a patch for every key;
// both refuse, with the entry's index.
func TestImportRefusesAHeadMonitorThatChecksTheBody(t *testing.T) {
	for _, existing := range []bool{false, true} {
		label := "new"
		if existing {
			label = "existing"
		}
		t.Run(label, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			doc := func(method string) string {
				return "version: 1\nmonitors:\n  - key: ok\n    name: OK\n    type: tcp\n    target: a.example.com:1\n" +
					"  - key: api\n    name: API\n    type: http\n    target: https://example.com\n" +
					"    method: " + method + "\n    keyword: ok\n    keyword_mode: must_contain\n"
			}
			want := 0
			if existing {
				if code, _, body := importYAML(t, srv, doc("GET"), false); code != http.StatusOK {
					t.Fatalf("seeding import: status = %d: %s", code, body)
				}
				want = 2
			}
			code, _, body := importYAML(t, srv, doc("HEAD"), false)
			wantStatusProblem(t, code, body, "monitors[1].method", "every check would fail")
			mons, err := db.ListMonitors(context.Background())
			must(t, err)
			if len(mons) != want {
				t.Fatalf("a refused file left %d monitors, want %d", len(mons), want)
			}
			for _, m := range mons {
				if m.Type == "http" && m.Method != "GET" {
					t.Errorf("a refused file changed the method to %q", m.Method)
				}
			}
		})
	}
}

// A preview runs the check it is given, so it would come back as a failed
// probe, or a green one that means nothing. It is refused before the prober
// runs.
func TestPreviewRefusesAHeadMonitorThatChecksTheBody(t *testing.T) {
	for name, tc := range headBodyChecks {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)
			rec := preview(t, srv, `{"target":"https://example.com","method":"HEAD",`+tc.fields+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "method", tc.want)
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times, want 0", prober.callCount())
			}
		})
	}
}
