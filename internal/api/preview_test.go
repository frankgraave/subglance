package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// preview posts a preview request and returns the recorder.
func preview(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/preview",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

func decodePreview(t *testing.T, rec *httptest.ResponseRecorder) previewResponse {
	t.Helper()
	var out previewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// TestPreviewCheckRunsWithoutSavingAnything is the whole point of the endpoint:
// the probe happens, and the monitor list is exactly as empty afterwards as it
// was before.
func TestPreviewCheckRunsWithoutSavingAnything(t *testing.T) {
	srv, db := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{
		OK: true, StatusCode: 200, Latency: 120 * time.Millisecond,
		CheckedAt: time.Now().UTC(),
	}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"http","target":"https://example.com/health"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	got := decodePreview(t, rec)
	if !got.OK || got.StatusCode != 200 {
		t.Errorf("response = %+v, want a passing 200", got)
	}
	if prober.callCount() != 1 {
		t.Fatalf("prober called %d times, want 1", prober.callCount())
	}

	monitors, err := db.ListMonitors(t.Context())
	if err != nil {
		t.Fatalf("list monitors: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("got %d monitors after a preview, want 0: a preview must not save", len(monitors))
	}

	prober.mu.Lock()
	defer prober.mu.Unlock()
	if len(prober.recorded) != 0 {
		t.Errorf("preview recorded heartbeats for %v, want none", prober.recorded)
	}
	if prober.calls[0].ID != 0 {
		t.Errorf("probed monitor has id %d, want 0: a preview monitor does not exist", prober.calls[0].ID)
	}
	if prober.calls[0].Enabled {
		t.Error("probed monitor is enabled; that is the flag that makes CheckNow record")
	}
}

// TestPreviewCheckInfersTypeAndTarget covers the inference that makes the
// sixty-second promise possible. Pasting a hostname must be enough.
func TestPreviewCheckInfersTypeAndTarget(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantType   string
		wantTarget string
	}{
		{"bare hostname becomes https", `{"target":"example.com"}`, "http", "https://example.com"},
		{"explicit url is left alone", `{"target":"http://example.com/x"}`, "http", "http://example.com/x"},
		{"host and port is tcp", `{"target":"db.example.com:5432"}`, "tcp", "db.example.com:5432"},
		{"an explicit type wins", `{"type":"ping","target":"example.com"}`, "ping", "example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true, CheckedAt: time.Now().UTC()}}
			srv.WithProber(prober)

			rec := preview(t, srv, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			got := decodePreview(t, rec)
			if got.Type != tc.wantType || got.Target != tc.wantTarget {
				t.Errorf("resolved to %s %s, want %s %s",
					got.Type, got.Target, tc.wantType, tc.wantTarget)
			}

			prober.mu.Lock()
			defer prober.mu.Unlock()
			// The echo must describe what was really probed, not a label.
			if prober.calls[0].Type != tc.wantType {
				t.Errorf("probed type %q, but response says %q", prober.calls[0].Type, got.Type)
			}
			if prober.calls[0].Target != tc.wantTarget {
				t.Errorf("probed target %q, but response says %q", prober.calls[0].Target, got.Target)
			}
		})
	}
}

// TestPreviewCheckRejectsUnguessableTargets: inference that cannot be sure has
// to say so. A guess here monitors something other than what was meant.
func TestPreviewCheckRejectsUnguessableTargets(t *testing.T) {
	for _, body := range []string{
		`{"target":"postgres://db.example.com/app"}`,
		`{"target":""}`,
		`{"target":"not a host"}`,
		`{"type":"gopher","target":"example.com"}`,
		`{"type":"ping","target":"https://example.com"}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)

			rec := preview(t, srv, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times for a rejected target, want 0", prober.callCount())
			}
		})
	}
}

// TestPreviewCheckAppliesSavedDefaults: a preview that passes has to be
// evidence about the monitor that would be created, so it must run with the
// same defaults the store would apply.
func TestPreviewCheckAppliesSavedDefaults(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	if rec := preview(t, srv, `{"target":"https://example.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	prober.mu.Lock()
	defer prober.mu.Unlock()
	got := prober.calls[0]
	if got.TimeoutS != 10 {
		t.Errorf("timeout_s = %d, want the store default 10", got.TimeoutS)
	}
	if got.Method != "GET" {
		t.Errorf("method = %q, want the store default GET", got.Method)
	}
	if got.ExpectedStatus != "200-299" {
		t.Errorf("expected_status = %q, want the store default 200-299", got.ExpectedStatus)
	}
	if got.Retries != 1 {
		t.Errorf("retries = %d, want 1: a preview answers once", got.Retries)
	}
}

// TestPreviewCheckIsRateLimited: without an id to key on, this is the one
// authenticated way to make the server fetch an arbitrary URL in a loop.
func TestPreviewCheckIsRateLimited(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	if rec := preview(t, srv, `{"target":"https://example.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec.Code)
	}
	rec := preview(t, srv, `{"target":"https://other.example.com"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 has no Retry-After header, so a client cannot know when to try again")
	}
	if prober.callCount() != 1 {
		t.Errorf("prober ran %d times, want 1: the limited request must not probe", prober.callCount())
	}
}

// TestPreviewCheckWithoutProber: an API deployed without a checker answers
// honestly instead of panicking.
func TestPreviewCheckWithoutProber(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := preview(t, srv, `{"target":"https://example.com"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPreviewCheckRejectsUnknownFields: the same trap DisallowUnknownFields
// closes on create. A typo'd field name here would silently probe with a
// default and tell the user the monitor works.
func TestPreviewCheckRejectsUnknownFields(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"target":"https://example.com","timeout_seconds":3}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPreviewCheckRejectsTrailingJSON: json.Decoder stops at the first value
// and ignores whatever follows, so a body with two objects used to preview the
// first and report success for a request the caller never made.
func TestPreviewCheckRejectsTrailingJSON(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv,
		`{"target":"https://first.example.com"}{"target":"https://second.example.com"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if prober.callCount() != 0 {
		t.Errorf("prober ran %d times for a malformed body, want 0", prober.callCount())
	}
}

// TestPreviewAcceptsTrailingWhitespace guards the fix from overshooting: a
// body with a trailing newline is what every HTTP client sends, and rejecting
// it would break the endpoint for everyone in the name of strictness.
func TestPreviewAcceptsTrailingWhitespace(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv, "{\"target\":\"https://example.com\"}\n\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPreviewRejectsWhatCreateWouldReject is the endpoint's contract: a green
// preview has to mean the monitor will save. Before this, an unsupported
// method reached http.NewRequestWithContext and an out-of-range ssl_warn_days
// previewed fine and then failed the SQLite constraint on create — the user
// passed the test and could not keep the result.
func TestPreviewRejectsWhatCreateWouldReject(t *testing.T) {
	for name, body := range map[string]string{
		"unsupported method":    `{"target":"https://example.com","method":"FETCH"}`,
		"ssl_warn_days zero":    `{"target":"https://example.com","ssl_warn_days":0}`,
		"ssl_warn_days too big": `{"target":"https://example.com","ssl_warn_days":400}`,
		"unknown keyword_mode":  `{"target":"https://example.com","keyword_mode":"maybe"}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)

			rec := preview(t, srv, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times for an invalid setting, want 0", prober.callCount())
			}
		})
	}
}

// TestPreviewNormalisesMethodCase: a lowercase method is a valid one written
// casually, not an error. It has to reach the prober upper-cased, or the probe
// runs with something http.NewRequestWithContext treats as a different method.
func TestPreviewNormalisesMethodCase(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"target":"https://example.com","method":"head"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := prober.lastMonitor().Method; got != "HEAD" {
		t.Errorf("probed with method %q, want %q", got, "HEAD")
	}
}

// A 400 has to carry the field on the wire, not just inside the validator.
// The add-monitor form reads `field` to decide which input to mark invalid;
// if the key is dropped between the validator and the JSON, the message goes
// back to the global region and no unit test notices, because it still
// renders. This asserts the contract at the boundary a client actually sees.
func TestPreviewErrorNamesTheField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithProber(&fakeProber{result: checker.Result{OK: true}})

	tests := []struct {
		name  string
		body  string
		field string
	}{
		{"bad target for type", `{"type":"http","target":"ftp://example.com"}`, "target"},
		{"unresolvable target", `{"target":"redis://cache:6379"}`, "target"},
		{"unknown type", `{"type":"gopher","target":"example.com"}`, "type"},
		{"bad method", `{"target":"https://example.com","method":"FETCH"}`, "method"},
		{"bad keyword mode", `{"target":"https://example.com","keyword_mode":"maybe"}`, "keyword_mode"},
		{"timeout out of range", `{"target":"https://example.com","timeout_s":999}`, "timeout_s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := preview(t, srv, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var got errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
			}
			if got.Error == "" {
				t.Error("error message is empty")
			}
			if got.Field != tc.field {
				t.Errorf("field = %q, want %q (message: %s)", got.Field, tc.field, got.Error)
			}
		})
	}
}

// Malformed JSON is not about a field, and the key must be absent rather than
// empty: in JavaScript `""` and `undefined` both read as falsy on a naive
// check but differ on `"field" in body`, and a client that trusts the latter
// would render a global failure beneath an input named "".
func TestRequestLevelErrorsOmitTheField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithProber(&fakeProber{result: checker.Result{OK: true}})

	rec := preview(t, srv, `{"target":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if _, present := raw["field"]; present {
		t.Errorf("field key present on a malformed-JSON error: %s", rec.Body.String())
	}
}
