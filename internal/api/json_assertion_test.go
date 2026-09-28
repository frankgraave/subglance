package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// These pin the API contract of json_assertion: the round trip through
// create, detail read, PATCH and preview, typed expected values, and field
// problems that name the sub-field at fault. The evaluation itself is tested
// in internal/checker.

const assertionMonitor = `{"name":"api","type":"http","target":"https://example.com/health",` +
	`"json_assertion":{"path":"checks.db.status","operator":"equals","expected":"up"}}`

func createdID(t *testing.T, body []byte) int64 {
	t.Helper()
	var got monitorResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got.ID
}

func detailAssertion(t *testing.T, srv *Server, id int64) any {
	t.Helper()
	rec := getMonitorRaw(t, srv, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	v, present := got["json_assertion"]
	if !present {
		t.Fatal("detail read has no json_assertion key; want null or an object")
	}
	return v
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateMonitorStoresJSONAssertion(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors", assertionMonitor)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	id := createdID(t, rec.Body.Bytes())

	stored, err := db.GetMonitor(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := store.JSONAssertion{Path: "checks.db.status", Operator: "equals", Expected: `"up"`}
	if stored.JSONAssertion == nil || *stored.JSONAssertion != want {
		t.Fatalf("stored assertion = %+v, want %+v", stored.JSONAssertion, want)
	}

	got := detailAssertion(t, srv, id)
	wantWire := map[string]any{"path": "checks.db.status", "operator": "equals", "expected": "up"}
	if b, _ := json.Marshal(got); string(b) != mustJSON(t, wantWire) {
		t.Errorf("detail json_assertion = %s, want %s", b, mustJSON(t, wantWire))
	}
}

// "1" and 1 are different expected values, and so are false and null. Each
// has to survive the trip to the database and back with its type intact.
func TestJSONAssertionExpectedKeepsItsType(t *testing.T) {
	srv, db := testServerWithDB(t)

	for _, expected := range []string{`"1"`, `1`, `1.5`, `true`, `false`, `null`, `"null"`} {
		body := `{"name":"x","type":"http","target":"https://example.com",` +
			`"json_assertion":{"path":"v","operator":"equals","expected":` + expected + `}}`
		rec := post(t, srv, "/api/v1/monitors", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected %s: status = %d: %s", expected, rec.Code, rec.Body.String())
		}
		id := createdID(t, rec.Body.Bytes())
		stored, err := db.GetMonitor(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if stored.JSONAssertion == nil || stored.JSONAssertion.Expected != expected {
			t.Errorf("expected %s stored as %+v", expected, stored.JSONAssertion)
		}
		got := detailAssertion(t, srv, id).(map[string]any)
		if b, _ := json.Marshal(got["expected"]); string(b) != expected {
			t.Errorf("expected %s read back as %s", expected, b)
		}
	}
}

func TestCreateMonitorWithoutJSONAssertionReadsNull(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"x","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := detailAssertion(t, srv, createdID(t, rec.Body.Bytes())); got != nil {
		t.Errorf("json_assertion = %#v, want null", got)
	}
}

func TestExistsAssertionNeedsNoExpectedValue(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"x","type":"http","target":"https://example.com",`+
		`"json_assertion":{"path":"items[0].id","operator":"exists"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	stored, _ := db.GetMonitor(t.Context(), createdID(t, rec.Body.Bytes()))
	if stored.JSONAssertion == nil || stored.JSONAssertion.Expected != "" {
		t.Errorf("stored = %+v, want an exists assertion with no expected value", stored.JSONAssertion)
	}
	got := detailAssertion(t, srv, stored.ID).(map[string]any)
	if _, present := got["expected"]; present {
		t.Errorf("detail carries expected %#v for exists; want it omitted", got["expected"])
	}
}

func TestCreateMonitorRejectsBadJSONAssertion(t *testing.T) {
	srv, _ := testServerWithDB(t)
	tests := []struct {
		name, assertion, field string
	}{
		{"bad path", `{"path":"a..b","operator":"exists"}`, "json_assertion.path"},
		{"empty path", `{"path":"","operator":"exists"}`, "json_assertion.path"},
		{"unknown operator", `{"path":"a","operator":"contains","expected":"x"}`, "json_assertion.operator"},
		{"missing expected", `{"path":"a","operator":"equals"}`, "json_assertion.expected"},
		{"expected on exists", `{"path":"a","operator":"exists","expected":1}`, "json_assertion.expected"},
		{"object expected", `{"path":"a","operator":"equals","expected":{"b":1}}`, "json_assertion.expected"},
		{"string for less_than", `{"path":"a","operator":"less_than","expected":"10"}`, "json_assertion.expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, srv, "/api/v1/monitors", `{"name":"x","type":"http","target":"https://example.com",`+
				`"json_assertion":`+tt.assertion+`}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			assertProblemField(t, rec, tt.field)
		})
	}
}

func TestJSONAssertionOnlyOnHTTPMonitors(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"x","type":"tcp","target":"db.internal:5432",`+
		`"json_assertion":{"path":"a","operator":"exists"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertProblemField(t, rec, "json_assertion")
}

func TestPatchMonitorJSONAssertion(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})

	rec := patch(t, srv, monitorPath(m.ID), `{"json_assertion":{"path":"lag","operator":"less_than","expected":30}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: status = %d: %s", rec.Code, rec.Body.String())
	}
	stored, _ := db.GetMonitor(t.Context(), m.ID)
	if stored.JSONAssertion == nil || stored.JSONAssertion.Expected != "30" {
		t.Fatalf("after set: %+v", stored.JSONAssertion)
	}

	// Omitting the key leaves the assertion alone.
	if rec := patch(t, srv, monitorPath(m.ID), `{"name":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d: %s", rec.Code, rec.Body.String())
	}
	if stored, _ = db.GetMonitor(t.Context(), m.ID); stored.JSONAssertion == nil {
		t.Fatal("a rename removed the assertion")
	}

	// A refused change leaves it alone too.
	rec = patch(t, srv, monitorPath(m.ID), `{"json_assertion":{"path":"lag","operator":"less_than","expected":"x"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad set: status = %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "json_assertion.expected")
	if stored, _ = db.GetMonitor(t.Context(), m.ID); stored.JSONAssertion == nil || stored.JSONAssertion.Expected != "30" {
		t.Fatalf("a refused PATCH changed the assertion to %+v", stored.JSONAssertion)
	}

	// Turning the monitor into a tcp monitor while it has an assertion is
	// refused rather than keeping a condition that is never evaluated.
	rec = patch(t, srv, monitorPath(m.ID), `{"type":"tcp","target":"db.internal:443"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("type change: status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertProblemField(t, rec, "json_assertion")

	// An explicit null removes it.
	if rec := patch(t, srv, monitorPath(m.ID), `{"json_assertion":null}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d: %s", rec.Code, rec.Body.String())
	}
	if stored, _ = db.GetMonitor(t.Context(), m.ID); stored.JSONAssertion != nil {
		t.Fatalf("null left the assertion in place: %+v", stored.JSONAssertion)
	}
	// And with it gone, the type change goes through.
	if rec := patch(t, srv, monitorPath(m.ID), `{"type":"tcp","target":"db.internal:443"}`); rec.Code != http.StatusOK {
		t.Fatalf("type change after clear: status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchMonitorRejectsUnknownAssertionFields(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})
	rec := patch(t, srv, monitorPath(m.ID), `{"json_assertion":{"path":"a","operator":"exists","value":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertProblemField(t, rec, "json_assertion")
}

// A preview probes with the assertion the monitor would be saved with, and
// its failure reports as an assertion, not as a keyword.
func TestPreviewCarriesJSONAssertion(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{
		OK: false, StatusCode: 200, Kind: checker.FailAssertion,
		Error: `checks.db.status was "down", expected "up"`,
	}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"http","target":"https://example.com/health",`+
		`"json_assertion":{"path":"checks.db.status","operator":"equals","expected":"up"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodePreview(t, rec)
	if got.Kind != "assertion" {
		t.Errorf("kind = %q, want assertion", got.Kind)
	}
	sent := prober.lastMonitor().JSONAssertion
	want := store.JSONAssertion{Path: "checks.db.status", Operator: "equals", Expected: `"up"`}
	if sent == nil || *sent != want {
		t.Errorf("prober got assertion %+v, want %+v", sent, want)
	}
}

func TestPreviewRejectsBadJSONAssertion(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"http","target":"https://example.com",`+
		`"json_assertion":{"path":"a[","operator":"exists"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertProblemField(t, rec, "json_assertion.path")
	if prober.callCount() != 0 {
		t.Error("a refused preview still probed")
	}
}
