package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

func TestSnapshotDiagnosticsFromRealHTTPChecks(t *testing.T) {
	var healthy atomic.Bool
	body := "<script>alert(1)</script>" + strings.Repeat("x", checker.MaxSnapshotBytes)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Request-Id", "request-1")
		w.Header().Set("Set-Cookie", "session=must-not-store")
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(body))
	}))
	defer target.Close()
	db := testDB(t)
	r := flappingRunner(t, db)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{
		Name: "response diagnostics", Type: "http", Target: target.URL,
		Enabled: true, CaptureResponse: true, Retries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if _, err := r.CheckNow(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	check()
	healthy.Store(true)
	check()
	healthy.Store(false)
	check()
	m.CaptureResponse = false
	check()
	got := recordedCaptureReasons(t, db)
	if len(got) != 4 || got[2] != "flapping" || got[3] != "disabled" {
		t.Fatalf("real HTTP decisions = %#v", got)
	}
	hbs, err := db.ListHeartbeats(t.Context(), m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var responses int
	for _, hb := range hbs {
		if hb.Response == nil {
			continue
		}
		responses++
		if !hb.Response.Truncated || !strings.HasPrefix(hb.Response.Body, "<script>") {
			t.Fatalf("HTTP capture lost body/truncation: %+v", hb.Response)
		}
		if hb.Response.Headers["X-Request-Id"] != "request-1" || hb.Response.Headers["Set-Cookie"] != "" {
			t.Fatalf("HTTP capture header boundary: %v", hb.Response.Headers)
		}
	}
	if responses != 1 {
		t.Fatalf("stored %d responses, want only the first", responses)
	}
}
