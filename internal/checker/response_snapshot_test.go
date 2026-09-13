package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// captureMonitor is a monitor with response capture switched on.
func captureMonitor(target string) Monitor {
	m := monitor(target)
	m.CaptureResponse = true
	return m
}

func TestCaptureKeepsTheBodyOfAFailedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "120")
		w.Header().Set("Set-Cookie", "session=super-secret-token; Path=/")
		w.Header().Set("Authorization", "Bearer super-secret-token")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"upstream database timeout"}`))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.OK {
		t.Fatal("check passed, want a status failure")
	}
	if res.Response == nil {
		t.Fatal("no response captured; the body that explains the failure was discarded")
	}
	if !strings.Contains(res.Response.Body, "upstream database timeout") {
		t.Errorf("body = %q, want the upstream detail", res.Response.Body)
	}
	if res.Response.Truncated {
		t.Error("a short body was reported as truncated")
	}
	if got := res.Response.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := res.Response.Headers["Retry-After"]; got != "120" {
		t.Errorf("Retry-After = %q, want 120", got)
	}

	// The allowlist is the security boundary: a credential must never reach
	// the database, whatever the monitored service chooses to send.
	for name, value := range res.Response.Headers {
		if strings.Contains(strings.ToLower(value), "super-secret-token") {
			t.Errorf("header %s leaked a credential: %q", name, value)
		}
	}
	if _, present := res.Response.Headers["Set-Cookie"]; present {
		t.Error("Set-Cookie was stored")
	}
	if _, present := res.Response.Headers["Authorization"]; present {
		t.Error("Authorization was stored")
	}
}

func TestCaptureOffKeepsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("private detail"))
	}))
	defer srv.Close()

	// monitor() leaves CaptureResponse at false, which is what a monitor that
	// opted out looks like.
	res := testChecker().Check(context.Background(), monitor(srv.URL))

	if res.OK {
		t.Fatal("check passed, want a status failure")
	}
	if res.Response != nil {
		t.Fatalf("captured %q with capture disabled", res.Response.Body)
	}
}

func TestCaptureKeepsNothingOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("all good"))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if !res.OK {
		t.Fatalf("check failed: %s", res.Error)
	}
	if res.Response != nil {
		t.Fatal("a successful check stored a response body")
	}
}

func TestCaptureTruncatesALongBody(t *testing.T) {
	// Deliberately far larger than the cap, and made of a repeating pattern so
	// a wrong slice shows up as a wrong length rather than as plausible text.
	long := strings.Repeat("0123456789", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Response == nil {
		t.Fatal("no response captured")
	}
	if got := len(res.Response.Body); got != MaxSnapshotBytes {
		t.Errorf("body length = %d, want the cap %d", got, MaxSnapshotBytes)
	}
	if !res.Response.Truncated {
		t.Error("a body past the cap was not marked truncated")
	}
}

// A body of exactly the cap is whole, not truncated. The off-by-one matters:
// claiming truncation tells a reader the answer continues when it does not.
func TestCaptureExactlyAtTheCapIsNotTruncated(t *testing.T) {
	body := strings.Repeat("a", MaxSnapshotBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Response == nil {
		t.Fatal("no response captured")
	}
	if res.Response.Truncated {
		t.Error("a body of exactly the cap was reported as truncated")
	}
	if len(res.Response.Body) != MaxSnapshotBytes {
		t.Errorf("body length = %d, want %d", len(res.Response.Body), MaxSnapshotBytes)
	}
}

// The cut lands mid-character unless the code trims back to a rune boundary.
// The padding length is chosen so that the cap falls inside a 3-byte rune:
// with 2046 ASCII bytes before it, byte 2048 is the second byte of "€".
func TestCaptureCutsOnARuneBoundary(t *testing.T) {
	body := strings.Repeat("a", MaxSnapshotBytes-2) + strings.Repeat("€", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Response == nil {
		t.Fatal("no response captured")
	}
	if !utf8.ValidString(res.Response.Body) {
		t.Errorf("body is not valid UTF-8: %q", res.Response.Body[len(res.Response.Body)-8:])
	}
	// The partial rune is dropped rather than replaced, so the text ends on
	// the last whole character.
	if got := len(res.Response.Body); got != MaxSnapshotBytes-2 {
		t.Errorf("body length = %d, want %d (the cap minus the partial rune)",
			got, MaxSnapshotBytes-2)
	}
}

// Binary is not text. A gzip frame or an image must not put invalid bytes into
// a STRICT TEXT column, whatever the monitored service answers with.
func TestCaptureScrubsBinaryBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte{0x1f, 0x8b, 0x08, 0xff, 0xfe, 'o', 'k'})
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Response == nil {
		t.Fatal("no response captured")
	}
	if !utf8.ValidString(res.Response.Body) {
		t.Errorf("body is not valid UTF-8: %q", res.Response.Body)
	}
}

// A keyword failure reads the body itself. The snapshot has to come from that
// same read: the body is a stream, and reading it twice would store nothing.
func TestCaptureOnKeywordFailureReusesTheBodyAlreadyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("service reports: degraded"))
	}))
	defer srv.Close()

	m := captureMonitor(srv.URL)
	m.Keyword = "healthy"
	m.KeywordMode = KeywordMustContain

	res := testChecker().Check(context.Background(), m)

	if res.OK {
		t.Fatal("check passed, want a keyword failure")
	}
	if res.Kind != FailKeyword {
		t.Fatalf("kind = %q, want %q", res.Kind, FailKeyword)
	}
	if res.Response == nil {
		t.Fatal("no response captured on a keyword failure")
	}
	if res.Response.Body != "service reports: degraded" {
		t.Errorf("body = %q, want the body the keyword check read", res.Response.Body)
	}
}

// A failure that never reached a response has nothing to capture, and must not
// invent an empty snapshot that a UI would then render as an empty panel.
func TestCaptureIsAbsentWhenThereIsNoResponse(t *testing.T) {
	res := testChecker().Check(context.Background(),
		captureMonitor("http://127.0.0.1:1/nothing-listens-here"))

	if res.OK {
		t.Fatal("check passed against a closed port")
	}
	if res.Response != nil {
		t.Fatalf("captured a response from a connection that never opened: %+v", res.Response)
	}
}

// An empty body with no allowlisted headers teaches nothing, so it is not
// stored at all.
func TestCaptureSkipsAnEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content-Type and Date are set by net/http unless suppressed.
		w.Header()["Content-Type"] = nil
		w.Header()["Date"] = nil
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Response != nil {
		t.Fatalf("stored a snapshot with nothing in it: %+v", res.Response)
	}
}

// The latency recorded for a check must include the cost of reading the
// snapshot, or the number reported to the user understates what the check did.
func TestCaptureCostIsInsideTheMeasuredLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, strings.Repeat("x", MaxSnapshotBytes))
	}))
	defer srv.Close()

	res := testChecker().Check(context.Background(), captureMonitor(srv.URL))

	if res.Latency <= 0 {
		t.Fatal("latency was not measured on a captured failure")
	}
}
