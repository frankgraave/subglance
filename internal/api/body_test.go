package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// TestRequestBodyRefusals pins how every JSON endpoint answers a body it
// cannot take: a wrong type names the request field and the JSON type it
// wants, the body-level failures name no field, and nothing in the sentence
// is Go's (a struct name, a Go type, the "json:" prefix).
func TestRequestBodyRefusals(t *testing.T) {
	srv, db := testServerWithDB(t)
	// Preview needs a checker to get past its 503; the body is refused
	// before the prober is reached.
	srv.WithProber(&fakeProber{})
	m := seedMonitor(t, db, store.Monitor{Name: "seed", Type: "http", Target: "https://example.com", Enabled: true})
	monitor := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10)
	const http0 = `"name":"x","type":"http","target":"https://example.com"`

	tests := []struct {
		name, method, path, body string
		status                   int
		field, want              string
	}{
		// A value of the wrong JSON type, at the top level and nested.
		{"string for an integer", "POST", "/api/v1/monitors", `{` + http0 + `,"interval_s":"60"}`,
			400, "interval_s", "interval_s must be a whole number, not a string"},
		{"fraction for an integer", "POST", "/api/v1/monitors", `{` + http0 + `,"interval_s":1.5}`,
			400, "interval_s", "interval_s must be a whole number, not 1.5"},
		{"integer out of range", "POST", "/api/v1/monitors", `{` + http0 + `,"interval_s":99999999999999999999}`,
			400, "interval_s", "interval_s is out of range"},
		{"boolean on a patch", "PATCH", monitor, `{"interval_s":true}`,
			400, "interval_s", "interval_s must be a whole number, not true or false"},
		{"number for a boolean", "PATCH", monitor, `{"enabled":1}`,
			400, "enabled", "enabled must be true or false, not a number"},
		{"tag value", "POST", "/api/v1/monitors", `{` + http0 + `,"tags":{"env":5}}`,
			400, "tags", "each value in tags must be a string, not a number"},
		{"tags as an array", "POST", "/api/v1/monitors", `{` + http0 + `,"tags":["env"]}`,
			400, "tags", "tags must be an object whose values are strings, not an array"},
		{"channel id", "POST", "/api/v1/monitors", `{` + http0 + `,"channel_ids":["a"]}`,
			400, "channel_ids", "each item in channel_ids must be a whole number, not a string"},
		{"nested dns field", "POST", "/api/v1/monitors", `{"name":"x","type":"dns","target":"example.com","dns":{"record_type":"A","expected":"1.2.3.4"}}`,
			400, "dns.expected", "dns.expected must be an array of strings, not a string"},
		{"assertion field on create", "POST", "/api/v1/monitors", `{` + http0 + `,"json_assertion":{"path":5,"operator":"equals","expected":1}}`,
			400, "json_assertion.path", "json_assertion.path must be a string, not a number"},
		{"assertion field on a patch", "PATCH", monitor, `{"json_assertion":{"path":5,"operator":"equals","expected":1}}`,
			400, "json_assertion.path", "json_assertion.path must be a string, not a number"},
		{"assertion key on a patch", "PATCH", monitor, `{"json_assertion":{"pth":"x"}}`,
			400, "json_assertion", `json_assertion must be an object with path, operator and expected, or null: unknown field "pth"`},
		{"preview", "POST", "/api/v1/monitors/preview", `{"type":"http","target":"https://example.com","timeout_s":"5"}`,
			400, "timeout_s", "timeout_s must be a whole number, not a string"},
		{"channel config value", "POST", "/api/v1/channels", `{"name":"x","type":"webhook","config":{"url":5}}`,
			400, "config", "each value in config must be a string, not a number"},
		{"maintenance name", "POST", "/api/v1/maintenance", `{"name":5}`,
			400, "name", "name must be a string, not a number"},
		{"token name", "POST", "/api/v1/tokens", `{"name":["x"]}`,
			400, "name", "name must be a string, not an array"},
		{"bulk tag action", "POST", "/api/v1/monitors/tags/preview", `{"action":5,"key":"env"}`,
			400, "action", "action must be a string, not a number"},
		{"bulk channel selection", "POST", "/api/v1/monitors/channels/preview", `{"action":"add","monitor_ids":"1","channel_id":1}`,
			400, "monitor_ids", "monitor_ids must be an array of whole numbers, not a string"},

		// Refusals about the body as a whole name no field.
		{"unknown field", "POST", "/api/v1/monitors", `{` + http0 + `,"intervl_s":60}`,
			400, "", `unknown field "intervl_s"`},
		{"array for the body", "POST", "/api/v1/channels", `[1]`,
			400, "", "the request body must be an object, not an array"},
		{"empty body", "POST", "/api/v1/tokens", ``,
			400, "", "the request body is empty"},
		{"truncated body", "PATCH", monitor, `{"name":`,
			400, "", "invalid JSON: the request body ends before the JSON value does"},
		{"syntax error", "POST", "/api/v1/maintenance", `{"name" 1}`,
			400, "", "invalid JSON at byte 9: invalid character '1' after object key"},
		{"second value", "POST", "/api/v1/monitors", `{` + http0 + `} {"name":"y"}`,
			400, "", "the request body must be exactly one JSON value"},
		{"trailing text", "POST", "/api/v1/channels", `{"name":"x","type":"webhook","config":{"url":"https://example.com/h"}} trailing`,
			400, "", "the request body must be exactly one JSON value"},
		{"time that does not parse", "POST", "/api/v1/maintenance", `{"name":"m","monitor_id":1,"starts_at":"tomorrow","ends_at":"2026-10-14T10:00:00Z"}`,
			400, "", `a time must be an RFC 3339 string such as 2026-10-14T09:00:00Z, not "tomorrow"`},
		{"time that is not a string", "POST", "/api/v1/maintenance", `{"name":"m","monitor_id":1,"starts_at":5}`,
			400, "", "a time must be an RFC 3339 string"},

		// A body over the limit is a size refusal.
		{"oversized monitor", "POST", "/api/v1/monitors", `{` + http0 + `,"body":"` + strings.Repeat("a", maxJSONBody) + `"}`,
			413, "", "the request body is larger than 1 MiB"},
		{"oversized bulk operation", "POST", "/api/v1/monitors/tags/preview", `{"action":"apply","key":"k","value":"` + strings.Repeat("a", maxBulkJSONBody) + `"}`,
			413, "", "the request body is larger than 256 KiB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, srv, tc.method, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			var got errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode refusal: %v (%s)", err, rec.Body.String())
			}
			if got.Field != tc.field {
				t.Errorf("field = %q, want %q (%s)", got.Field, tc.field, got.Error)
			}
			if !strings.HasPrefix(got.Error, tc.want) {
				t.Errorf("error = %q, want it to start with %q", got.Error, tc.want)
			}
			for _, goText := range []string{"Go struct", "Go value", "json: ", "Request", "map[", "int64", "UnmarshalJSON", "http: "} {
				if strings.Contains(got.Error, goText) {
					t.Errorf("error %q carries decoder text %q", got.Error, goText)
				}
			}
		})
	}

	// None of the refused creates may have created anything.
	monitors, err := db.ListMonitors(t.Context())
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 {
		t.Errorf("monitors = %d after refused creates, want only the seed", len(monitors))
	}
}

