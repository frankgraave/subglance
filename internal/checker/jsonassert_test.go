package checker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func assertion(path string, op JSONOperator, expected string) *JSONAssertion {
	a := &JSONAssertion{Path: path, Operator: op}
	if expected != "" {
		a.Expected = json.RawMessage(expected)
	}
	return a
}

// The case the feature exists for: a health endpoint that answers 200 and
// says, in a field, that it is not healthy.
func TestHTTPJSONAssertionFailsOnHealthyStatusWithUnhealthyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"degraded","checks":{"db":{"status":"down"}}}`))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.CaptureResponse = true
	m.JSONAssertion = assertion("checks.db.status", JSONEquals, `"up"`)

	res := testChecker().Check(context.Background(), m)
	if res.OK {
		t.Fatal("check passed on a body that reports the database down")
	}
	if res.Kind != FailAssertion {
		t.Errorf("kind = %q, want %q", res.Kind, FailAssertion)
	}
	if want := `checks.db.status was "down", expected "up"`; res.Error != want {
		t.Errorf("error = %q, want %q", res.Error, want)
	}
	if res.Response == nil || !strings.Contains(res.Response.Body, `"down"`) {
		t.Errorf("snapshot = %+v, want the body that failed the assertion", res.Response)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 kept on the result", res.StatusCode)
	}
}

func TestHTTPJSONAssertionPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"ok":true}],"checks":{"db":{"status":"up"}}}`))
	}))
	defer srv.Close()

	for _, a := range []*JSONAssertion{
		assertion("checks.db.status", JSONEquals, `"up"`),
		assertion("items[0].ok", JSONEquals, `true`),
		assertion("items[0]", JSONExists, ""),
	} {
		m := monitor(srv.URL)
		m.JSONAssertion = a
		if res := testChecker().Check(context.Background(), m); !res.OK {
			t.Errorf("%s %s %s: failed: %s", a.Path, a.Operator, a.Expected, res.Error)
		}
	}
}

// A status failure is reported as a status failure: the assertion is about a
// response that was otherwise acceptable, and a 503 page is rarely JSON.
func TestHTTPJSONAssertionRunsAfterTheStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.JSONAssertion = assertion("status", JSONEquals, `"up"`)
	res := testChecker().Check(context.Background(), m)
	if res.Kind != FailStatus {
		t.Errorf("kind = %q, want %q", res.Kind, FailStatus)
	}
}

// A body past the read cap fails explicitly rather than being parsed as the
// truncated, invalid document its first MiB would be.
func TestHTTPJSONAssertionRefusesAnOversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"up","pad":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxBodyRead)))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.JSONAssertion = assertion("status", JSONEquals, `"up"`)
	res := testChecker().Check(context.Background(), m)
	if res.OK || res.Kind != FailAssertion {
		t.Fatalf("result = ok %v kind %q, want an assertion failure", res.OK, res.Kind)
	}
	if !strings.Contains(res.Error, "larger than 1024 KiB") {
		t.Errorf("error = %q, want it to say the body was too large", res.Error)
	}
}

// A body of exactly the cap is complete, and is checked.
func TestHTTPJSONAssertionAcceptsABodyOfExactlyTheCap(t *testing.T) {
	prefix, suffix := `{"status":"up","pad":"`, `"}`
	pad := strings.Repeat("x", maxBodyRead-len(prefix)-len(suffix))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(prefix + pad + suffix))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.JSONAssertion = assertion("status", JSONEquals, `"up"`)
	if res := testChecker().Check(context.Background(), m); !res.OK {
		t.Errorf("check failed on a body of exactly %d bytes: %s", maxBodyRead, res.Error)
	}
}

// The keyword search still sees the first MiB of an oversize body, as it did
// before the assertion shared its read.
func TestHTTPKeywordStillSearchesAnOversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("healthy "))
		_, _ = w.Write([]byte(strings.Repeat("x", maxBodyRead)))
	}))
	defer srv.Close()

	m := monitor(srv.URL)
	m.Keyword, m.KeywordMode = "healthy", KeywordMustContain
	if res := testChecker().Check(context.Background(), m); !res.OK {
		t.Errorf("keyword check failed: %s", res.Error)
	}
}

// A keyword-only check reads the cap and stops: a stream that has sent
// exactly maxBodyRead bytes and then holds the connection open is judged on
// what it sent, not failed as a connection error when the timeout cuts the
// read short.
func TestHTTPKeywordDoesNotWaitPastTheCap(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("healthy "))
		_, _ = w.Write([]byte(strings.Repeat("x", maxBodyRead-len("healthy "))))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	m := monitor(srv.URL)
	m.Timeout = 2 * time.Second
	m.Keyword, m.KeywordMode = "healthy", KeywordMustContain
	began := time.Now()
	res := testChecker().Check(context.Background(), m)
	elapsed := time.Since(began)
	if !res.OK {
		t.Fatalf("keyword check failed: %s (kind %q)", res.Error, res.Kind)
	}
	if res.Latency >= m.Timeout || elapsed >= m.Timeout {
		t.Errorf("latency %v, Check took %v; want both under the %v timeout", res.Latency, elapsed, m.Timeout)
	}
}

