package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// evolutionNumber is a recipient as Evolution API writes it: the country
// code and the number, without the plus.
const evolutionNumber = "31612345678"

// Kuma posts {number, text} to Evolution API's /message/sendText/<instance>.
// The converted webhook posts to the same address with a body of its own;
// the body names a phone number, so it is left to fill in and the report
// masks the recipient Kuma used. The token goes in the withheld headers.
func TestEvolutionBecomesAWebhookToTheSameServer(t *testing.T) {
	const key = "kumaSecretEvolutionToken42"
	cases := []struct {
		name    string
		config  string
		url     string // "" when the url is withheld
		headers bool
		notes   []string
		absent  []string
	}{
		{"a local address, with a token", `{"type":"evolution","evolutionApiUrl":"http://localhost:8080/","evolutionAuthToken":"` + key + `",` +
			`"evolutionInstanceName":"ops","evolutionRecipient":"` + evolutionNumber + `"}`,
			"http://localhost:8080/message/sendText/ops", true,
			[]string{"localhost is a local name", "--allow-private-targets", `fill in the headers as "` + evolutionHeader + `"`,
				"the recipient Kuma sent to (+31 6 •••• 5678)"},
			[]string{"custom message"}},
		// Kuma escaped the instance name as one path segment, so a space
		// and a slash in it stay part of the name.
		{"a public address under a path, an instance name to escape", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org/evo//",` +
			`"evolutionInstanceName":"Main line/1","evolutionRecipient":"` + evolutionNumber + `@s.whatsapp.net"}`,
			"https://wa.example.org/evo/message/sendText/Main%20line%2F1", false,
			[]string{"the recipient Kuma sent to (+31 6 •••• 5678)"},
			[]string{"allow-private-targets", "fill in the url", "fill in the headers"}},
		{"a private address, a number with 00 in front", `{"type":"evolution","evolutionApiUrl":"http://192.168.1.7:8080",` +
			`"evolutionInstanceName":"ops","evolutionRecipient":"00` + evolutionNumber + `"}`,
			"http://192.168.1.7:8080/message/sendText/ops", false,
			[]string{"192.168.1.7 is private or reserved", "the recipient Kuma sent to (+31 6 •••• 5678)"}, nil},
		// A group id can begin with the number of whoever made the group,
		// so it is not shown at all.
		{"a group", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org","evolutionInstanceName":"ops",` +
			`"evolutionRecipient":"120363042318711234@g.us"}`,
			"https://wa.example.org/message/sendText/ops", false, []string{"the recipient Kuma sent to (a group)"}, []string{"120363042318711234"}},
		{"no recipient", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org","evolutionInstanceName":"ops"}`,
			"https://wa.example.org/message/sendText/ops", false, nil, []string{"the recipient Kuma sent to"}},
		// Kuma's custom message is a Liquid template; it is named, not
		// carried over. One Kuma kept but did not use is not named.
		{"a custom message", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org","evolutionInstanceName":"ops",` +
			`"evolutionUseCustomMessage":true,"evolutionCustomMessage":"{{ name }} is {{ status }}"}`,
			"https://wa.example.org/message/sendText/ops", false,
			[]string{"Kuma sent a custom message, a template SubGlance does not read"}, []string{"{{ name }}"}},
		{"a custom message switched off", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org","evolutionInstanceName":"ops",` +
			`"evolutionUseCustomMessage":false,"evolutionCustomMessage":"{{ name }} is {{ status }}"}`,
			"https://wa.example.org/message/sendText/ops", false, nil, []string{"custom message"}},
		// Braces in the instance name are a literal part of the path, not
		// a placeholder.
		{"braces in the instance name", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org","evolutionInstanceName":"{{x}}"}`,
			"https://wa.example.org/message/sendText/%7B%7Bx%7D%7D", false, nil, nil},
		{"credentials in the address", `{"type":"evolution","evolutionApiUrl":"https://admin:hunter2@wa.example.org","evolutionInstanceName":"ops"}`,
			"", false, []string{"Kuma's Evolution API address carried a user name, a password or a query string", evolutionURLShape}, []string{"hunter2"}},
		{"a query in the address", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org/?token=hunter2","evolutionInstanceName":"ops"}`,
			"", false, []string{"carried a user name, a password or a query string"}, []string{"hunter2"}},
		// Kuma appended its path after the fragment, so its request never
		// reached the API's endpoint. A fragment can hold a key, so the
		// address is not quoted.
		{"a fragment in the address", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org/#apikey=hunter2","evolutionInstanceName":"ops"}`,
			"", false, []string{"Kuma's Evolution API address is not one SubGlance can post to", evolutionURLShape}, []string{"hunter2", "wa.example.org/#"}},
		{"an address without a scheme", `{"type":"evolution","evolutionApiUrl":"evolution:8080","evolutionInstanceName":"ops"}`,
			"", false, []string{`address "evolution:8080" is not one SubGlance can post to`, evolutionURLShape}, nil},
		// Without an address Kuma posted to a default host of its own; the
		// import does not guess which server was meant.
		{"no address", `{"type":"evolution","evolutionInstanceName":"ops","evolutionAuthToken":"` + key + `"}`,
			"", true, []string{"Kuma had no Evolution API address", evolutionURLShape}, []string{"evolapicloud"}},
		{"no instance name", `{"type":"evolution","evolutionApiUrl":"https://wa.example.org"}`,
			"", false, []string{"Kuma had no Evolution API instance name", evolutionURLShape}, nil},
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
			for _, want := range append(tc.notes, "fill in the body as "+strings.TrimSpace(evolutionBody)) {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			// The token is a credential and the recipient a phone number:
			// neither the file nor the report holds one.
			for _, unwanted := range append(tc.absent, key, "612345678") {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["url"], unwanted) {
					t.Errorf("notes %q or url %q contain %q", notes, c.Config["url"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{
				"url":     "http://evolution-api:8080/message/sendText/ops",
				"headers": strings.Replace(evolutionHeader, "<token>", key, 1),
				"body":    strings.Replace(evolutionBody, "<number>", evolutionNumber, 1),
			})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
