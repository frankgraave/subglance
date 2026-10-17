package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A monitor's free-text settings are refused past their ceilings, counted as
// characters, on every path that saves or previews one. Measured before the
// limits: a 200,000-character target, keyword, body, header value, status
// list or JSON path was saved with a 201.

// limitCase is one setting at its ceiling and one past it, as the fields of a
// create request for an http monitor.
type limitCase struct {
	field     string
	at, over  map[string]any
	wantWords string
}

func monitorLimitCases() map[string]limitCase {
	headers := func(n int) map[string]any {
		h := map[string]any{}
		for i := range n {
			h["X-H"+itoa(int64(i))] = "v"
		}
		return h
	}
	// "é" is two bytes, so a target of 2048 characters is 4,076 bytes and
	// fits: the ceiling counts what a person reads.
	target := func(n int) string {
		return "https://example.com/" + strings.Repeat("é", n-len("https://example.com/"))
	}
	// "200," 512 times is 2048 characters; the trailing comma is an empty
	// part, which the parser skips. Past it, a list that still parses, so
	// only the limit can refuse it.
	status := func(extra string) string { return strings.Repeat("200,", 512) + extra }
	assertion := func(path, expected string) map[string]any {
		return map[string]any{"path": path, "operator": "equals", "expected": json.RawMessage(expected)}
	}
	quoted := func(n int) string { return `"` + strings.Repeat("e", n-2) + `"` }
	return map[string]limitCase{
		"target": {"target",
			map[string]any{"target": target(maxMonitorTextLen)},
			map[string]any{"target": target(maxMonitorTextLen + 1)},
			"target must be 2048 characters or fewer"},
		"keyword": {"keyword",
			map[string]any{"keyword": strings.Repeat("k", maxMonitorTextLen)},
			map[string]any{"keyword": strings.Repeat("k", maxMonitorTextLen+1)},
			"keyword must be 2048 characters or fewer"},
		"expected_status": {"expected_status",
			map[string]any{"expected_status": status("")},
			map[string]any{"expected_status": status("2xx")},
			"expected_status must be 2048 characters or fewer"},
		"body": {"body",
			map[string]any{"method": "POST", "body": strings.Repeat("b", maxMonitorBodyLen)},
			map[string]any{"method": "POST", "body": strings.Repeat("b", maxMonitorBodyLen+1)},
			"body must be 65536 characters or fewer"},
		"header count": {"headers",
			map[string]any{"headers": headers(maxMonitorHeaders)},
			map[string]any{"headers": headers(maxMonitorHeaders + 1)},
			"a monitor sends at most 64 headers; this one has 65"},
		// "X-A: " is five bytes, so a value of 8185 makes a line of 8190.
		"header line": {"headers",
			map[string]any{"headers": map[string]any{"X-A": strings.Repeat("v", maxHeaderLineLen-5)}},
			map[string]any{"headers": map[string]any{"X-A": strings.Repeat("v", maxHeaderLineLen-4)}},
			`header "X-A" is 8191 bytes as sent; most servers refuse a header line over 8190`},
		"json path": {"json_assertion.path",
			map[string]any{"json_assertion": assertion(strings.Repeat("p", maxMonitorTextLen), `1`)},
			map[string]any{"json_assertion": assertion(strings.Repeat("p", maxMonitorTextLen+1), `1`)},
			"json_assertion.path must be 2048 characters or fewer"},
		// Measured as written, quotes included.
		"json expected": {"json_assertion.expected",
			map[string]any{"json_assertion": assertion("status", quoted(maxMonitorTextLen))},
			map[string]any{"json_assertion": assertion("status", quoted(maxMonitorTextLen+1))},
			"json_assertion.expected must be 2048 characters or fewer"},
	}
}

// limitJSON is a create request for an http monitor with fields merged in.
func limitJSON(t *testing.T, fields map[string]any, drop ...string) string {
	t.Helper()
	req := map[string]any{"name": "API", "type": "http", "target": "https://example.com/health"}
	for _, k := range drop {
		delete(req, k)
	}
	for k, v := range fields {
		req[k] = v
	}
	b, err := json.Marshal(req)
	must(t, err)
	return string(b)
}

func TestCreateMonitorHoldsFreeTextSettingsToTheirLimits(t *testing.T) {
	for name, tc := range monitorLimitCases() {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", limitJSON(t, tc.over))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.wantWords)
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Fatalf("a refused create stored %d monitors", len(mons))
			}
			rec = doJSON(t, srv, http.MethodPost, "/api/v1/monitors", limitJSON(t, tc.at))
			mustStatus(t, rec, http.StatusCreated, "create at the limit")
		})
	}
}

