package notifier

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		in, cc, want string
	}{
		{"+31 6 1234 5678", "", "+31612345678"},
		{"+31 (0)6-1234-5678", "", "+31612345678"},
		{"0031 6 12345678", "", "+31612345678"},
		{"06-12345678", "+31", "+31612345678"},
		{"06 1234 5678", "+31", "+31612345678"},
		{"(415) 555.0100", "+1", "+14155550100"},
		{"+44 7700 900123", "+31", "+447700900123"}, // an explicit code wins
	}
	for _, tc := range tests {
		got, err := normalizePhone(tc.in, tc.cc)
		if err != nil || got != tc.want {
			t.Errorf("normalizePhone(%q, %q) = %q, %v; want %q", tc.in, tc.cc, got, err, tc.want)
		}
	}
}

func TestNormalizePhoneRefuses(t *testing.T) {
	tests := map[string]struct{ in, cc, wantInError string }{
		"no country code":  {"06 1234 5678", "", "no country code"},
		"letters":          {"+31 6 CALL ME", "", "not a phone number"},
		"too short":        {"+31 12", "", "not a phone number"},
		"too long":         {"+31 6 1234 5678 9012 34", "", "not a phone number"},
		"zero country":     {"+0612345678", "", "not a phone number"},
		"masked read-back": {"+31 6 •••• 5678", "", "masked"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := normalizePhone(tc.in, tc.cc)
			if err == nil {
				t.Fatalf("normalizePhone(%q) accepted it", tc.in)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantInError)
			}
			// The person typing needs to see which entry was wrong.
			if !strings.Contains(err.Error(), strings.TrimSpace(tc.in)) {
				t.Errorf("error = %q, want it to quote the number", err)
			}
		})
	}
}

func TestSMSRecipients(t *testing.T) {
	got, err := smsRecipients(map[string]string{
		"numbers":      "06 1234 5678,\n+44 7700 900123; 0031 6 87654321\n",
		"country_code": "31",
	})
	if err != nil {
		t.Fatalf("smsRecipients: %v", err)
	}
	want := "+31612345678 +447700900123 +31687654321"
	if strings.Join(got, " ") != want {
		t.Errorf("numbers = %v, want %s", got, want)
	}

	for name, cfg := range map[string]map[string]string{
		"none":           {"numbers": " , "},
		"duplicate":      {"numbers": "+31612345678, 06-12345678", "country_code": "+31"},
		"too many":       {"numbers": manyNumbers(smsMaxRecipients + 1)},
		"bad cc":         {"numbers": "0612345678", "country_code": "NL"},
		"one bad number": {"numbers": "+31612345678, 12"},
	} {
		if _, err := smsRecipients(cfg); err == nil {
			t.Errorf("%s: accepted %v", name, cfg)
		}
	}
	if _, err := smsRecipients(map[string]string{"numbers": manyNumbers(smsMaxRecipients)}); err != nil {
		t.Errorf("exactly %d numbers refused: %v", smsMaxRecipients, err)
	}
}

func manyNumbers(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("+316123456%02d", i)
	}
	return strings.Join(parts, ",")
}

func TestMaskPhone(t *testing.T) {
	tests := map[string]string{
		"+31612345678":  "+31 6 •••• 5678",
		"+14155550100":  "+1 4 •••• 0100",
		"+447700900123": "+44 7 •••• 0123",
		"+353861234567": "+353 8 •••• 4567",
		"+3521234":      "+352 •••• 34", // short national number
		"not a number":  "••••",
	}
	for in, want := range tests {
		if got := maskPhone(in); got != want {
			t.Errorf("maskPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskSMSNumbersHidesEveryNumber(t *testing.T) {
	got := MaskSMSNumbers("06 1234 5678, +44 7700 900123, rubbish", "+31")
	if got != "+31 6 •••• 5678, +44 7 •••• 0123, ••••" {
		t.Errorf("masked = %q", got)
	}
	for _, digits := range []string{"1234", "7700", "900"} {
		if strings.Contains(got, digits) {
			t.Errorf("masked form %q still shows %q", got, digits)
		}
	}
}

func TestScrubPhones(t *testing.T) {
	got := scrubPhones("The 'To' number +31612345678 is not valid (31612345678)", []string{"+31612345678"})
	if strings.Contains(got, "12345678") {
		t.Errorf("scrubbed = %q, still has the number", got)
	}
}

// TestScrubPhonesNationalForms: a provider may quote the number the way it is
// dialled at home, with or without the trunk zero, and more than once. Every
// one goes; an error code that is not the number stays.
func TestScrubPhonesNationalForms(t *testing.T) {
	in := "error 21211: 0612345678 0612345678 unreachable, tried 612345678"
	got := scrubPhones(in, []string{"+31612345678"})
	if strings.Contains(got, "12345678") {
		t.Errorf("scrubbed = %q, still has the number", got)
	}
	if strings.Count(got, "+31 6 •••• 5678") != 3 {
		t.Errorf("scrubbed = %q, want each of the three occurrences masked", got)
	}
	if !strings.Contains(got, "error 21211:") {
		t.Errorf("scrubbed = %q, lost the provider's error code", got)
	}

	// A longer run of digits that merely contains the number is not it.
	if got := scrubPhones("reference 90612345678", []string{"+31612345678"}); got != "reference 90612345678" {
		t.Errorf("scrubbed = %q, want a longer digit run left alone", got)
	}
}