// TestRequestBodiesAreReadByOneDecoder keeps every JSON request body on
// readJSON. A handler with a decoder of its own is how the API came to answer
// with Go's error text and no field, and to accept a second JSON value on
// some endpoints and refuse it on others.
func TestRequestBodiesAreReadByOneDecoder(t *testing.T) {
	// The bodies that are not JSON: a configuration file (YAML) and a logo
	// image. Both are read whole, under a limit of their own.
	rawBody := map[string]string{
		"config_handlers.go":  "io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigFileBytes))",
		"status_page_logo.go": "io.ReadAll(http.MaxBytesReader(w, r.Body, store.MaxStatusPageLogoBytes))",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "body.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(src), "\n") {
			at := name + ":" + strconv.Itoa(i+1)
			if strings.Contains(line, "json.NewDecoder(") {
				t.Errorf("%s decodes JSON itself; use readJSON, or decodeStrict for a raw part of a body", at)
			}
			if readsBody.MatchString(line) && (rawBody[name] == "" || !strings.Contains(line, rawBody[name])) {
				t.Errorf("%s reads the request body itself; use readJSON", at)
			}
			if strings.Contains(line, `"invalid JSON: "+`) {
				t.Errorf("%s writes the decoder's error text; answer with bodyRefusal", at)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked %d files; the walk did not find the package", checked)
	}
}

// readsBody matches the request's body, not a field that ends in Body
// (a monitor's own m.Body).
var readsBody = regexp.MustCompile(`(^|[^\w.])r\.Body\b`)

func TestByteSize(t *testing.T) {
	for n, want := range map[int64]string{1 << 20: "1 MiB", 4 << 20: "4 MiB", 256 << 10: "256 KiB", 1000: "1000 bytes", 1536 << 10: "1536 KiB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// A request type whose field is declared on an embedded struct is found as
// encoding/json finds it.
func TestFieldTypeLooksThroughEmbeddedStructs(t *testing.T) {
	type inner struct {
		Port *int `json:"port"`
	}
	type outer struct {
		inner
		Hosts []struct {
			Name string `json:"name"`
		} `json:"hosts"`
		Skip string `json:"-"`
	}
	if got := fieldType(typeOf[outer](), "port"); got == nil || got.String() != "*int" {
		t.Errorf("port = %v, want *int", got)
	}
	if got := fieldType(typeOf[outer](), "hosts.name"); got == nil || got.String() != "string" {
		t.Errorf("hosts.name = %v, want string", got)
	}
	if got := fieldType(typeOf[outer](), "-"); got != nil {
		t.Errorf(`"-" = %v, want nil`, got)
	}
	if got := fieldType(typeOf[outer](), "missing"); got != nil {
		t.Errorf("missing = %v, want nil", got)
	}
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }
