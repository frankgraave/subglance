package kumaimport

import (
	"strings"
	"testing"
)

// Kuma 2's Save HTTP Error Response becomes capture_response, written out
// both ways; a Kuma 1.23 monitor, which has no such column, and a type Kuma
// offers no such switch on, come over without it. What Kuma kept beyond what
// SubGlance keeps is listed.
func TestResponseCaptureFollowsKuma(t *testing.T) {
	kuma2 := func(over row) row {
		r := httpRow(row{"save_error_response": int64(1), "save_response": int64(0), "response_max_length": int64(1024)})
		for k, v := range over {
			r[k] = v
		}
		return r
	}
	on, off := true, false
	cases := []struct {
		name string
		mon  row
		want *bool
		note string // "" when no note about responses is expected
	}{
		{"error responses kept", kuma2(nil), &on, ""},
		{"error responses not kept", kuma2(row{"save_error_response": int64(0)}), &off, ""},
		{"not kept, with a limit and passing responses set", kuma2(row{"save_error_response": int64(0),
			"save_response": int64(1), "response_max_length": int64(0)}), &off, ""},
		{"on a keyword monitor", kuma2(row{"type": "keyword", "keyword": "ok", "save_error_response": int64(0)}), &off, ""},
		{"on a json-query monitor", kuma2(row{"type": "json-query", "json_path": "status", "expected_value": "ok",
			"save_error_response": int64(0)}), &off, ""},
		{"passing responses kept as well", kuma2(row{"save_response": int64(1)}), &on,
			"Kuma also kept the response of a passing check; SubGlance keeps the response of a failed check only"},
		{"no limit", kuma2(row{"response_max_length": int64(0)}), &on,
			"Kuma's Response Max Length was 0, which Kuma describes as no limit; SubGlance keeps the first 2048 bytes of a failed check's response"},
		{"a limit above SubGlance's", kuma2(row{"response_max_length": int64(65536)}), &on,
			"Kuma's Response Max Length was 65536 characters; SubGlance keeps the first 2048 bytes"},
		{"a limit below Kuma's default", kuma2(row{"response_max_length": int64(200)}), &on,
			"Kuma's Response Max Length was 200 characters; SubGlance keeps the first 2048 bytes"},
		{"a raised limit SubGlance keeps in full", kuma2(row{"response_max_length": int64(2048)}), &on, ""},
		{"no columns (Kuma 1.23)", httpRow(nil), nil, ""},
		{"a port monitor", portRow(row{"save_error_response": int64(0)}), nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: []row{tc.mon}})
			if len(res.Document.Monitors) != 1 {
				t.Fatalf("monitor not converted: %s", notesOf(res))
			}
			got := res.Document.Monitors[0].CaptureResponse
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("capture_response = %v, want it left out", *got)
			case tc.want != nil && got == nil:
				t.Errorf("capture_response left out, want %v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("capture_response = %v, want %v", *got, *tc.want)
			}
			notes := notesOf(res)
			if tc.note == "" {
				if strings.Contains(notes, "response") {
					t.Errorf("unexpected note about responses:\n%s", notes)
				}
				return
			}
			if !strings.Contains(notes, tc.note) {
				t.Errorf("report lacks %q:\n%s", tc.note, notes)
			}
		})
	}
}
