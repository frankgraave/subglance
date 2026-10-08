package notifier

import (
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// isGSM7Only reports whether every character of s is in the GSM-7 alphabet,
// which is what keeps a message out of UCS-2 and its 70-character parts.
func isGSM7Only(s string) bool {
	for _, r := range s {
		if !isGSM7(r) {
			return false
		}
	}
	return true
}

func TestToGSM7(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"plain ASCII stays":            {"Production API", "Production API"},
		"GSM-7 accents stay":           {"Café Zürich señor", "Café Zürich señor"},
		"missing accents fold":         {"Crêpe brûlée ŁÓDŹ", "Crepe brulée LODZ"},
		"emoji and joiners go":         {"\U0001F525 API \U0001F469\u200d\U0001F4BB down \u2705", "API down"},
		"smart punctuation folds":      {"“quoted” – it’s…", "\"quoted\" - it's..."},
		"line breaks become one space": {"one\ntwo\r\nthree", "one two three"},
		"other scripts become ?":       {"API Москва", "API ??????"},
		"extension chars stay":         {"cost €5 [eu]", "cost €5 [eu]"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := toGSM7(tc.in)
			if got != tc.want {
				t.Errorf("toGSM7(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !isGSM7Only(got) {
				t.Errorf("toGSM7(%q) = %q still has characters outside GSM-7", tc.in, got)
			}
		})
	}
}

func TestSeptetLengthCountsTheExtensionTableTwice(t *testing.T) {
	if got := smsSeptetLen("a€{"); got != 5 {
		t.Errorf("septets(a€{) = %d, want 1 + 2 + 2", got)
	}
}

func TestFitSeptetsCutsAtAWordAndMarksIt(t *testing.T) {
	got := fitSeptets("connection refused by the upstream load balancer", 30)
	if got != "connection refused by the..." {
		t.Errorf("fit = %q", got)
	}
	if smsSeptetLen(got) > 30 {
		t.Errorf("fit = %d septets, want at most 30", smsSeptetLen(got))
	}
	// Extension characters count double, so a string of them is cut at
	// half the characters.
	if got := fitSeptets(strings.Repeat("€", 20), 10); smsSeptetLen(got) > 10 {
		t.Errorf("fit of euro signs = %q, %d septets, want at most 10", got, smsSeptetLen(got))
	}
}

func smsAlert(event state.Event) Alert {
	return Alert{
		MonitorID:   7,
		MonitorName: "Production API",
		Target:      "https://api.example.com/health",
		Event:       string(event),
		Cause:       "timeout",
		LastError:   "timeout after 10s",
		StartedAt:   time.Date(2026, 9, 29, 12, 3, 0, 0, time.UTC),
		At:          time.Date(2026, 9, 29, 12, 15, 0, 0, time.UTC),
	}
}

func TestSMSTextWording(t *testing.T) {
	down := smsAlert(state.EventIncidentConfirmed)
	up := smsAlert(state.EventIncidentResolved)
	reminder := smsAlert(state.EventIncidentReminder)
	grouped := smsAlert(state.EventIncidentConfirmed)
	grouped.GroupedNames = []string{"API", "Web", "DB"}

	tests := map[string]struct {
		a    Alert
		zone string
		want string
	}{
		"down, channel zone": {down, "Europe/Amsterdam", "DOWN Production API: timeout after 10s (since 14:03)"},
		"up":                 {up, "Europe/Amsterdam", "UP Production API after 12 min"},
		"reminder":           {reminder, "Europe/Amsterdam", "STILL DOWN Production API: timeout after 10s (since 14:03)"},
		"grouped":            {grouped, "Europe/Amsterdam", "DOWN 3 monitors: API, Web, DB"},
		"test button": {Alert{MonitorName: testAlertName, Event: string(state.EventIncidentResolved)}, "",
			"TEST SubGlance: this is a test message from the Send test button. No monitor is down."},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := smsText(tc.a, tc.zone, smsSeptets); got != tc.want {
				t.Errorf("text = %q\nwant   %q", got, tc.want)
			}
		})
	}
}

// TestSMSTextWithoutZoneNamesIt: without a channel zone the time is the
// server's, and it says which zone that is.
//
// The expectation is read from the process zone rather than pinned by writing
// time.Local: that variable is read by every time.Now() in the binary,
// including other tests' goroutines, so assigning it is a data race.
func TestSMSTextWithoutZoneNamesIt(t *testing.T) {
	a := smsAlert(state.EventIncidentConfirmed)
	want := "(since " + a.StartedAt.In(time.Local).Format("15:04 MST") + ")"
	got := smsText(a, "", smsSeptets)
	if !strings.HasSuffix(got, want) {
		t.Errorf("text = %q, want it to end in %q, the time with its zone", got, want)
	}
}

// TestSMSTextIsAlwaysOneGSM7Part is the rule that decides what an alert costs:
// whatever the monitor is called and whatever the error says, the message is
// at most 160 septets and never leaves GSM-7.
func TestSMSTextIsAlwaysOneGSM7Part(t *testing.T) {
	long := strings.Repeat("Very long monitor name \U0001F680 with ünïcödé ", 8)
	cases := []Alert{
		smsAlert(state.EventIncidentConfirmed),
		smsAlert(state.EventIncidentResolved),
	}
	for _, base := range cases {
		for _, name := range []string{long, "API \U0001F525", strings.Repeat("€", 200), "Москва"} {
			for _, errText := range []string{"", strings.Repeat("upstream said no \U0001F645 ", 30)} {
				a := base
				a.MonitorName, a.LastError = name, errText
				for _, withheld := range []int{0, 1, 37} {
					got := smsMessage(a, "Europe/Amsterdam", withheld)
					if n := smsSeptetLen(got); n > smsSeptets {
						t.Errorf("%d septets, want at most %d: %q", n, smsSeptets, got)
					}
					if !isGSM7Only(got) {
						t.Errorf("message leaves GSM-7: %q", got)
					}
					if !strings.HasPrefix(got, "DOWN ") && !strings.HasPrefix(got, "UP ") {
						t.Errorf("message does not lead with its status: %q", got)
					}
					if withheld > 0 && !strings.Contains(got, "not sent by SMS") {
						t.Errorf("message drops the held-back count: %q", got)
					}
				}
			}
		}
	}
}

// TestSMSTextShortensTheNameNotTheStatus: a long name gives way, the status
// word and the start time do not.
func TestSMSTextShortensTheNameNotTheStatus(t *testing.T) {
	a := smsAlert(state.EventIncidentConfirmed)
	a.MonitorName = strings.Repeat("Customer portal backend ", 6)
	got := smsText(a, "Europe/Amsterdam", smsSeptets)
	if !strings.HasPrefix(got, "DOWN Customer portal") || !strings.HasSuffix(got, "(since 14:03)") {
		t.Errorf("text = %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("text = %q, want the cut marked", got)
	}
}
