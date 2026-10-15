package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// A hostname the resolver refuses without asking a server used to be saved,
// and every check after that failed as "host not found" — an outage report
// about a host nobody has, caused by a typo nobody was told about. Each way a
// target reaches the store refuses it now, on the target field.

func TestCreateMonitorRefusesAnUnresolvableHostname(t *testing.T) {
	for _, tc := range []struct{ typ, target, want string }{
		{"http", "https://exa..mple.com/health", "empty label"},
		{"tcp", "db-.example.com:5432", "ends with a hyphen"},
		{"ssl", "exa!mple.com", `contains "!"`},
		{"ping", "999.1.1.1", "not a valid IP address"},
		{"tcp", "b\u00fccher.de:443", "xn--"},
	} {
		t.Run(tc.typ+" "+tc.target, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"A","type":"`+tc.typ+`","target":"`+tc.target+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "target", tc.want)
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

func TestPatchMonitorRefusesAnUnresolvableHostname(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "DB", Type: "tcp", Target: "db.example.com:5432"})
	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10)

	rec := patch(t, srv, path, `{"target":"db..example.com:5432"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "target", "empty label")
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Target != "db.example.com:5432" {
		t.Errorf("a refused patch changed the target to %q", got.Target)
	}
}

// A monitor saved before the rule existed keeps working as an edit target:
// a patch that leaves the target and type alone does not re-judge them, so
// renaming it or pausing it is not refused over a hostname the user did not
// touch. Fixing the hostname is what is asked of them, on the target field.
func TestPatchKeepsAnOlderMonitorWithAnUnresolvableHostnameEditable(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "Old", Type: "tcp", Target: "exa..mple.com:443"})
	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10)

	if rec := patch(t, srv, path, `{"name":"Renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := patch(t, srv, path, `{"target":"example.com:443"}`); rec.Code != http.StatusOK {
		t.Fatalf("fixing the target = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestPreviewRefusesAnUnresolvableHostname(t *testing.T) {
	for _, body := range []string{
		`{"target":"exa..mple.com"}`, // no type: read as http
		`{"type":"ping","target":"-x.example.com"}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)

			rec := preview(t, srv, body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"target"`) {
				t.Fatalf("status = %d (body %s), want 400 on target", rec.Code, rec.Body.String())
			}
			if prober.callCount() != 0 {
				t.Errorf("prober called %d times for a refused target", prober.callCount())
			}
		})
	}
}

// An http target's port is held to the range a tcp target's already is:
// net/url takes any digits as a port, and the dial refuses the rest on every
// check, which reads as an outage of a host that may be fine.
func TestHTTPMonitorRefusesAPortOutOfRange(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"A","type":"http","target":"https://example.com:99999/health"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "target", "port 99999 is out of range")

	m := seedMonitor(t, db, store.Monitor{Name: "Web", Type: "http", Target: "https://example.com/"})
	rec = patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10), `{"target":"https://example.com:0/"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "target", "port 0 is out of range")
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Target != "https://example.com/" {
		t.Errorf("a refused patch changed the target to %q", got.Target)
	}
}

func TestImportRefusesAnUnresolvableHostname(t *testing.T) {
	srv, _ := testServerWithDB(t)
	doc := "version: 1\nmonitors:\n  - key: a\n    name: A\n    type: ssl\n    target: exa!mple.com\n"
	code, _, body := importYAML(t, srv, doc, true)
	if code != http.StatusBadRequest || !strings.Contains(body, `"field":"monitors[0].target"`) {
		t.Errorf("import = %d %s, want 400 at monitors[0].target", code, body)
	}
}
