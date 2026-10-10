package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// wahaChat is signalTo as WAHA writes it: the country code and the number,
// without the plus, then @c.us.
const wahaChat = "31612345678@c.us"

// Kuma posts {session, chatId, text} to WAHA's /api/sendText. The converted
// webhook posts to the same address with a body of its own; the body names
// a phone number, so it is left to fill in and the report masks the chat
// Kuma used. The API key goes in the withheld headers.
func TestWAHABecomesAWebhookToTheSameServer(t *testing.T) {
	const key = "kumaSecretWahaKey42"
	cases := []struct {
		name    string
		config  string
		url     string // "" when the url is withheld
		headers bool
		notes   []string
		absent  []string
	}{
		{"Kuma's example address, with a key", `{"type":"waha","wahaApiUrl":"http://localhost:3000/","wahaApiKey":"` + key + `",` +
			`"wahaSession":"default","wahaChatId":"` + wahaChat + `"}`,
			"http://localhost:3000/api/sendText", true,
			[]string{"localhost is a local name", "--allow-private-targets", `fill in the headers as "X-Api-Key: <api key>"`,
				`the session Kuma used ("default")`, "the chat it sent to (+31 6 •••• 5678)"}, nil},
		{"a public address under a path, no key", `{"type":"waha","wahaApiUrl":"https://wa.example.org/waha//",` +
			`"wahaSession":"ops","wahaChatId":"0031612345678"}`,
			"https://wa.example.org/waha/api/sendText", false,
			[]string{`the session Kuma used ("ops")`, "the chat it sent to (+31 6 •••• 5678)"},
			[]string{"allow-private-targets", "fill in the url", "fill in the headers"}},
		{"a private address, a number without a domain", `{"type":"waha","wahaApiUrl":"http://192.168.1.7:3000",` +
			`"wahaChatId":"31612345678"}`,
			"http://192.168.1.7:3000/api/sendText", false,
			[]string{"192.168.1.7 is private or reserved", "the chat it sent to (+31 6 •••• 5678)"}, []string{"session Kuma used"}},
		// A group id can begin with the number of whoever made the group,
		// so it is not shown at all.
		{"a group", `{"type":"waha","wahaApiUrl":"https://wa.example.org","wahaChatId":"120363042318711234@g.us"}`,
			"https://wa.example.org/api/sendText", false, []string{"the chat it sent to (a group)"}, []string{"120363042318711234"}},
		{"a group in the older form", `{"type":"waha","wahaApiUrl":"https://wa.example.org","wahaChatId":"31612345678-1618740914"}`,
			"https://wa.example.org/api/sendText", false, []string{"the chat it sent to (a group)"}, []string{"1618740914"}},
		{"a chat that is not a number", `{"type":"waha","wahaApiUrl":"https://wa.example.org","wahaChatId":"ops-team@lid"}`,
			"https://wa.example.org/api/sendText", false, []string{"the chat it sent to (••••)"}, []string{"ops-team"}},
		// Kuma's request escaped the braces in a path, and so does the
		// url: they are a literal part of the path, not a placeholder.
		{"braces in the path", `{"type":"waha","wahaApiUrl":"https://wa.example.org/{{x}}"}`,
			"https://wa.example.org/%7B%7Bx%7D%7D/api/sendText", false, nil, nil},
		{"credentials in the address", `{"type":"waha","wahaApiUrl":"https://admin:hunter2@wa.example.org"}`,
			"", false, []string{"carried a user name, a password or a query string", wahaURLShape}, []string{"hunter2"}},
		{"a query in the address", `{"type":"waha","wahaApiUrl":"https://wa.example.org/?token=hunter2"}`,
			"", false, []string{"carried a user name, a password or a query string"}, []string{"hunter2"}},
		{"credentials in an address that does not parse", `{"type":"waha","wahaApiUrl":"https://admin:hunter2@wa.example.org:bad"}`,
			"", false, []string{"Kuma's WAHA address is not one SubGlance can post to"}, []string{"hunter2"}},
		// Kuma appended its path after the fragment, so its request never
		// reached WAHA's endpoint. A fragment can hold a key, so the
		// address is not quoted.
		{"a fragment in the address", `{"type":"waha","wahaApiUrl":"https://wa.example.org/#apikey=hunter2"}`,
			"", false, []string{"Kuma's WAHA address is not one SubGlance can post to", wahaURLShape}, []string{"hunter2", "wa.example.org/#"}},
		{"an address without a scheme", `{"type":"waha","wahaApiUrl":"waha:3000"}`,
			"", false, []string{`address "waha:3000" is not one SubGlance can post to`, wahaURLShape}, nil},
		{"a port with no host", `{"type":"waha","wahaApiUrl":"http://:3000"}`,
			"", false, []string{`address "http://:3000" is not one SubGlance can post to`}, nil},
		{"no address", `{"type":"waha"}`, "", false, []string{"is not one SubGlance can post to"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			wantURL, wantLen := tc.url, 2
			if wantURL == "" {
				wantURL = configfile.Placeholder
			}
			if tc.headers {
				wantLen = 3
				if c.Config["headers"] != configfile.Placeholder {
					t.Errorf("headers = %q, want them withheld", c.Config["headers"])
				}
			}
			if c.Type != "webhook" || c.Config["url"] != wantURL || c.Config["body"] != configfile.Placeholder || len(c.Config) != wantLen {
				t.Errorf("channel = %s %q, want a webhook to %s with the body withheld", c.Type, c.Config, wantURL)
			}
			for _, want := range append(tc.notes, "fill in the body as "+strings.TrimSpace(wahaBody)) {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			// The key is a credential and the chat a phone number: neither
			// the file nor the report holds one.
			for _, unwanted := range append(tc.absent, key, "612345678") {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["url"], unwanted) {
					t.Errorf("notes %q or url %q contain %q", notes, c.Config["url"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{
				"url":     "http://waha:3000/api/sendText",
				"headers": strings.Replace(wahaHeader, "<api key>", key, 1),
				"body": strings.NewReplacer("<session>", "default", "<chat id>", wahaChat).
					Replace(wahaBody),
			})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
