package checker

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// These deliberately synthetic fixtures demonstrate prefix tradeoffs, not a
// claim about the distribution of real error pages. Late causes are included
// so the retained 2 KiB boundary cannot be mistaken for a complete diagnosis.
func TestSyntheticSnapshotDiagnosticPrefixes(t *testing.T) {
	const cause = "upstream database unavailable"
	cases := []struct {
		name, body           string
		wantCause, truncated bool
	}{
		{"compact JSON", `{"error":"` + cause + `"}`, true, false},
		{"plain HTML", "<html><h1>" + cause + "</h1></html>", true, false},
		{"padded JSON", `{"context":"` + strings.Repeat("x", 1400) + `","error":"` + cause + `"}`, true, false},
		{"multilingual context", strings.Repeat("界", 600) + cause, true, false},
		{"late HTML cause", "<style>" + strings.Repeat("x", 3000) + "</style><h1>" + cause + "</h1>", false, true},
		{"script-heavy page", "<script>" + strings.Repeat("x", 9000) + "</script><h1>" + cause + "</h1>", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"X-Request-Id": {"synthetic-request"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			snap := captureResponse(Monitor{CaptureResponse: true}, resp, nil)
			if snap == nil {
				t.Fatal("no diagnostic snapshot")
			}
			if got := strings.Contains(snap.Body, cause); got != tc.wantCause {
				t.Fatalf("diagnostic cause retained = %v, want %v", got, tc.wantCause)
			}
			if snap.Truncated != tc.truncated {
				t.Fatalf("truncated = %v, want %v", snap.Truncated, tc.truncated)
			}
			if snap.Headers["X-Request-Id"] != "synthetic-request" {
				t.Fatal("request correlation lost")
			}
			for _, size := range []int{1024, 2048, 4096} {
				prefix := sanitiseBody([]byte(tc.body[:min(size, len(tc.body))]))
				t.Logf("synthetic body=%d candidate=%d retained=%d cause=%v", len(tc.body), size, len(prefix), strings.Contains(prefix, cause))
			}
		})
	}
}
