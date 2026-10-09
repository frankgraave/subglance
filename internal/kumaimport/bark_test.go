package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// Kuma pushes to a Bark endpoint that ends in the device key. The converted
// webhook pushes to the same server with a body of its own; the key is the
// credential, so the url is left to fill in, and the report names the
// server without it.
func TestBarkBecomesAWebhookToTheSameServer(t *testing.T) {
	const key = "kumaSecretDeviceKey42"
	cases := []struct {
		name   string
		config string
		body   string
		notes  []string
		absent []string
	}{
		{"the public server, Kuma's defaults", `{"type":"Bark","barkEndpoint":"https://api.day.app/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"fill in the url as https://api.day.app/<device key>"},
			[]string{"allow-private-targets", "left out"}},
		{"group and sound, API v2", `{"type":"Bark","apiVersion":"v2","barkEndpoint":"https://api.day.app/` + key + `/",` +
			`"barkGroup":"Uptime alerts","barkSound":"alarm"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "group": "Uptime alerts", "sound": "alarm" }` + "\n",
			[]string{"https://api.day.app/<device key>"}, nil},
		{"a self-hosted server on a local name", `{"type":"Bark","apiVersion":"v1","barkEndpoint":"http://bark:8080/` + key + `","barkSound":"minuet"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "minuet" }` + "\n",
			[]string{"fill in the url as http://bark:8080/<device key>", "bark is a local name", "--allow-private-targets"}, nil},
		{"a self-hosted server under a path", `{"type":"Bark","barkEndpoint":"https://push.example.org/bark/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"fill in the url as https://push.example.org/bark/<device key>"}, []string{"allow-private-targets"}},
		{"a private address", `{"type":"Bark","barkEndpoint":"http://192.168.1.9:8080/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"http://192.168.1.9:8080/<device key>", "192.168.1.9 is private or reserved"}, nil},
		// The path before the key is written escaped, so a brace in it
		// cannot become a placeholder in the address that is filled in.
		{"braces in the path", `{"type":"Bark","barkEndpoint":"https://push.example.org/{{x}}/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"https://push.example.org/%7B%7Bx%7D%7D/<device key>"}, nil},
		{"a custom sound", `{"type":"Bark","barkEndpoint":"https://api.day.app/` + key + `","barkSound":"siren.caf"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "siren.caf" }` + "\n", nil, nil},
		{"a sound that is not a name", `{"type":"Bark","barkEndpoint":"https://api.day.app/` + key + `","barkSound":"../x"}`,
			barkBody, []string{`the sound "../x" is not a Bark sound name`}, nil},
		{"a group with a placeholder", `{"type":"Bark","barkEndpoint":"https://api.day.app/` + key + `","barkGroup":"{{msg}}","barkSound":"bell"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "bell" }` + "\n",
			[]string{`the group "{{msg}}" has braces`}, nil},
		{"a group with a quote", `{"type":"Bark","barkEndpoint":"https://api.day.app/` + key + `","barkGroup":"ops \"night\"","barkSound":"bell"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "group": "ops \"night\"", "sound": "bell" }` + "\n", nil, nil},
		{"credentials in the endpoint", `{"type":"Bark","barkEndpoint":"https://admin:hunter2@push.example.org/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"not an address SubGlance can post to", barkURLShape}, []string{"hunter2", "push.example.org"}},
		{"a query in the endpoint", `{"type":"Bark","barkEndpoint":"https://push.example.org/` + key + `?token=hunter2"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"not an address SubGlance can post to"}, []string{"hunter2"}},
		{"an endpoint without a scheme", `{"type":"Bark","barkEndpoint":"api.day.app/` + key + `"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"not an address SubGlance can post to", barkURLShape}, nil},
		{"no endpoint", `{"type":"Bark"}`,
			`{ "title": "{{summary}}", "body": "{{details}}", "sound": "telegraph" }` + "\n",
			[]string{"not an address SubGlance can post to"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "webhook" || c.Config["url"] != configfile.Placeholder || c.Config["body"] != tc.body || len(c.Config) != 2 {
				t.Errorf("channel = %s %q, want a webhook with the url withheld and the body\n%s", c.Type, c.Config, tc.body)
			}
			for _, want := range append(tc.notes, "now a webhook channel that pushes to Bark") {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			// The device key is the credential: it is in neither the
			// file nor the report.
			for _, unwanted := range append(tc.absent, key) {
				if strings.Contains(notes, unwanted) || strings.Contains(c.Config["body"], unwanted) {
					t.Errorf("notes %q or body %q contain %q", notes, c.Config["body"], unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{"url": "https://api.day.app/" + key})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
