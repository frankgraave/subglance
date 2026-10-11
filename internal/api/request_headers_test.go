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

// Request headers net/http refuses to send fail every check in the
// transport, before the target is contacted, as a connection error. Saved,
// such a header makes a monitor report an outage of a server it never
// reached, so every way a header gets in refuses one.

// badHeaders are the three ways a header map cannot be sent as it is, each
// with a fragment of the message that names it.
var badHeaders = map[string]struct {
	json, want string
}{
	"space in name":    {`{"X Api":"k"}`, `"X Api"`},
	"newline in value": {`{"X-Api":"a\nb"}`, `"X-Api" has a line break`},
	"same name twice":  {`{"X-Api":"a","x-api":"b"}`, `"X-Api" and "x-api"`},
}

func wantHeaderProblem(t *testing.T, code int, body, want string) {
	t.Helper()
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", code, body)
	}
	var e errorResponse
	must(t, json.Unmarshal([]byte(body), &e))
	if e.Field != "headers" && !strings.HasSuffix(e.Field, ".headers") {
		t.Errorf("field = %q, want headers", e.Field)
	}
	if !strings.Contains(e.Error, want) {
		t.Errorf("error = %q, want it to name %s", e.Error, want)
	}
}

func TestCreateMonitorRefusesHeadersThatCannotBeSent(t *testing.T) {
	for name, tc := range badHeaders {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"API","type":"http","target":"https://example.com","headers":`+tc.json+`}`)
			wantHeaderProblem(t, rec.Code, rec.Body.String(), tc.want)
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
	srv, _ := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"API","type":"http","target":"https://example.com","headers":{"Authorization":"Bearer x","X-Tab":"a\tb"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid headers: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchMonitorRefusesHeadersThatCannotBeSent(t *testing.T) {
	for name, tc := range badHeaders {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
				IntervalS: 60, TimeoutS: 10, Enabled: true, Headers: map[string]string{"X-Api": "k"}})
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"headers":`+tc.json+`}`)
			wantHeaderProblem(t, rec.Code, rec.Body.String(), tc.want)
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if len(got.Headers) != 1 || got.Headers["X-Api"] != "k" {
				t.Errorf("a refused patch changed the headers to %v", got.Headers)
			}
		})
	}
}

// Only headers a request carries are judged. A monitor saved before the
// rule, with a header the rule refuses, can still be renamed or paused; the
// edit that sends its headers is the one that has to fix them.
func TestPatchMonitorWithoutHeadersIgnoresStoredOnes(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, Headers: map[string]string{"X Api": "k"}})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"API v2","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"headers":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing the headers: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestImportRefusesHeadersThatCannotBeSent(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"space in name":      {`{"X Api": "k"}`, `"X Api"`},
		"newline in value":   {`{"X-Api": "a\nb"}`, `"X-Api" has a line break`},
		"same name twice":    {`{"X-Api": "a", "x-api": "b"}`, `"X-Api" and "x-api"`},
		"withheld, bad name": {`{"X Api": "<fill in after import>"}`, `"X Api"`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			doc := "version: 1\nmonitors:\n  - key: ok\n    name: OK\n    type: tcp\n    target: a.example.com:1\n" +
				"  - key: api\n    name: API\n    type: http\n    target: https://example.com\n    headers: " + tc.yaml + "\n"
			code, _, body := importYAML(t, srv, doc, false)
			wantHeaderProblem(t, code, body, tc.want)
			var e errorResponse
			must(t, json.Unmarshal([]byte(body), &e))
			if e.Field != "monitors[1].headers" {
				t.Errorf("field = %q, want monitors[1].headers", e.Field)
			}
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused file wrote %d monitors", len(mons))
			}
		})
	}
}

// A preview runs the check it is given, so an unsendable header would come
// back as a failed probe of a server that was never contacted. It is refused
// the way create refuses it, before the prober runs.
func TestPreviewRefusesHeadersThatCannotBeSent(t *testing.T) {
	for name, tc := range badHeaders {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)
			rec := preview(t, srv, `{"target":"https://example.com","headers":`+tc.json+`}`)
			wantHeaderProblem(t, rec.Code, rec.Body.String(), tc.want)
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times for unsendable headers, want 0", prober.callCount())
			}
		})
	}
}
