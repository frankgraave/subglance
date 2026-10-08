package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// Kuma posts to <base>/api/services/notify/<action> with the long-lived
// token as a bearer token. The converted webhook makes the same request, so
// the address comes over and only the header is left to fill in.
func TestHomeAssistantBecomesAWebhookToTheNotifyAction(t *testing.T) {
	const token = "kuma-secret-ha-token"
	cases := []struct {
		name   string
		config string
		url    string // "" when the url is withheld
		notes  []string
		absent []string
	}{
		{"a phone on a local name", `{"type":"HomeAssistant","homeAssistantUrl":" http://homeassistant.local:8123// ",` +
			`"longLivedAccessToken":"` + token + `","notificationService":"mobile_app_pixel"}`,
			"http://homeassistant.local:8123/api/services/notify/mobile_app_pixel",
			[]string{"homeassistant.local is a local name", "--allow-private-targets"}, nil},
		{"no action means notify", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org","longLivedAccessToken":"` + token + `"}`,
			"https://ha.example.org/api/services/notify/notify", nil, []string{"allow-private-targets", "fill in the url"}},
		{"a private address under a path", `{"type":"HomeAssistant","homeAssistantUrl":"http://192.168.1.20:8123/ha/",` +
			`"notificationService":"persistent_notification"}`,
			"http://192.168.1.20:8123/ha/api/services/notify/persistent_notification",
			[]string{"192.168.1.20 is private or reserved", "--allow-private-targets"}, nil},
		{"a host name without a dot", `{"type":"HomeAssistant","homeAssistantUrl":"http://homeassistant:8123","notificationService":"notify"}`,
			"http://homeassistant:8123/api/services/notify/notify", []string{"homeassistant is a local name"}, nil},
		{"a public address", `{"type":"HomeAssistant","homeAssistantUrl":"https://9.9.9.9:8123"}`,
			"https://9.9.9.9:8123/api/services/notify/notify", nil, []string{"allow-private-targets"}},
		{"a private IPv6 address", `{"type":"HomeAssistant","homeAssistantUrl":"http://[fd00::5]:8123"}`,
			"http://[fd00::5]:8123/api/services/notify/notify", []string{"fd00::5 is private or reserved"}, nil},
		{"the action with its domain", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org","notificationService":"notify.Mobile_App_X"}`,
			"https://ha.example.org/api/services/notify/mobile_app_x", []string{`given as "notify.mobile_app_x"`}, nil},
		{"an action that is not a name", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org","notificationService":"../../states"}`,
			"", []string{`action "../../states" is not a Home Assistant action name`, "https://ha.example.org/api/services/notify/<action>"}, nil},
		{"an action with a placeholder", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org","notificationService":"{{msg}}"}`,
			"", []string{"is not a Home Assistant action name"}, nil},
		{"an address without a scheme", `{"type":"HomeAssistant","homeAssistantUrl":"homeassistant.local:8123"}`,
			"", []string{"is not one SubGlance can post to", haURLShape}, nil},
		{"an address that is not http", `{"type":"HomeAssistant","homeAssistantUrl":"ftp://ha.example.org"}`,
			"", []string{`address "ftp://ha.example.org" is not one SubGlance can post to`}, nil},
		{"no address", `{"type":"HomeAssistant","longLivedAccessToken":"` + token + `"}`,
			"", []string{"is not one SubGlance can post to"}, nil},
		{"an address with a query", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org/?x=1"}`,
			"", []string{"is not one SubGlance can post to"}, nil},
		{"credentials in the address", `{"type":"HomeAssistant","homeAssistantUrl":"https://admin:hunter2@ha.example.org"}`,
			"", []string{"carried a user name and password"}, []string{"hunter2"}},
		{"credentials and a query in the address", `{"type":"HomeAssistant","homeAssistantUrl":"https://admin:hunter2@ha.example.org/?x=1"}`,
			"", []string{"carried a user name and password"}, []string{"hunter2"}},
		{"credentials in an address that does not parse", `{"type":"HomeAssistant","homeAssistantUrl":"https://admin:hunter2@ha.example.org:bad"}`,
			"", []string{"Kuma's Home Assistant address is not one SubGlance can post to"}, []string{"hunter2"}},
		{"a port with no host", `{"type":"HomeAssistant","homeAssistantUrl":"http://:8123"}`,
			"", []string{`address "http://:8123" is not one SubGlance can post to`}, nil},
		// Kuma's request escaped the braces in a path, and so does the
		// url: they are a literal part of the path, not a placeholder.
		{"braces in the path", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org/{{x}}"}`,
			"https://ha.example.org/%7B%7Bx%7D%7D/api/services/notify/notify", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			wantURL := tc.url
			if wantURL == "" {
				wantURL = configfile.Placeholder
			}
			if c.Type != "webhook" || c.Config["url"] != wantURL || c.Config["headers"] != configfile.Placeholder ||
				c.Config["body"] != homeAssistantBody || c.Config["method"] != "" || len(c.Config) != 3 {
				t.Errorf("channel = %s %v, want a webhook to %s", c.Type, c.Config, wantURL)
			}
			for _, want := range append(tc.notes, `fill in the headers as "Authorization: Bearer <long-lived access token>"`, "automation") {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			for _, unwanted := range append(tc.absent, token) {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["url"], unwanted) {
					t.Errorf("notes %q or url %q contain %q", notes, c.Config["url"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{
				"url":     "https://ha.example.org/api/services/notify/notify",
				"headers": "Authorization: Bearer " + token,
			})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
