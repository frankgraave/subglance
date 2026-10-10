package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// Kuma ran the apprise command with its Apprise URL. The converted webhook
// posts a title and a body to Apprise API, where that URL is saved under a
// key; the URL is the target service's credentials, so neither it nor any
// part of it after the scheme reaches the file or the report.
func TestAppriseBecomesAWebhookToAppriseAPI(t *testing.T) {
	const secret = "kumaSecretAppriseToken42"
	cases := []struct {
		name   string
		config string
		notes  []string
		absent []string
	}{
		{"a service SubGlance has a channel type for", `{"type":"apprise","appriseURL":"tgram://123456:` + secret + `/-1001234"}`,
			[]string{"Kuma's Apprise URL sent to tgram://; SubGlance reaches Telegram without Apprise, as a channel type of its own"},
			[]string{"one title of its own", "names no service"}},
		{"a service with a webhook body in the documentation", `{"type":"apprise","appriseURL":"pover://user@` + secret + `"}`,
			[]string{"sent to pover://; SubGlance reaches Pushover without Apprise, as a webhook with the body the channel documentation gives"},
			[]string{"channel type"}},
		{"a service SubGlance does not reach", `{"type":"apprise","appriseURL":"lametric://` + secret + `@192.168.1.3/"}`,
			[]string{"Kuma's Apprise URL sent to lametric://"},
			[]string{"without Apprise", "192.168.1.3"}},
		// Apprise reads several URLs separated by commas or white space;
		// each scheme is named once, in order, upper case folded.
		{"several services", `{"type":"apprise","appriseURL":" Discord://1/` + secret + `, ntfys://` + secret + `@ntfy.example.org/up\n` +
			`ntfy://host/x  lametric://` + secret + ` discord://2/y"}`,
			[]string{"sent to discord://, ntfys://, ntfy:// and lametric://; SubGlance reaches Discord and ntfy without Apprise, as a channel type of its own"},
			[]string{"ntfy.example.org", "ntfy and ntfy"}},
		{"both kinds", `{"type":"apprise","appriseURL":"mailtos://user:` + secret + `@mail.example.org,matrixs://u:` + secret + `@hs.example.org/!room"}`,
			[]string{"SubGlance reaches email without Apprise, as a channel type of its own, and Matrix as a webhook with the body the channel documentation gives"},
			[]string{"mail.example.org", "hs.example.org"}},
		// What does not parse can still be a credential, so it is not quoted.
		{"an Apprise URL without a scheme", `{"type":"apprise","appriseURL":"` + secret + `"}`,
			[]string{"Kuma's Apprise URL names no service SubGlance can read"}, []string{"sent to"}},
		// A scheme has to start the URL: one inside another URL's path or
		// query is part of its credentials, not a second service.
		{"a scheme inside a URL", `{"type":"apprise","appriseURL":"json://host/?to=tgram://` + secret + `"}`,
			[]string{"Kuma's Apprise URL sent to json://"}, []string{"tgram", "Telegram"}},
		{"no Apprise URL", `{"type":"apprise"}`,
			[]string{"Kuma had no Apprise URL, so it sent nowhere"}, []string{"sent to"}},
		// Kuma passed a title only when one was set; white space alone is
		// trimmed to nothing, as Kuma's form left it empty.
		{"a title", `{"type":"apprise","appriseURL":"tgram://` + secret + `","title":"Kuma"}`,
			[]string{"Kuma sent every alert under one title of its own; the body sends each alert's summary as the title instead"}, nil},
		{"a blank title", `{"type":"apprise","appriseURL":"tgram://` + secret + `","title":"  "}`, nil, []string{"title of its own"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "webhook" || c.Config["url"] != configfile.Placeholder || c.Config["body"] != appriseBody || len(c.Config) != 2 {
				t.Errorf("channel = %s %q, want a webhook with the url withheld and the Apprise API body", c.Type, c.Config)
			}
			for _, want := range append(tc.notes, "posts to Apprise API", "fill in the url as "+appriseURLShape, "--allow-private-targets") {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			for _, unwanted := range append(tc.absent, secret) {
				if strings.Contains(notes, unwanted) {
					t.Errorf("notes %q contain %q", notes, unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{"url": "http://apprise:8000/notify/kuma"})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
