package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// openWAChat is a chat as OpenWA writes it: the country code and the
// number, without the plus, and @c.us.
const openWAChat = "31612345678@c.us"

// Kuma posts {chatId, text} to OpenWA's
// /api/sessions/<session>/messages/send-text, once for every chat in its
// list. The converted webhook posts to the same address with a body of its
// own; the body names a chat, so it is left to fill in and the report masks
// the chats Kuma used. The API key goes in the withheld headers.
func TestOpenWABecomesAWebhookToTheSameServer(t *testing.T) {
	const key = "kumaSecretOpenWAKey42"
	cases := []struct {
		name    string
		config  string
		url     string // "" when the url is withheld
		headers bool
		notes   []string
		absent  []string
	}{
		{"a local address, with a key", `{"type":"openwa","openwaApiUrl":"http://localhost:2785/","openwaApiKey":"` + key + `",` +
			`"openwaSession":"default","openwaChatId":"` + openWAChat + `"}`,
			"http://localhost:2785/api/sessions/default/messages/send-text", true,
			[]string{"localhost is a local name", "--allow-private-targets", `fill in the headers as "` + openWAHeader + `"`,
				"the chat Kuma sent to (+31 6 •••• 5678)"},
			[]string{"custom message", "chats Kuma sent to", "add a webhook channel"}},
		// Kuma escaped the session as one path segment, so a space and a
		// slash in it stay part of it.
		{"a public address under a path, a session to escape", `{"type":"openwa","openwaApiUrl":"https://wa.example.org/openwa//",` +
			`"openwaSession":"Main line/1","openwaChatId":"0031612345678@c.us"}`,
			"https://wa.example.org/openwa/api/sessions/Main%20line%2F1/messages/send-text", false,
			[]string{"the chat Kuma sent to (+31 6 •••• 5678)"},
			[]string{"allow-private-targets", "fill in the url", "fill in the headers"}},
		// Kuma dropped only the trailing slashes; a doubled slash inside
		// the path is part of the address a proxy routes on.
		{"a doubled slash inside the path", `{"type":"openwa","openwaApiUrl":"https://wa.example.org/proxy//openwa/","openwaSession":"ops"}`,
			"https://wa.example.org/proxy//openwa/api/sessions/ops/messages/send-text", false, nil, nil},
		{"a private address", `{"type":"openwa","openwaApiUrl":"http://192.168.1.7:2785","openwaSession":"ops","openwaChatId":"` + openWAChat + `"}`,
			"http://192.168.1.7:2785/api/sessions/ops/messages/send-text", false,
			[]string{"192.168.1.7 is private or reserved"}, nil},
		// Kuma posted once to every chat in the list and a webhook posts
		// once: the report names how many there were and masks each. A
		// group's id and a linked id are not shown at all.
		{"several chats", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops",` +
			`"openwaChatId":"` + openWAChat + `, 120363042318711234@g.us ,1234567890@lid"}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false,
			[]string{"with the first of the 3 chats Kuma sent to (+31 6 •••• 5678)",
				"Kuma sent each alert to 3 chats and a webhook posts to one",
				"add a webhook channel with the same url and headers for each of the others (a group, ••••)"},
			[]string{"120363042318711234", "1234567890"}},
		// Kuma dropped empty entries from the list.
		{"one chat among empty entries", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops",` +
			`"openwaChatId":" ,` + openWAChat + `,,"}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false,
			[]string{"the chat Kuma sent to (+31 6 •••• 5678)"}, []string{"chats", "add a webhook channel"}},
		{"no chat", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops"}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false, nil, []string{"Kuma sent to"}},
		// Kuma's custom message is a Liquid template; it is named, not
		// carried over. One Kuma kept but did not use, or one of white
		// space only, which Kuma did not use either, is not named.
		{"a custom message", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops",` +
			`"openwaUseCustomMessage":true,"openwaCustomMessage":"{{ name }} is {{ status }}"}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false,
			[]string{"Kuma sent a custom message, a template SubGlance does not read"}, []string{"{{ name }}"}},
		{"a custom message switched off", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops",` +
			`"openwaUseCustomMessage":false,"openwaCustomMessage":"{{ name }} is {{ status }}"}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false, nil, []string{"custom message"}},
		{"a blank custom message", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"ops",` +
			`"openwaUseCustomMessage":true,"openwaCustomMessage":"  \n "}`,
			"https://wa.example.org/api/sessions/ops/messages/send-text", false, nil, []string{"custom message"}},
		// Braces in the session are a literal part of the path, not a
		// placeholder.
		{"braces in the session", `{"type":"openwa","openwaApiUrl":"https://wa.example.org","openwaSession":"{{x}}"}`,
			"https://wa.example.org/api/sessions/%7B%7Bx%7D%7D/messages/send-text", false, nil, nil},
		{"credentials in the address", `{"type":"openwa","openwaApiUrl":"https://admin:hunter2@wa.example.org","openwaSession":"ops"}`,
			"", false, []string{"Kuma's OpenWA address carried a user name, a password or a query string", openWAURLShape}, []string{"hunter2"}},
		{"a query in the address", `{"type":"openwa","openwaApiUrl":"https://wa.example.org/?apiKey=hunter2","openwaSession":"ops"}`,
			"", false, []string{"carried a user name, a password or a query string"}, []string{"hunter2"}},
		{"a fragment in the address", `{"type":"openwa","openwaApiUrl":"https://wa.example.org/#key=hunter2","openwaSession":"ops"}`,
			"", false, []string{"Kuma's OpenWA address is not one SubGlance can post to", openWAURLShape}, []string{"hunter2", "wa.example.org/#"}},
		{"an address without a scheme", `{"type":"openwa","openwaApiUrl":"openwa:2785","openwaSession":"ops"}`,
			"", false, []string{`address "openwa:2785" is not one SubGlance can post to`, openWAURLShape}, nil},
		{"no address", `{"type":"openwa","openwaSession":"ops","openwaApiKey":"` + key + `"}`,
			"", true, []string{"Kuma had no OpenWA address", openWAURLShape}, nil},
		{"no session", `{"type":"openwa","openwaApiUrl":"https://wa.example.org"}`,
			"", false, []string{"Kuma had no OpenWA session", openWAURLShape}, nil},
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
			for _, want := range append(tc.notes, "fill in the body as "+strings.TrimSpace(openWABody)) {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			// The key is a credential and a chat a phone number: neither
			// the file nor the report holds one.
			for _, unwanted := range append(tc.absent, key, "612345678") {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["url"], unwanted) {
					t.Errorf("notes %q or url %q contain %q", notes, c.Config["url"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{
				"url":     "http://openwa:2785/api/sessions/ops/messages/send-text",
				"headers": strings.Replace(openWAHeader, "<api key>", key, 1),
				"body":    strings.Replace(openWABody, "<chat id>", openWAChat, 1),
			})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
