package kumaimport

import (
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// The bodies the converter writes are the examples in docs/channels.md, so a
// fix to an example reaches people who imported from Kuma, and the other way
// round.
func TestWebhookBodiesAreTheDocumentedExamples(t *testing.T) {
	raw, err := os.ReadFile("../../docs/channels.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for heading, body := range map[string]string{
		"### Microsoft Teams": teamsBody,
		"### Matrix":          matrixBody,
		"### Pushover":        pushoverBody,
		"### Mattermost, Rocket.Chat and Google Chat": chatBody,
		"### Home Assistant":                          homeAssistantBody,
		"### Signal":                                  signalBody,
		"### Bark":                                    barkBody,
		"### WhatsApp (WAHA)":                         wahaBody,
	} {
		start := strings.Index(doc, "\n"+heading+"\n")
		if start == -1 {
			t.Errorf("docs/channels.md has no %q section", heading)
			continue
		}
		section := doc[start+len(heading)+2:]
		if next := strings.Index(section, "\n#"); next != -1 {
			section = section[:next]
		}
		m := regexp.MustCompile("(?s)```json\n(.*?)```").FindStringSubmatch(section)
		if m == nil {
			t.Errorf("%s in docs/channels.md has no JSON example", heading)
			continue
		}
		if m[1] != body {
			t.Errorf("%s: the converter writes\n%s\nthe documentation shows\n%s", heading, body, m[1])
		}
	}
}

// convertKuma converts one notification row as the fixture reader would
// hand it over.
func convertKuma(t *testing.T, config string) (configfile.Channel, string) {
	t.Helper()
	var res Result
	c, ok := convertChannel(row{"id": int64(1), "name": "Chat", "active": int64(1), "config": config}, &res)
	if !ok {
		t.Fatalf("skipped: %s", notesOf(res))
	}
	return c, notesOf(res)
}

// fillIn stands in for the user who fills the withheld values in after the
// import, so the result can be put through the webhook's own validator.
func fillIn(cfg map[string]string, values map[string]string) map[string]string {
	out := maps.Clone(cfg)
	for k, v := range out {
		if v == configfile.Placeholder {
			out[k] = values[k]
		}
	}
	return out
}

func TestTeamsBecomesAWebhookWithACard(t *testing.T) {
	cases := []struct {
		name   string
		config string
		notes  []string
		absent []string
	}{
		{"tags listed on Kuma's card", `{"type":"teams","webhookUrl":"https://example.webhook.office.com/kuma-secret","teamsEnableTags":true}`,
			[]string{"Teams Workflows", "Send test", "tags were listed"}, nil},
		{"tags not listed", `{"type":"teams","webhookUrl":"https://example.webhook.office.com/kuma-secret","teamsEnableTags":false}`,
			[]string{"Teams Workflows", "Send test"}, []string{"tags were listed"}},
		{"tags setting absent", `{"type":"teams","webhookUrl":"https://example.webhook.office.com/kuma-secret"}`,
			[]string{"Teams Workflows", "Send test"}, []string{"tags were listed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "webhook" || c.Config["url"] != configfile.Placeholder || c.Config["body"] != teamsBody ||
				c.Config["method"] != "" || c.Config["headers"] != "" {
				t.Errorf("channel = %+v", c)
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
			cfg := fillIn(c.Config, map[string]string{"url": "https://example.webhook.office.com/workflows/1"})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}

func TestMatrixBecomesAPutToTheRoom(t *testing.T) {
	c, notes := convertKuma(t, `{"type":"matrix","homeserverUrl":"https://matrix.example.org/","internalRoomId":"!abc:example.org",`+
		`"accessToken":"kuma-secret-matrix","matrixUseTemplate":true,"matrixTemplate":"{{ msg }}"}`)
	want := map[string]string{
		"url":     "https://matrix.example.org/_matrix/client/v3/rooms/!abc:example.org/send/m.room.message/{{txn_id}}",
		"method":  "PUT",
		"headers": configfile.Placeholder,
		"body":    matrixBody,
	}
	if c.Type != "webhook" || !maps.Equal(c.Config, want) {
		t.Errorf("channel = %s %v, want webhook %v", c.Type, c.Config, want)
	}
	for _, want := range []string{"Authorization: Bearer <access token>", "template was not carried over"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes = %q, want %q", notes, want)
		}
	}
	cfg := fillIn(c.Config, map[string]string{"headers": "Authorization: Bearer token"})
	if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
		t.Errorf("the filled-in channel is refused: %v", err)
	}

	// A room id is one path segment: a character that would end the path
	// or start a query is escaped.
	c, _ = convertKuma(t, `{"type":"matrix","homeserverUrl":"https://m.example","internalRoomId":"!a/b?c#d:m.example"}`)
	if got, want := c.Config["url"], "https://m.example/_matrix/client/v3/rooms/!a%2Fb%3Fc%23d:m.example/send/m.room.message/{{txn_id}}"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}

	// Without a usable homeserver there is no address to write, and the
	// user is told the shape to fill in.
	for _, config := range []string{
		`{"type":"matrix","homeserverUrl":"matrix.example.org","internalRoomId":"!abc:example.org"}`,
		`{"type":"matrix","homeserverUrl":"https://matrix.example.org"}`,
	} {
		c, notes = convertKuma(t, config)
		if c.Config["url"] != configfile.Placeholder || !strings.Contains(notes, "/send/m.room.message/{{txn_id}}") {
			t.Errorf("%s: url = %q, notes = %q", config, c.Config["url"], notes)
		}
	}
}

func TestPushoverBecomesAWebhookWithItsSettingsInTheBody(t *testing.T) {
	cases := []struct {
		name   string
		config string
		body   map[string]any
		notes  []string
	}{
		{"only the credentials", `{"type":"pushover","pushoveruserkey":"kuma-secret-user","pushoverapptoken":"kuma-secret-app"}`,
			map[string]any{"title": "{{summary}}", "message": "{{details}}"}, nil},
		{"every setting", `{"type":"pushover","pushoveruserkey":"u","pushoverapptoken":"a","pushoverpriority":"1",` +
			`"pushoversounds":"siren","pushoversounds_up":"magic","pushoverdevice":"phone,tablet","pushoverttl":3600,"pushovertitle":"Kuma"}`,
			map[string]any{"title": "{{summary}}", "message": "{{details}}", "priority": 1.0, "sound": "siren",
				"device": "phone,tablet", "ttl": 3600.0},
			[]string{"another sound for a recovery", "title was replaced"}},
		{"emergency priority", `{"type":"pushover","pushoverpriority":2}`,
			map[string]any{"title": "{{summary}}", "message": "{{details}}", "priority": 2.0, "retry": 30.0, "expire": 3600.0}, nil},
		{"values Pushover would refuse", `{"type":"pushover","pushoverpriority":"5","pushoversounds":"a\"b","pushoverdevice":"x y","pushoverttl":"-1"}`,
			map[string]any{"title": "{{summary}}", "message": "{{details}}"},
			[]string{`priority "5"`, `sound "a\"b"`, `device "x y"`, `lifetime "-1"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "webhook" || c.Config["url"] != configfile.Placeholder || len(c.Config) != 2 {
				t.Errorf("channel = %+v", c)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(c.Config["body"]), &body); err != nil {
				t.Fatalf("body is not JSON: %v\n%s", err, c.Config["body"])
			}
			if !maps.Equal(body, tc.body) {
				t.Errorf("body = %v, want %v", body, tc.body)
			}
			for _, want := range append(tc.notes, "api.pushover.net/1/messages.json?token=") {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			cfg := fillIn(c.Config, map[string]string{"url": "https://api.pushover.net/1/messages.json?token=a&user=u"})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}

func TestChatServicesBecomeAWebhookWithATextMessage(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		url     string
		channel any // the body's "channel", nil when it has none
		notes   []string
		absent  []string
	}{
		{"Mattermost with a channel", `{"type":"mattermost","mattermostWebhookUrl":"https://mm.example/hooks/kuma-secret",` +
			`"mattermostchannel":"Ops-Alerts","mattermostusername":"Uptime Kuma","mattermosticonemo":":ok: :x:"}`,
			"https://mm.example/hooks/abc", "ops-alerts",
			[]string{"Mattermost incoming webhook", "own name and icon"}, nil},
		{"Mattermost direct message", `{"type":"mattermost","mattermostWebhookUrl":"https://mm.example/hooks/kuma-secret","mattermostchannel":"@frank"}`,
			"https://mm.example/hooks/abc", "@frank", []string{"Mattermost incoming webhook"}, []string{"own name and icon", "left out"}},
		{"Mattermost without a channel", `{"type":"mattermost","mattermostWebhookUrl":"https://mm.example/hooks/kuma-secret"}`,
			"https://mm.example/hooks/abc", nil, nil, []string{"own name and icon", "left out"}},
		{"Mattermost channel by display name", `{"type":"mattermost","mattermostWebhookUrl":"https://mm.example/hooks/kuma-secret","mattermostchannel":"Ops Alerts"}`,
			"https://mm.example/hooks/abc", nil, []string{`channel "Ops Alerts" is not a Mattermost channel name`}, nil},
		{"Rocket.Chat with a channel", `{"type":"rocket.chat","rocketwebhookURL":"https://rc.example/hooks/kuma-secret",` +
			`"rocketchannel":"#Alerts","rocketusername":"kuma","rocketiconemo":":ghost:"}`,
			"https://rc.example/hooks/abc/def", "#Alerts", []string{"Rocket.Chat incoming integration", "own name and icon"}, nil},
		{"Rocket.Chat room id", `{"type":"rocket.chat","rocketwebhookURL":"https://rc.example/hooks/kuma-secret","rocketchannel":"GENERAL"}`,
			"https://rc.example/hooks/abc/def", "GENERAL", nil, []string{"own name and icon", "left out"}},
		{"Mattermost channel with a leading #", `{"type":"mattermost","mattermostWebhookUrl":"https://mm.example/hooks/kuma-secret","mattermostchannel":"#Alerts"}`,
			"https://mm.example/hooks/abc", "#alerts", []string{"Mattermost incoming webhook"}, []string{"left out"}},
		{"Rocket.Chat channel beyond ASCII", `{"type":"rocket.chat","rocketwebhookURL":"https://rc.example/hooks/kuma-secret","rocketchannel":"#警報"}`,
			"https://rc.example/hooks/abc/def", "#警報", nil, []string{"left out"}},
		{"Rocket.Chat channel with a space", `{"type":"rocket.chat","rocketwebhookURL":"https://rc.example/hooks/kuma-secret","rocketchannel":"#ops alerts"}`,
			"https://rc.example/hooks/abc/def", nil, []string{`channel "#ops alerts" is not a Rocket.Chat channel`}, nil},
		{"Rocket.Chat channel that is not one", `{"type":"rocket.chat","rocketwebhookURL":"https://rc.example/hooks/kuma-secret","rocketchannel":"#a\"b"}`,
			"https://rc.example/hooks/abc/def", nil, []string{`channel "#a\"b" is not a Rocket.Chat channel`}, nil},
		{"Google Chat with a template", `{"type":"GoogleChat","googleChatWebhookURL":"https://chat.googleapis.com/v1/spaces/X/messages?key=k&token=kuma-secret",` +
			`"googleChatUseTemplate":true,"googleChatTemplate":"{{ msg }}"}`,
			"https://chat.googleapis.com/v1/spaces/X/messages?key=k&token=t", nil,
			[]string{"Google Chat space webhook", "template was not carried over"}, []string{"own name and icon"}},
		{"Google Chat template switched off", `{"type":"GoogleChat","googleChatWebhookURL":"https://chat.googleapis.com/v1/spaces/X/messages?key=k&token=kuma-secret",` +
			`"googleChatUseTemplate":false,"googleChatTemplate":"{{ msg }}"}`,
			"https://chat.googleapis.com/v1/spaces/X/messages?key=k&token=t", nil, nil, []string{"template"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Type != "webhook" || c.Config["url"] != configfile.Placeholder || len(c.Config) != 2 {
				t.Errorf("channel = %+v", c)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(c.Config["body"]), &body); err != nil {
				t.Fatalf("body is not JSON: %v\n%s", err, c.Config["body"])
			}
			want := map[string]any{"text": "{{summary}}\n{{details}}"}
			if tc.channel != nil {
				want["channel"] = tc.channel
			} else if c.Config["body"] != chatBody {
				t.Errorf("body = %q, want the documented %q", c.Config["body"], chatBody)
			}
			if !maps.Equal(body, want) {
				t.Errorf("body = %v, want %v", body, want)
			}
			for _, w := range append(tc.notes, "now a webhook channel that posts a text message") {
				if !strings.Contains(notes, w) {
					t.Errorf("notes = %q, want %q", notes, w)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(notes, unwanted) {
					t.Errorf("notes = %q, should not say %q", notes, unwanted)
				}
			}
			cfg := fillIn(c.Config, map[string]string{"url": tc.url})
			if err := notifier.NewWebhookSender(nil).Validate(cfg); err != nil {
				t.Errorf("the filled-in channel is refused: %v", err)
			}
		})
	}
}
