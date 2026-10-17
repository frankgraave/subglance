package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// readSnippet's result is stored as a channel's delivery error and shown
// under the channel, so it is valid UTF-8 whatever the receiver answered.
func TestReadSnippetStaysUTF8(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // "" means only check validity and the length bound
	}{
		{"short answer as written", "invalid_token", "invalid_token"},
		{"first line only", "bad request\n<html>…</html>", "bad request"},
		{"latin-1 byte becomes a replacement character", "unknown user m\xfcller", "unknown user m\uFFFDller"},
	}
	// Every offset of a two-, three- and four-byte character against the
	// 160-byte cut and the 200-byte read limit.
	for _, char := range []string{"ö", "€", "🙂"} {
		for pad := range len(char) {
			tests = append(tests, struct{ name, body, want string }{
				"cut " + char + " after " + strings.Repeat("a", pad), strings.Repeat("a", pad) + strings.Repeat(char, 120), "",
			})
			// Spaces trimmed at the start bring the read limit's end
			// under the cut, so a character split there would surface.
			tests = append(tests, struct{ name, body, want string }{
				"read limit " + char + " after " + strings.Repeat("a", pad),
				strings.Repeat(" ", 100) + strings.Repeat("a", pad) + strings.Repeat(char, 60), "",
			})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readSnippet(strings.NewReader(tt.body))
			if !utf8.ValidString(got) {
				t.Fatalf("readSnippet(%q) = %q, not UTF-8", tt.body, got)
			}
			if strings.ContainsRune(got, utf8.RuneError) && !strings.ContainsRune(tt.want, utf8.RuneError) {
				t.Errorf("readSnippet(%q) = %q, replaced a character that was only cut", tt.body, got)
			}
			if len(strings.TrimSuffix(got, "…")) > 160 {
				t.Errorf("readSnippet kept %d bytes, the bound is 160", len(got))
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("readSnippet(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// Through the path that uses it: a receiver refusing an alert in a language
// with accents gives a delivery error that can be stored and shown.
func TestRejectedDeliveryErrorIsUTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("a" + strings.Repeat("é", 100)))
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = httpSend(context.Background(), srv.Client(), req)
	if err == nil {
		t.Fatal("a 400 was delivered")
	}
	if msg := err.Error(); !utf8.ValidString(msg) || !strings.HasPrefix(msg, "endpoint rejected the alert (400): aé") {
		t.Errorf("delivery error = %q", msg)
	}
}