func TestEvaluateJSONAssertion(t *testing.T) {
	tests := []struct {
		name string
		body string
		a    *JSONAssertion
		want string // "" means the assertion holds
	}{
		// Typed equality: the string "1" is not the number 1.
		{"number equals number", `{"n":1}`, assertion("n", JSONEquals, `1`), ""},
		{"number equals decimal form", `{"n":1.0}`, assertion("n", JSONEquals, `1`), ""},
		{"string is not number", `{"n":"1"}`, assertion("n", JSONEquals, `1`),
			`n was the string "1", expected the number 1`},
		{"number is not string", `{"n":1}`, assertion("n", JSONEquals, `"1"`),
			`n was the number 1, expected the string "1"`},
		{"string differs", `{"s":"down"}`, assertion("s", JSONEquals, `"up"`), `s was "down", expected "up"`},

		// Booleans and null.
		{"true equals true", `{"ok":true}`, assertion("ok", JSONEquals, `true`), ""},
		{"false is not true", `{"ok":false}`, assertion("ok", JSONEquals, `true`), `ok was false, expected true`},
		{"string true is not true", `{"ok":"true"}`, assertion("ok", JSONEquals, `true`),
			`ok was the string "true", expected the boolean true`},
		{"null equals null", `{"err":null}`, assertion("err", JSONEquals, `null`), ""},
		{"false is not null", `{"err":false}`, assertion("err", JSONEquals, `null`),
			`err was the boolean false, expected null`},
		{"null is not a string", `{"s":null}`, assertion("s", JSONEquals, `"up"`),
			`s was null, expected the string "up"`},
		{"null exists", `{"err":null}`, assertion("err", JSONExists, ""), ""},

		// not_equals.
		{"not_equals holds", `{"s":"up"}`, assertion("s", JSONNotEquals, `"down"`), ""},
		{"not_equals holds across types", `{"n":"0"}`, assertion("n", JSONNotEquals, `0`), ""},
		{"not_equals fails", `{"s":"down"}`, assertion("s", JSONNotEquals, `"down"`),
			`s was "down", expected anything else`},

		// Ordering compares numbers only, exactly.
		{"less_than holds", `{"lag":3}`, assertion("lag", JSONLessThan, `10`), ""},
		{"less_than fails", `{"lag":12.5}`, assertion("lag", JSONLessThan, `10`), `lag was 12.5, expected less than 10`},
		{"less_than equal fails", `{"lag":10}`, assertion("lag", JSONLessThan, `10`), `lag was 10, expected less than 10`},
		{"greater_than holds", `{"free":0.25}`, assertion("free", JSONGreaterThan, `0.1`), ""},
		{"greater_than on a string", `{"free":"50"}`, assertion("free", JSONGreaterThan, `10`),
			`free was the string "50", expected a number greater than 10`},
		{"large integers compare exactly", `{"id":9007199254740993}`,
			assertion("id", JSONGreaterThan, `9007199254740992`), ""},

		// Paths.
		{"array index", `{"items":[{"ok":false},{"ok":true}]}`, assertion("items[1].ok", JSONEquals, `true`), ""},
		{"top-level array", `[{"id":7}]`, assertion("[0].id", JSONEquals, `7`), ""},
		{"nested index", `{"m":[[1,2],[3]]}`, assertion("m[1][0]", JSONEquals, `3`), ""},

		// Missing paths say where the walk stopped.
		{"missing key", `{"checks":{}}`, assertion("checks.db.status", JSONEquals, `"up"`),
			`checks.db.status does not exist in the response: checks has no key "db"`},
		{"missing top-level key", `{}`, assertion("status", JSONExists, ""),
			`status does not exist in the response: the top level has no key "status"`},
		{"index out of range", `{"items":[]}`, assertion("items[0].ok", JSONEquals, `true`),
			`items[0].ok does not exist in the response: items has 0 item(s), so there is no [0]`},
		{"key into a string", `{"checks":"fine"}`, assertion("checks.db", JSONExists, ""),
			`checks.db does not exist in the response: checks is a string, not an object`},
		{"index into an object", `{"items":{}}`, assertion("items[0]", JSONExists, ""),
			`items[0] does not exist in the response: items is an object, not an array`},

		// Not JSON at all.
		{"html body", `<html>ok</html>`, assertion("status", JSONEquals, `"up"`),
			`response is not valid JSON, so status could not be read`},
		{"empty body", ``, assertion("status", JSONExists, ""),
			`response is not valid JSON, so status could not be read`},
		{"two documents", `{"status":"up"}{"status":"down"}`, assertion("status", JSONEquals, `"up"`),
			`response is not valid JSON, so status could not be read`},
		{"composite value is not a scalar", `{"s":{"a":1}}`, assertion("s", JSONEquals, `"up"`),
			`s was an object, expected the string "up"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if field, err := ValidateJSONAssertion(*tt.a); err != nil {
				t.Fatalf("test assertion is invalid (%s): %v", field, err)
			}
			if got := evaluateJSONAssertion(*tt.a, []byte(tt.body)); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestValidateJSONAssertion(t *testing.T) {
	tests := []struct {
		name      string
		a         JSONAssertion
		wantField string // "" means valid
	}{
		{"valid equals", *assertion("checks.db.status", JSONEquals, `"up"`), ""},
		{"valid index", *assertion("items[0].ok", JSONEquals, `true`), ""},
		{"valid top-level index", *assertion("[0]", JSONExists, ""), ""},
		{"valid null", *assertion("err", JSONEquals, `null`), ""},

		{"empty path", *assertion("", JSONExists, ""), "path"},
		{"leading dot", *assertion(".a", JSONExists, ""), "path"},
		{"trailing dot", *assertion("a.", JSONExists, ""), "path"},
		{"double dot", *assertion("a..b", JSONExists, ""), "path"},
		{"unclosed bracket", *assertion("a[0", JSONExists, ""), "path"},
		{"non-numeric index", *assertion("a[x]", JSONExists, ""), "path"},
		{"negative index", *assertion("a[-1]", JSONExists, ""), "path"},
		{"empty index", *assertion("a[]", JSONExists, ""), "path"},
		{"dot before bracket", *assertion("a.[0]", JSONExists, ""), "path"},
		{"key straight after bracket", *assertion("a[0]b", JSONExists, ""), "path"},
		{"stray close bracket", *assertion("a]", JSONExists, ""), "path"},

		{"unknown operator", *assertion("a", "contains", `"x"`), "operator"},
		{"empty operator", *assertion("a", "", `"x"`), "operator"},

		{"exists with a value", *assertion("a", JSONExists, `"x"`), "expected"},
		{"equals without a value", *assertion("a", JSONEquals, ""), "expected"},
		{"bare word is not JSON", *assertion("a", JSONEquals, `up`), "expected"},
		{"two values", *assertion("a", JSONEquals, `1 2`), "expected"},
		{"object value", *assertion("a", JSONEquals, `{"b":1}`), "expected"},
		{"array value", *assertion("a", JSONNotEquals, `[1]`), "expected"},
		{"less_than a string", *assertion("a", JSONLessThan, `"10"`), "expected"},
		{"greater_than null", *assertion("a", JSONGreaterThan, `null`), "expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, err := ValidateJSONAssertion(tt.a)
			if tt.wantField == "" {
				if err != nil {
					t.Errorf("rejected a valid assertion: %s: %v", field, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %+v, want a %s problem", tt.a, tt.wantField)
			}
			if field != tt.wantField {
				t.Errorf("field = %q, want %q (%v)", field, tt.wantField, err)
			}
		})
	}
}

// A stream that sends more than the assertion's cap and then holds the
// connection open is judged on what it sent: the check returns at once, not
// after draining the rest of the stream until the deadline.
func TestHTTPJSONAssertionDoesNotDrainAnOpenOversizeStream(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxBodyRead+1)))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	m := monitor(srv.URL)
	m.Timeout = 2 * time.Second
	m.JSONAssertion = assertion("status", JSONEquals, `"up"`)
	began := time.Now()
	res := testChecker().Check(context.Background(), m)
	elapsed := time.Since(began)
	if res.OK || res.Kind != FailAssertion {
		t.Fatalf("result = ok %v kind %q, want an assertion failure", res.OK, res.Kind)
	}
	if res.Latency >= m.Timeout || elapsed >= m.Timeout {
		t.Errorf("latency %v, Check took %v; want both under the %v timeout", res.Latency, elapsed, m.Timeout)
	}
}

// A JSON body that stalls after its headers is a timeout, not a broken
// connection.
func TestHTTPJSONAssertionReportsAStalledBodyAsATimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	m := monitor(srv.URL)
	m.Timeout = 300 * time.Millisecond
	m.JSONAssertion = assertion("status", JSONEquals, `"up"`)
	res := testChecker().Check(context.Background(), m)
	if res.OK || res.Kind != FailTimeout {
		t.Fatalf("result = ok %v kind %q (%s), want a timeout", res.OK, res.Kind, res.Error)
	}
}
