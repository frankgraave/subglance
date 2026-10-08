package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// kumaTwilioSID has the shape of a Twilio account SID. It is assembled at run
// time so that no SID-shaped literal sits in the source for a secret scanner
// to report.
var kumaTwilioSID = "AC" + strings.Repeat("0123456789abcdef", 2)

// kumaTwilio is a Kuma Twilio notification as Kuma's form saves it, with
// the fields of the pinned 1.23.16 and 2.5.5 sources. extra adds or
// replaces fields.
func kumaTwilio(extra string) string {
	cfg := `{"name":"SMS on-call","type":"twilio","isDefault":false,"applyExisting":false,` +
		`"twilioAccountSID":"` + kumaTwilioSID + `","twilioAuthToken":"kuma-secret-twilio",` +
		`"twilioFromNumber":"+1 202-555-0100","twilioToNumber":"+12025550123"`
	if extra != "" {
		cfg += "," + extra
	}
	return cfg + "}"
}

func TestTwilioBecomesAnSMSChannel(t *testing.T) {
	cases := []struct {
		name   string
		config string
		from   string
		notes  []string
		absent []string
	}{
		{"account SID and auth token", kumaTwilio(""), "+12025550100",
			[]string{"(+1 2 •••• 0123)", "at most 10 alerts an hour"},
			[]string{"API key", "messaging service"}},
		{"API key", kumaTwilio(`"twilioApiKey":"SK0123"`), "+12025550100",
			[]string{"signed in with an API key", "not the API key secret"}, nil},
		{"messaging service", kumaTwilio(`"twilioMessagingServiceSID":"MG0123"`), "+12025550100",
			[]string{"messaging service"}, []string{"API key"}},
		{"sender name", kumaTwilio(`"twilioFromNumber":"Ops Alerts"`), "Ops Alerts", nil, nil},
		{"00 for the plus", kumaTwilio(`"twilioFromNumber":"0044 7700 900123"`), "+447700900123", nil, nil},
		{"no recipient", kumaTwilio(`"twilioToNumber":""`), "+12025550100",
			[]string{"fill in numbers with the phone number to send to"}, []string{"••••"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "sms" || len(c.Config) != 5 ||
				c.Config["provider"] != notifier.SMSProviderTwilio || c.Config["account_sid"] != kumaTwilioSID ||
				c.Config["from"] != tc.from || c.Config["auth_token"] != configfile.Placeholder ||
				c.Config["numbers"] != configfile.Placeholder {
				t.Errorf("channel = %+v", c)
			}
			// The recipient is personal data and the token a credential;
			// neither may reach the file, the report included.
			for _, leak := range []string{"kuma-secret", "2025550123", "SK0123", "MG0123"} {
				if strings.Contains(notes, leak) {
					t.Errorf("notes = %q, contain %q", notes, leak)
				}
			}
			for _, want := range tc.notes {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(notes, unwanted) {
					t.Errorf("notes = %q, should not say %q", notes, unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{"auth_token": "token", "numbers": "+12025550123"})
			if err := notifier.ValidateSMSConfig(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}

// A Twilio notification SubGlance's SMS channel would refuse is skipped with
// the reason, rather than written into a file whose dry run then fails.
func TestTwilioThatCannotBeSentIsSkipped(t *testing.T) {
	cases := []struct {
		name, config, reason string
	}{
		{"no account SID", kumaTwilio(`"twilioAccountSID":""`), "account_sid"},
		{"not an account SID", kumaTwilio(`"twilioAccountSID":"SK0123"`), "account_sid"},
		{"no sender", kumaTwilio(`"twilioFromNumber":""`), "from is required"},
		{"sender name too long", kumaTwilio(`"twilioFromNumber":"Operations Team"`), "from must be"},
		{"not a phone number", kumaTwilio(`"twilioFromNumber":"+1 (202)"`), "not a phone number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			_, ok := convertChannel(row{"id": int64(1), "name": "SMS", "active": int64(1), "config": tc.config}, &res)
			if ok {
				t.Fatal("converted, want skipped")
			}
			if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "Twilio settings") ||
				!strings.Contains(res.Skipped[0].Reason, tc.reason) {
				t.Errorf("skipped = %+v, want a reason naming %q", res.Skipped, tc.reason)
			}
			if strings.Contains(notesOf(res), "kuma-secret") {
				t.Errorf("the report contains the auth token: %s", notesOf(res))
			}
		})
	}
}