func TestPatchMonitorHoldsFreeTextSettingsToTheirLimits(t *testing.T) {
	for name, tc := range monitorLimitCases() {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Method: "POST",
				Target: "https://example.com/health", IntervalS: 60, TimeoutS: 10, Enabled: true})
			path := "/api/v1/monitors/" + itoa(m.ID)
			rec := patch(t, srv, path, limitJSON(t, tc.over, "name", "type", "target"))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.wantWords)
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if got.Target != m.Target || got.Keyword != "" || got.Body != "" || len(got.Headers) != 0 ||
				got.ExpectedStatus != m.ExpectedStatus || got.JSONAssertion != nil {
				t.Fatalf("a refused patch changed the monitor: %+v", got)
			}
			mustStatus(t, patch(t, srv, path, limitJSON(t, tc.at, "name", "type", "target")), http.StatusOK, "patch at the limit")
		})
	}
}

// A preview answers what the save would: a draft that previews green and is
// then refused on save is the one outcome a preview may not produce.
func TestPreviewHoldsFreeTextSettingsToTheirLimits(t *testing.T) {
	for name, tc := range monitorLimitCases() {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			srv.WithProber(&fakeProber{})
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors/preview", limitJSON(t, tc.over, "name"))
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.wantWords)
		})
	}
}

func TestImportHoldsFreeTextSettingsToTheirLimits(t *testing.T) {
	entry := func(fields map[string]any) string {
		m := map[string]any{"key": "api", "name": "API", "type": "http", "target": "https://example.com/health"}
		for k, v := range fields {
			m[k] = v
		}
		// JSON is YAML, so the entry is written as a JSON document.
		b, err := json.Marshal(map[string]any{"version": 1, "monitors": []any{m}})
		must(t, err)
		return string(b)
	}
	for name, tc := range monitorLimitCases() {
		for _, existing := range []bool{false, true} {
			label := name + "/new"
			if existing {
				label = name + "/existing"
			}
			t.Run(label, func(t *testing.T) {
				srv, db := testServerWithDB(t)
				if existing {
					if code, _, body := importYAML(t, srv, entry(map[string]any{"method": "POST"}), false); code != http.StatusOK {
						t.Fatalf("seeding import: status = %d: %s", code, body)
					}
				}
				code, _, body := importYAML(t, srv, entry(tc.over), false)
				wantStatusProblem(t, code, body, "monitors[0]."+tc.field, tc.wantWords)
				mons, err := db.ListMonitors(context.Background())
				must(t, err)
				for _, m := range mons {
					if m.Target != "https://example.com/health" || m.Keyword != "" || m.Body != "" ||
						len(m.Headers) != 0 || m.JSONAssertion != nil {
						t.Errorf("a refused file changed the monitor: %+v", m)
					}
				}
				if code, _, body := importYAML(t, srv, entry(tc.at), false); code != http.StatusOK {
					t.Errorf("import at the limit: status = %d: %s", code, body)
				}
			})
		}
	}
}

// legacyLong is a monitor saved before the limits, with every free-text
// setting past its ceiling.
func legacyLong() store.Monitor {
	headers := map[string]string{"X-Long": strings.Repeat("v", maxHeaderLineLen)}
	for i := range maxMonitorHeaders {
		headers["X-H"+itoa(int64(i))] = "v"
	}
	return store.Monitor{
		Name: "Legacy", Type: "http", Method: "POST",
		Target:         "https://example.com/" + strings.Repeat("t", maxMonitorTextLen),
		Keyword:        strings.Repeat("k", maxMonitorTextLen+1),
		ExpectedStatus: strings.Repeat("200,", 600),
		Headers:        headers,
		Body:           strings.Repeat("b", maxMonitorBodyLen+1),
		JSONAssertion: &store.JSONAssertion{Path: strings.Repeat("p", maxMonitorTextLen+1), Operator: "equals",
			Expected: `"` + strings.Repeat("e", maxMonitorTextLen) + `"`},
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	}
}

