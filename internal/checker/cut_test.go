package checker

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCutBytesNeverSplitsACharacter(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short enough", "héllo", 6, "héllo"},
		{"ascii", "abcdef", 3, "abc"},
		{"cut after a whole character", "aé", 3, "aé"},
		{"cut inside a two-byte character", "aéb", 2, "a"},
		{"cut inside a three-byte character", "a€", 2, "a"},
		{"cut after the second byte of three", "a€", 3, "a"},
		{"cut inside a four-byte character", "ab🙂", 5, "ab"},
		{"nothing whole fits", "é", 1, ""},
		{"zero", "abc", 0, ""},
		// Not UTF-8 to begin with: the cut stays near n instead of walking
		// back to the start of the text.
		{"continuation bytes only", strings.Repeat("\x80", 10), 6, strings.Repeat("\x80", 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cutBytes(tt.s, tt.n); got != tt.want {
				t.Errorf("cutBytes(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

// A failure message quotes a value from the response or the zone and cuts
// it at a byte count. Every offset of a two-, three- and four-byte character
// is tried against the cut, so a message that keeps half a character fails
// here whichever byte the cut lands on.
func TestShortenedValuesInFailureMessagesStayUTF8(t *testing.T) {
	for _, char := range []string{"é", "€", "🙂"} {
		for pad := range len(char) {
			value := strings.Repeat("a", pad) + strings.Repeat(char, 200)

			msg := evaluateJSONAssertion(*assertion("name", JSONEquals, `"x"`), []byte(`{"name":"`+value+`"}`))
			if !utf8.ValidString(msg) || !strings.HasSuffix(msg, `…, expected "x"`) {
				t.Errorf("json assertion, %q after %d byte(s): %q", char, pad, msg)
			}

			for _, typ := range []string{DNSRecordTXT, DNSRecordCNAME} {
				msg := describeDNSMismatch(typ, "example.test", []string{"v=1"}, []string{value})
				if !utf8.ValidString(msg) || !strings.Contains(msg, "…") {
					t.Errorf("%s mismatch, %q after %d byte(s): %q", typ, char, pad, msg)
				}
				if strings.Contains(msg, `\x`) {
					t.Errorf("%s mismatch quotes a split byte, %q after %d byte(s): %q", typ, char, pad, msg)
				}
			}
		}
	}
}

// The same through a check: a TXT record of accented text, as a
// site-verification or a human-written record holds, fails with a message
// that can be stored and shown.
func TestDNSMismatchOnAccentedRecordIsUTF8(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", &dnsmessage.TXTResource{TXT: []string{"a" + strings.Repeat("ü", 60)}})
	res := openChecker().Check(context.Background(), f.monitor("example.test", DNSRecordTXT, "v=spf1 -all"))
	if res.OK || res.Kind != FailDNSMismatch {
		t.Fatalf("OK=%v kind=%q, want a mismatch", res.OK, res.Kind)
	}
	if !utf8.ValidString(res.Error) || strings.Contains(res.Error, `\x`) {
		t.Errorf("mismatch message keeps half a character: %q", res.Error)
	}
}
