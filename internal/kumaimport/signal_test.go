package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// The numbers are in ranges reserved for fiction: 555-01xx in North America
// and 06-12345678's neighbourhood is the Dutch documentation example.
const (
	signalFrom = "+12025550123"
	signalTo   = "+31612345678"
)

// Kuma posts {message, number, recipients} to signal-cli-rest-api. The
// converted webhook posts to the same address with a body of its own; the
// body names phone numbers, so it is left to fill in and the report masks
// the numbers Kuma used.
func TestSignalBecomesAWebhookToTheSameAPI(t *testing.T) {
	cases := []struct {
		name   string
		config string
		url    string // "" when the url is withheld
		notes  []string
		absent []string
	}{
		{"an address on a local name", `{"type":"signal","signalURL":"http://signal-api:8080/v2/send",` +
			`"signalNumber":"` + signalFrom + `","signalRecipients":"` + signalTo + `"}`,
			"http://signal-api:8080/v2/send",
			[]string{"signal-api is a local name", "--allow-private-targets",
				"the number Kuma sent from (+1 2 •••• 0123)", "the recipients it sent to (+31 6 •••• 5678)"}, nil},
		{"a public address", `{"type":"signal","signalURL":"https://signal.example.org/v2/send","signalNumber":"` + signalFrom + `"}`,
			"https://signal.example.org/v2/send", []string{"(+1 2 •••• 0123)"},
			[]string{"allow-private-targets", "fill in the url", "recipients it sent to"}},
		{"a private address", `{"type":"signal","signalURL":"http://192.168.1.5:8080/v2/send"}`,
			"http://192.168.1.5:8080/v2/send", []string{"192.168.1.5 is private or reserved"},
			[]string{"sent from", "sent to"}},
		{"several recipients and a group", `{"type":"signal","signalURL":"https://signal.example.org/v2/send",` +
			`"signalRecipients":" ` + signalTo + ` , +1 202 555 0199,group.c2lnbmFs ,"}`,
			"https://signal.example.org/v2/send",
			[]string{"the recipients it sent to (+31 6 •••• 5678, +1 2 •••• 0199, a group)"}, []string{"c2lnbmFs"}},
		{"a recipient that is not a number", `{"type":"signal","signalURL":"https://signal.example.org/v2/send",` +
			`"signalRecipients":"ops-team"}`,
			"https://signal.example.org/v2/send", []string{"the recipients it sent to (••••)"}, []string{"ops-team"}},
		{"a fragment is dropped", `{"type":"signal","signalURL":"https://signal.example.org/v2/send#top"}`,
			"https://signal.example.org/v2/send", nil, nil},
		// Kuma's request escaped the braces in a path, and so does the
		// url: they are a literal part of the path, not a placeholder.
		{"braces in the path", `{"type":"signal","signalURL":"https://signal.example.org/{{x}}/v2/send"}`,
			"https://signal.example.org/%7B%7Bx%7D%7D/v2/send", nil, nil},
		{"credentials in the address", `{"type":"signal","signalURL":"https://admin:hunter2@signal.example.org/v2/send"}`,
			"", []string{"carried a user name, a password or a query string", signalURLShape}, []string{"hunter2"}},
		{"a query in the address", `{"type":"signal","signalURL":"https://signal.example.org/v2/send?key=hunter2"}`,
			"", []string{"carried a user name, a password or a query string"}, []string{"hunter2"}},
		{"credentials in an address that does not parse", `{"type":"signal","signalURL":"https://admin:hunter2@signal.example.org:bad"}`,
			"", []string{"Kuma's Signal address is not one SubGlance can post to"}, []string{"hunter2"}},
		{"an address without a scheme", `{"type":"signal","signalURL":"signal-api:8080/v2/send"}`,
			"", []string{`address "signal-api:8080/v2/send" is not one SubGlance can post to`, signalURLShape}, nil},
		{"an address that is not http", `{"type":"signal","signalURL":"ftp://signal.example.org"}`,
			"", []string{`address "ftp://signal.example.org" is not one SubGlance can post to`}, nil},
		{"a port with no host", `{"type":"signal","signalURL":"http://:8080/v2/send"}`,
			"", []string{`address "http://:8080/v2/send" is not one SubGlance can post to`}, nil},
		{"no address", `{"type":"signal"}`, "", []string{"is not one SubGlance can post to"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			wantURL := tc.url
			if wantURL == "" {
				wantURL = configfile.Placeholder
			}
			if c.Type != "webhook" || c.Config["url"] != wantURL || c.Config["body"] != configfile.Placeholder || len(c.Config) != 2 {
				t.Errorf("channel = %s %v, want a webhook to %s with the body withheld", c.Type, c.Config, wantURL)
			}
			for _, want := range append(tc.notes, "fill in the body as "+strings.TrimSpace(signalBody)) {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			// A phone number is personal data: neither the file nor the
			// report holds one in full, in any form Kuma might have kept.
			for _, unwanted := range append(tc.absent, "2025550123", "612345678", "5550199", "555 0199") {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["url"], unwanted) {
					t.Errorf("notes %q or url %q contain %q", notes, c.Config["url"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{
				"url": "https://signal.example.org/v2/send",
				"body": strings.NewReplacer("<Signal number>", signalFrom, "<recipient>", signalTo).
					Replace(signalBody),
			})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