// A monitor saved before the limits keeps every setting through an edit of
// another one, through a PATCH that sends the stored values back, as an edit
// form does, and through an import of its own export.
func TestMonitorSavedBeforeTheLimitsKeepsItsSettings(t *testing.T) {
	srv, db := testServerWithDB(t)
	old := legacyLong()
	m := seedMonitor(t, db, old)
	path := "/api/v1/monitors/" + itoa(m.ID)

	mustStatus(t, patch(t, srv, path, `{"interval_s":120}`), http.StatusOK, "edit the interval")

	headers := map[string]any{}
	for k, v := range old.Headers {
		headers[k] = v
	}
	sameBack, err := json.Marshal(map[string]any{
		"target": old.Target, "keyword": old.Keyword, "expected_status": old.ExpectedStatus,
		"headers": headers, "body": old.Body,
		"json_assertion": map[string]any{"path": old.JSONAssertion.Path, "operator": "equals",
			"expected": json.RawMessage(old.JSONAssertion.Expected)},
	})
	must(t, err)
	mustStatus(t, patch(t, srv, path, string(sameBack)), http.StatusOK, "send the stored values back")

	doc := exportYAML(t, srv)
	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import of own export: status = %d: %.300s", code, body)
	}
	if len(rep.Monitors) != 1 || rep.Monitors[0].Action != actionUnchanged {
		t.Errorf("report = %+v, want one unchanged monitor", rep.Monitors)
	}

	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Target != old.Target || got.Keyword != old.Keyword || got.ExpectedStatus != old.ExpectedStatus ||
		got.Body != old.Body || len(got.Headers) != len(old.Headers) || got.Headers["X-Long"] != old.Headers["X-Long"] ||
		got.JSONAssertion == nil || *got.JSONAssertion != *old.JSONAssertion || got.IntervalS != 120 {
		t.Errorf("the stored settings changed")
	}

	// A new value is measured all the same.
	rec := patch(t, srv, path, `{"keyword":"`+strings.Repeat("n", maxMonitorTextLen+1)+`"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "keyword", "keyword must be 2048 characters or fewer")
}

// A refusal names the header without quoting all of a long name.
func TestHeaderLineRefusalQuotesAShortName(t *testing.T) {
	p := headersLimitProblem(map[string]string{strings.Repeat("N", 9000): "v"})
	if p.ok() || p.field != "headers" {
		t.Fatalf("problem = %+v, want one on headers", p)
	}
	if len(p.msg) > 200 {
		t.Errorf("the refusal is %d bytes long: %.120s...", len(p.msg), p.msg)
	}
}

// TestOpenAPIMonitorLimitsMatchTheServer keeps the document's limits where
// the server applies them to every value, and out of the schema where it
// does not. Create and preview measure every value, so their schemas carry
// the limits. An edit accepts a value saved before the limits back
// unchanged, which a maxLength cannot see, so the PATCH schema and the
// JSONAssertion it shares state them in words only: with a maxLength there,
// a validator refuses an edit the server takes.
func TestOpenAPIMonitorLimitsMatchTheServer(t *testing.T) {
	spec := loadSpec(t)
	schema := func(name string) map[string]any {
		t.Helper()
		node, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no components.schemas.%s", name)
		}
		var s map[string]any
		if err := node.Decode(&s); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return s
	}
	prop := func(s map[string]any, field string) map[string]any {
		props, _ := s["properties"].(map[string]any)
		p, _ := props[field].(map[string]any)
		return p
	}
	const limitsRef = "#/components/schemas/JSONAssertionLimits"
	want := map[string]struct {
		key string
		n   int
	}{
		"target":          {"maxLength", maxMonitorTextLen},
		"keyword":         {"maxLength", maxMonitorTextLen},
		"expected_status": {"maxLength", maxMonitorTextLen},
		"body":            {"maxLength", maxMonitorBodyLen},
		"headers":         {"maxProperties", maxMonitorHeaders},
	}

	for _, name := range []string{"CreateMonitorRequest", "PreviewCheckRequest"} {
		s := schema(name)
		for field, w := range want {
			if got := prop(s, field)[w.key]; got != w.n {
				t.Errorf("%s.%s %s = %v, want %d", name, field, w.key, got, w.n)
			}
		}
		if !slices.Contains(schemaRefs(prop(s, "json_assertion")), limitsRef) {
			t.Errorf("%s.json_assertion does not take JSONAssertionLimits", name)
		}
	}
	if got := prop(schema("JSONAssertionLimits"), "path")["maxLength"]; got != maxMonitorTextLen {
		t.Errorf("JSONAssertionLimits.path maxLength = %v, want %d", got, maxMonitorTextLen)
	}

	patchSchema := schema("PatchMonitorRequest")
	for field := range want {
		for _, key := range []string{"maxLength", "maxProperties"} {
			if got, ok := prop(patchSchema, field)[key]; ok {
				t.Errorf("PatchMonitorRequest.%s sets %s %v, which refuses a stored value sent back", field, key, got)
			}
		}
	}
	if slices.Contains(schemaRefs(patchSchema), limitsRef) {
		t.Errorf("PatchMonitorRequest takes JSONAssertionLimits, which refuses a stored path sent back")
	}
	for _, field := range []string{"path", "expected"} {
		if got, ok := prop(schema("JSONAssertion"), field)["maxLength"]; ok {
			t.Errorf("JSONAssertion.%s sets maxLength %v, but PATCH shares it", field, got)
		}
	}
}

// schemaRefs lists every $ref inside a decoded schema.
func schemaRefs(v any) []string {
	var refs []string
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" {
				refs = append(refs, s)
			}
			refs = append(refs, schemaRefs(e)...)
		}
	case []any:
		for _, e := range x {
			refs = append(refs, schemaRefs(e)...)
		}
	}
	return refs
}
