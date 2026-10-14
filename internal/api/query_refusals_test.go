package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/frankgraave/subglance/internal/store"
)

// queryRefusal is one bad value for one documented query parameter, and the
// parameter the refusal has to name.
type queryRefusal struct {
	op    string // the spec's "METHOD /path" for the operation
	query string // appended to the concrete path, leading "?" included
	field string // the parameter the refusal must name
}

// queryRefusals covers every query parameter the server can refuse, with each
// distinct refusal sentence at least once: a parameter refused by two checks
// (a window that does not parse and one out of range) has a case for each.
var queryRefusals = []queryRefusal{
	{"GET /api/v1/monitors", "?heartbeats=0", "heartbeats"},
	{"GET /api/v1/monitors", "?heartbeats=many", "heartbeats"},
	{"GET /api/v1/monitors/{id}/heartbeats", "?limit=1001", "limit"},
	{"GET /api/v1/monitors/{id}/heartbeats", "?limit=abc", "limit"},
	{"GET /api/v1/monitors/{id}/uptime", "?window=soon", "window"},
	{"GET /api/v1/monitors/{id}/uptime", "?window=1s", "window"},
	{"GET /api/v1/monitors/{id}/latency", "?window=12h", "window"},
	{"GET /api/v1/monitors/{id}/incidents", "?limit=501", "limit"},
	{"GET /api/v1/incidents/resolved", "?days=0", "days"},
	{"GET /api/v1/incidents/resolved", "?days=731", "days"},
	{"GET /api/v1/incidents/resolved", "?limit=201", "limit"},
	{"GET /api/v1/incidents/resolved", "?cursor=abc", "cursor"},
	// Well formed, but pins a lower bound in 1970: refused by the cap on
	// how far back a walk may reach, a different check from the parse.
	{"GET /api/v1/incidents/resolved", "?cursor=1.1.1.30", "cursor"},
	{"GET /api/v1/settings/retention/preview", "?raw_seconds=abc", "raw_seconds"},
	// In range for the parameter, refused by the retention policy itself.
	{"GET /api/v1/settings/retention/preview", "?raw_seconds=60", "raw_seconds"},
	{"GET /api/v1/settings/retention/preview", "?rollup_seconds=-1", "rollup_seconds"},
	{"GET /api/v1/push/{token}", "?status=maybe", "status"},
	{"POST /api/v1/push/{token}", "?status=maybe", "status"},
	{"POST /api/v1/config/import", "?dry_run=perhaps", "dry_run"},
}

// queryNeverRefused are the documented query parameters no value of which is
// refused, with the reason. A parameter belongs here only if that is a choice.
var queryNeverRefused = map[string]string{
	"GET /api/v1/push/{token} msg":     "a long message is truncated, not refused",
	"POST /api/v1/push/{token} msg":    "a long message is truncated, not refused",
	"GET /api/v1/stream last_event_id": "an unreadable id starts a fresh stream, as a missing one does",
}

// TestQueryParameterRefusalsNameTheParameter holds every query-parameter
// refusal to the error shape's promise: a refusal about one input names it in
// `field`, so a client places the message without matching on its wording.
func TestQueryParameterRefusalsNameTheParameter(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	srv.WithPushRecorder(&fakePusher{})

	for _, tc := range queryRefusals {
		t.Run(tc.op+tc.query, func(t *testing.T) {
			method, path, _ := strings.Cut(tc.op, " ")
			path = strings.Replace(path, "{id}", itoa(m.ID), 1)
			if strings.Contains(path, "{token}") {
				// A fresh monitor per case: one push per monitor per few
				// seconds is admitted, refused or not.
				created := createPushMonitor(t, srv, pushMonitorBody)
				path = created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
			}

			var body *strings.Reader
			if method == http.MethodPost {
				body = strings.NewReader("version: 1\n")
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(method, path+tc.query, body)
			rec := httptest.NewRecorder()
			if strings.HasPrefix(path, "/api/v1/push/") {
				srv.Handler().ServeHTTP(rec, req) // the token is the credential
			} else {
				authedHandler(srv).ServeHTTP(rec, req)
			}

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			var e errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatalf("decode %s: %v", rec.Body.String(), err)
			}
			if e.Field != tc.field {
				t.Errorf("field = %q, want %q (error %q)", e.Field, tc.field, e.Error)
			}
			if e.Error == "" {
				t.Error("refusal has no sentence")
			}
		})
	}
}

// TestEveryQueryParameterHasARefusalCase walks the query parameters
// docs/openapi.yaml documents and fails on one that has neither a refusal case
// above nor a reason it is never refused, so a new parameter cannot ship with a
// refusal that names nothing. It also fails on a case or a reason for a
// parameter the document no longer has.
func TestEveryQueryParameterHasARefusalCase(t *testing.T) {
	spec := loadSpec(t)
	documented := documentedQueryParameters(t, spec)
	if len(documented) == 0 {
		t.Fatal("found no query parameters in the spec; the walk is broken")
	}

	covered := make(map[string]bool)
	for _, tc := range queryRefusals {
		covered[tc.op+" "+tc.field] = true
	}
	for key := range queryNeverRefused {
		if covered[key] {
			t.Errorf("%s has a refusal case and is also listed as never refused", key)
		}
		covered[key] = true
	}

	var missing, stale []string
	for key := range documented {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	for key := range covered {
		if !documented[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, key := range missing {
		t.Errorf("query parameter %s is documented but has no refusal case "+
			"(add one to queryRefusals, or say in queryNeverRefused why it is never refused)", key)
	}
	for _, key := range stale {
		t.Errorf("%s is covered here but %s documents no such query parameter", key, specPath)
	}
}

// specParameter is one entry of a `parameters` list, inline or by reference.
type specParameter struct {
	Ref  string `yaml:"$ref"`
	Name string `yaml:"name"`
	In   string `yaml:"in"`
}

// documentedQueryParameters returns "METHOD /path name" for every query
// parameter in the spec, counting path-level lists and resolving references
// into components.parameters.
func documentedQueryParameters(t *testing.T, spec *openAPISpec) map[string]bool {
	t.Helper()

	var doc struct {
		Components struct {
			Parameters map[string]specParameter `yaml:"parameters"`
		} `yaml:"components"`
	}
	raw, err := os.ReadFile(filepath.Clean(specPath))
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	resolve := func(p specParameter) specParameter {
		if p.Ref == "" {
			return p
		}
		const prefix = "#/components/parameters/"
		target, ok := doc.Components.Parameters[strings.TrimPrefix(p.Ref, prefix)]
		if !strings.HasPrefix(p.Ref, prefix) || !ok {
			t.Fatalf("unresolvable parameter reference %q", p.Ref)
		}
		return target
	}

	out := make(map[string]bool)
	for path, item := range spec.Paths {
		var shared []specParameter
		if node, ok := item["parameters"]; ok {
			if err := node.Decode(&shared); err != nil {
				t.Fatalf("decode %s parameters: %v", path, err)
			}
		}
		for method, node := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			var op struct {
				Parameters []specParameter `yaml:"parameters"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
			for _, p := range append(append([]specParameter(nil), shared...), op.Parameters...) {
				if p = resolve(p); p.In == "query" {
					out[strings.ToUpper(method)+" "+path+" "+p.Name] = true
				}
			}
		}
	}
	return out
}
