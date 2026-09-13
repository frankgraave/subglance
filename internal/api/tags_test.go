package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createTagged creates a monitor with the given JSON body and returns the
// decoded response.
func createTagged(t *testing.T, srv *Server, body string) monitorResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return got
}

func TestCreateMonitorStoresAndReturnsTags(t *testing.T) {
	srv, _ := testServerWithDB(t)

	created := createTagged(t, srv, `{"name":"a","type":"http","target":"https://a.example",
		"tags":{" Env ":" prod ","customer":"Acme"}}`)

	// The key is canonical, the value keeps its case: `customer:Acme` is a
	// name and flattening it would show up in the UI.
	if created.Tags["env"] != "prod" || created.Tags["customer"] != "Acme" {
		t.Fatalf("tags = %v, want env=prod customer=Acme", created.Tags)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodGet, "/api/v1/monitors/"+itoa(created.ID), ""))
	var fetched monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.Tags["env"] != "prod" {
		t.Fatalf("tags after re-read = %v", fetched.Tags)
	}
}

// An untagged monitor must not grow a `"tags":{}` key: the response shape for
// everything created before tags existed stays byte for byte what it was.
func TestUntaggedMonitorOmitsTagsFromJSON(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"a","type":"http","target":"https://a.example"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"tags"`) {
		t.Fatalf("untagged monitor carried a tags key: %s", rec.Body.String())
	}
}

func TestCreateMonitorRejectsBadTags(t *testing.T) {
	srv, _ := testServerWithDB(t)

	cases := map[string]string{
		"bare tag":       `{"name":"a","type":"http","target":"https://a.example","tags":{"env":""}}`,
		"empty key":      `{"name":"a","type":"http","target":"https://a.example","tags":{"  ":"prod"}}`,
		"key with space": `{"name":"a","type":"http","target":"https://a.example","tags":{"my env":"prod"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors", body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			// The problem must name the field, so a form can show the error
			// next to the input that caused it.
			if !strings.Contains(rec.Body.String(), `"tags"`) {
				t.Fatalf("problem did not name the tags field: %s", rec.Body.String())
			}
		})
	}
}

// A value containing a colon must survive: the API never splits on `:`, so
// `key:value` stays a display convention rather than a parse format.
func TestTagValueMayContainAColon(t *testing.T) {
	srv, _ := testServerWithDB(t)

	created := createTagged(t, srv,
		`{"name":"a","type":"http","target":"https://a.example","tags":{"docs":"https://example.com:8443/x"}}`)
	if got := created.Tags["docs"]; got != "https://example.com:8443/x" {
		t.Fatalf("docs tag = %q, want the colon preserved", got)
	}
}

// PATCH replaces the whole set, like headers: without that there is no way to
// remove a tag through the single `tags` field.
func TestPatchReplacesTagsAndCanClearThem(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createTagged(t, srv,
		`{"name":"a","type":"http","target":"https://a.example","tags":{"env":"prod","customer":"Acme"}}`)
	path := "/api/v1/monitors/" + itoa(created.ID)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch, path, `{"tags":{"env":"staging"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if len(patched.Tags) != 1 || patched.Tags["env"] != "staging" {
		t.Fatalf("tags after patch = %v, want only env=staging", patched.Tags)
	}

	rec = httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch, path, `{"tags":{}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cleared monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if len(cleared.Tags) != 0 {
		t.Fatalf("tags after clearing = %v, want none", cleared.Tags)
	}
}

// Omitting `tags` from a PATCH must leave them alone; the whole point of the
// pointer fields is that "not sent" and "sent empty" differ.
func TestPatchWithoutTagsLeavesThemAlone(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createTagged(t, srv,
		`{"name":"a","type":"http","target":"https://a.example","tags":{"env":"prod"}}`)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		jsonRequest(http.MethodPatch, "/api/v1/monitors/"+itoa(created.ID), `{"name":"renamed"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Tags["env"] != "prod" {
		t.Fatalf("rename dropped the tags: %v", patched.Tags)
	}
}

func TestPatchRejectsBadTags(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createTagged(t, srv, `{"name":"a","type":"http","target":"https://a.example"}`)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		jsonRequest(http.MethodPatch, "/api/v1/monitors/"+itoa(created.ID), `{"tags":{"env":"  "}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// The listing is what the dashboard reads, and it must carry tags so grouping
// and filtering need no second fetch.
func TestListMonitorsCarriesTags(t *testing.T) {
	srv, _ := testServerWithDB(t)
	createTagged(t, srv, `{"name":"a","type":"http","target":"https://a.example","tags":{"env":"prod"}}`)
	createTagged(t, srv, `{"name":"b","type":"http","target":"https://b.example","tags":{"env":"staging"}}`)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodGet, "/api/v1/monitors", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var body struct {
		Monitors []monitorResponse `json:"monitors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	list := body.Monitors
	if len(list) != 2 {
		t.Fatalf("got %d monitors, want 2", len(list))
	}
	seen := map[string]bool{}
	for _, m := range list {
		seen[m.Tags["env"]] = true
	}
	if !seen["prod"] || !seen["staging"] {
		t.Fatalf("listing lost tags: %+v", list)
	}
}
