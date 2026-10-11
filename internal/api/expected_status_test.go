package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// An expected status the checker cannot parse fails every http check before
// the request is made. Saved, it makes a monitor report an outage of a server
// it never contacted, so every way a monitor gets one refuses it.

// badStatusSpecs are specs checker.ParseStatusMatcher refuses, each with a
// fragment of the message that names what is wrong.
var badStatusSpecs = map[string]string{
	"not a number":  `invalid status "abc"`,
	"out of range":  "status 700 out of range",
	"inverted":      "299-200 is inverted",
	"bad shorthand": `invalid status range start "2xx"`,
	"only commas":   "matches nothing",
}

var badStatusInput = map[string]string{
	"not a number":  "abc",
	"out of range":  "700",
	"inverted":      "299-200",
	"bad shorthand": "2xx-3xx",
	"only commas":   ",",
}

func wantStatusProblem(t *testing.T, code int, body, field, want string) {
	t.Helper()
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", code, body)
	}
	var e errorResponse
	must(t, json.Unmarshal([]byte(body), &e))
	if e.Field != field {
		t.Errorf("field = %q, want %s", e.Field, field)
	}
	if !strings.Contains(e.Error, want) {
		t.Errorf("error = %q, want it to say %s", e.Error, want)
	}
}

func TestCreateMonitorRefusesAnExpectedStatusItCannotParse(t *testing.T) {
	for name, spec := range badStatusInput {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"API","type":"http","target":"https://example.com","expected_status":"`+spec+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "expected_status", badStatusSpecs[name])
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

// The forms a spec may take are still accepted, and an empty one still
// means the default.
func TestCreateMonitorAcceptsTheStatusSpecsTheCheckerReads(t *testing.T) {
	for spec, stored := range map[string]string{
		"":               "200-299",
		"200-299":        "200-299",
		"2xx,301":        "2xx,301",
		"200, 204":       "200, 204",
		" 200-204 , 3XX": " 200-204 , 3XX",
	} {
		srv, db := testServerWithDB(t)
		rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
			`{"name":"API","type":"http","target":"https://example.com","expected_status":"`+spec+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("%q: status = %d, want 201: %s", spec, rec.Code, rec.Body.String())
		}
		mons, err := db.ListMonitors(context.Background())
		must(t, err)
		if len(mons) != 1 || mons[0].ExpectedStatus != stored {
			t.Errorf("%q: stored %+v, want one monitor with %q", spec, mons, stored)
		}
	}
}

func TestPatchMonitorRefusesAnExpectedStatusItCannotParse(t *testing.T) {
	for name, spec := range badStatusInput {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
				IntervalS: 60, TimeoutS: 10, Enabled: true, ExpectedStatus: "200-204"})
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"expected_status":"`+spec+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "expected_status", badStatusSpecs[name])
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if got.ExpectedStatus != "200-204" {
				t.Errorf("a refused patch changed expected_status to %q", got.ExpectedStatus)
			}
		})
	}
}

// Only a spec a request carries is judged. A monitor saved before the rule
// can still be renamed or paused; the edit that sends a spec has to fix it.
func TestPatchMonitorWithoutExpectedStatusIgnoresTheStoredOne(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, ExpectedStatus: "abc"})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"API v2","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"expected_status":"200-299"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("fixing the spec: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// A file is checked twice on its way in: as a create for a new key and as a
// patch for every key. Both paths have to refuse, with the entry's index.
func TestImportRefusesAnExpectedStatusItCannotParse(t *testing.T) {
	for name, spec := range badStatusInput {
		for _, existing := range []bool{false, true} {
			label := name + "/new"
			if existing {
				label = name + "/existing"
			}
			t.Run(label, func(t *testing.T) {
				srv, db := testServerWithDB(t)
				doc := func(spec string) string {
					return "version: 1\nmonitors:\n  - key: ok\n    name: OK\n    type: tcp\n    target: a.example.com:1\n" +
						"  - key: api\n    name: API\n    type: http\n    target: https://example.com\n" +
						"    expected_status: \"" + spec + "\"\n"
				}
				want := 0
				if existing {
					// The first file gives the monitors their keys, so the
					// second is planned as an update of each.
					if code, _, body := importYAML(t, srv, doc("200-204"), false); code != http.StatusOK {
						t.Fatalf("seeding import: status = %d: %s", code, body)
					}
					want = 2
				}
				code, _, body := importYAML(t, srv, doc(spec), false)
				wantStatusProblem(t, code, body, "monitors[1].expected_status", badStatusSpecs[name])
				mons, err := db.ListMonitors(context.Background())
				must(t, err)
				if len(mons) != want {
					t.Fatalf("a refused file left %d monitors, want %d", len(mons), want)
				}
				for _, m := range mons {
					if m.Type == "http" && m.ExpectedStatus != "200-204" {
						t.Errorf("a refused file changed expected_status to %q", m.ExpectedStatus)
					}
				}
			})
		}
	}
}

// A preview runs the check it is given, so an unparseable spec would come
// back as a failed probe. It is refused the way create refuses it, before
// the prober runs.
func TestPreviewRefusesAnExpectedStatusItCannotParse(t *testing.T) {
	for name, spec := range badStatusInput {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)
			rec := preview(t, srv, `{"target":"https://example.com","expected_status":"`+spec+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "expected_status", badStatusSpecs[name])
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times for an unparseable spec, want 0", prober.callCount())
			}
		})
	}
}
