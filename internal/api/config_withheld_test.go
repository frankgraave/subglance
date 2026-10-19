package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
	"gopkg.in/yaml.v3"
)

// withheldFile is a file whose every credential is withheld, of each channel
// type whose required setting is a credential, as the Uptime Kuma converter
// writes one. Imported anywhere, it creates each channel switched off and
// stored without the withheld values.
const withheldFile = `version: 1
channels:
  - key: discord
    name: Discord
    type: discord
    config:
      url: <fill in after import>
  - key: telegram
    name: Telegram
    type: telegram
    config:
      bot_token: <fill in after import>
      chat_id: "-1001234"
  - key: ntfy
    name: ntfy
    type: ntfy
    config:
      url: https://ntfy.example.com
      username: sg
      password: <fill in after import>
      topic: <fill in after import>
  - key: gotify
    name: Gotify
    type: gotify
    config:
      url: https://gotify.example.com
      token: <fill in after import>
  - key: pushover
    name: Pushover
    type: webhook
    config:
      url: <fill in after import>
      body: '{ "title": "{{summary}}", "message": "{{details}}" }'
  - key: form
    name: Form
    type: webhook
    config:
      url: <fill in after import>
      headers: <fill in after import>
      body: msg={{summary}}&d={{details}}
`

// withheldNeeds is the needs_secrets an import of withheldFile reports.
var withheldNeeds = map[string]string{
	"discord":  "url",
	"telegram": "bot_token",
	"ntfy":     "password,topic",
	"gotify":   "token",
	"pushover": "url",
	"form":     "headers,url",
}

// exportedNeeds is what another instance needs filled in from an export of
// what withheldFile created: the same, plus the ntfy and Gotify server
// addresses, which every export withholds (they are not on the public
// allowlist) and which the first file gave.
var exportedNeeds = map[string]string{
	"discord":  "url",
	"telegram": "bot_token",
	"ntfy":     "password,topic,url",
	"gotify":   "token,url",
	"pushover": "url",
	"form":     "headers,url",
}

func checkNeeds(t *testing.T, what string, rep importReport, needs map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, it := range rep.Channels {
		got[it.Key] = strings.Join(it.NeedsSecrets, ",")
	}
	for key, want := range needs {
		if got[key] != want {
			t.Errorf("%s: %s needs_secrets = %q, want %q", what, key, got[key], want)
		}
	}
}

// A channel an import created without its credential is stored without
// it, and has to export as a file that says so: one that imports again,
// into the same instance as unchanged and into another as the first file
// did. Written without the required setting, it was refused by both
// ("config.url is required for a discord channel").
func TestExportOfAChannelImportedWithoutItsSecretImportsAgain(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	code, rep, body := importYAML(t, src, withheldFile, false)
	if code != http.StatusOK {
		t.Fatalf("first import = %d: %s", code, body)
	}
	checkNeeds(t, "first import", rep, withheldNeeds)
	chans, err := srcDB.ListChannels(context.Background())
	must(t, err)
	for _, c := range chans {
		if c.Type == store.ChannelDiscord && len(c.Config) != 0 {
			t.Fatalf("the fixture is not the case under test: discord stored with %v", c.Config)
		}
	}

	exported := exportYAML(t, src)
	var doc configfile.Document
	must(t, yaml.Unmarshal([]byte(exported), &doc))
	for _, c := range doc.Channels {
		for _, k := range strings.Split(withheldNeeds[c.Key], ",") {
			if c.Config[k] != configfile.Placeholder {
				t.Errorf("export: %s config.%s = %q, want it withheld", c.Key, k, c.Config[k])
			}
		}
	}

	code, rep, body = importYAML(t, src, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import into the instance that wrote it = %d: %s", code, body)
	}
	if rep.Summary.Create != 0 || rep.Summary.Update != 0 {
		t.Errorf("import into the instance that wrote it = %+v, want everything unchanged\n%s", rep.Summary, body)
	}
	checkNeeds(t, "same instance", rep, withheldNeeds)

	dst, _ := testServerWithDB(t)
	code, rep, body = importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import into another instance = %d: %s", code, body)
	}
	checkNeeds(t, "another instance", rep, exportedNeeds)

	// And the second instance's own export imports into it as unchanged.
	again := exportYAML(t, dst)
	code, rep, body = importYAML(t, dst, again, false)
	if code != http.StatusOK || rep.Summary.Create != 0 || rep.Summary.Update != 0 {
		t.Errorf("second instance, its own export = %d %+v: %s", code, rep.Summary, body)
	}
}

// A webhook's headers are withheld whole, and they can name a Content-Type
// such as a form. Another instance has no headers to keep, and read the
// body as JSON, the type a webhook without headers sends: a form body was
// refused as invalid JSON, so a channel that worked could not be moved.
func TestImportOfAFormWebhookWithItsHeadersWithheld(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	_, err := srcDB.CreateChannel(context.Background(), store.Channel{Name: "Form", Type: store.ChannelWebhook,
		Enabled: true, Config: map[string]string{
			"url":     "https://hook.example.com/form",
			"headers": "Content-Type: application/x-www-form-urlencoded\nX-Token: secret",
			"body":    "msg={{summary}}&d={{details}}",
		}})
	must(t, err)
	exported := exportYAML(t, src)

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if len(rep.Channels) != 1 || strings.Join(rep.Channels[0].NeedsSecrets, ",") != "headers,url" {
		t.Errorf("report = %+v, want the form channel with headers and url to fill in", rep.Channels)
	}
	chans, err := dstDB.ListChannels(context.Background())
	must(t, err)
	if len(chans) != 1 || chans[0].Enabled || chans[0].Config["body"] != "msg={{summary}}&d={{details}}" {
		t.Errorf("stored = %+v, want the body kept and the channel switched off", chans)
	}
	for k := range chans[0].Config {
		if k != "body" {
			t.Errorf("stored %s = %q: a stand-in is never stored", k, chans[0].Config[k])
		}
	}
}

// With its headers withheld, a body is still held to the rules every type
// shares; and once the file gives the headers, or the instance keeps them,
// to its own type's.
func TestImportStillRefusesAWebhookBodyItCanJudge(t *testing.T) {
	webhook := func(headers, body string) string {
		return "version: 1\nchannels:\n  - key: w\n    name: W\n    type: webhook\n    config:\n" +
			"      url: <fill in after import>\n      headers: " + headers + "\n      body: '" + body + "'\n"
	}
	cases := map[string]struct{ doc, want string }{
		"unknown placeholder, headers withheld": {webhook(configfile.Placeholder, "{{nope}}"), "unknown placeholder"},
		"unclosed braces, headers withheld":     {webhook(configfile.Placeholder, "x={{summary"), "not closed"},
		"not JSON, JSON headers given":          {webhook("'Content-Type: application/json'", "msg={{summary}}"), "not valid JSON"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			code, _, body := importYAML(t, srv, tc.doc, false)
			if code != http.StatusBadRequest {
				t.Fatalf("import = %d, want 400: %s", code, body)
			}
			var e errorResponse
			must(t, json.Unmarshal([]byte(body), &e))
			if e.Field != "channels[0].config.body" || !strings.Contains(e.Error, tc.want) {
				t.Errorf("refusal = %s %q, want config.body and %q", e.Field, e.Error, tc.want)
			}
		})
	}

	// Kept from the instance: the stored JSON type still decides.
	srv, db := testServerWithDB(t)
	_, err := db.CreateChannel(context.Background(), store.Channel{Name: "W", Type: store.ChannelWebhook,
		Enabled: true, Config: map[string]string{"url": "https://hook.example.com/x",
			"headers": "Content-Type: application/json\nX-Token: secret"}})
	must(t, err)
	doc := exportYAML(t, srv)
	doc = strings.Replace(doc, "    config:\n", "    config:\n      body: msg={{summary}}\n", 1)
	if code, _, body := importYAML(t, srv, doc, true); code != http.StatusBadRequest || !strings.Contains(body, "not valid JSON") {
		t.Errorf("a form body under stored JSON headers = %d %s, want refused as not JSON", code, body)
	}
}

// Only a setting the import would refuse the file for lacking is added: a
// webhook stored with a body no rule accepts, under JSON headers it keeps,
// exports as it is, with nothing claimed withheld that it never had.
func TestExportWithholdsNothingTheChannelNeverHad(t *testing.T) {
	srv, db := testServerWithDB(t)
	_, err := db.CreateChannel(context.Background(), store.Channel{Name: "Old", Type: store.ChannelWebhook,
		Enabled: true, Config: map[string]string{"url": "https://hook.example.com/x", "body": "{{nope}}"}})
	must(t, err)
	_, err = db.CreateChannel(context.Background(), store.Channel{Name: "Mail", Type: store.ChannelEmail,
		Enabled: true, Config: map[string]string{"to": "ops@example.com", "host": "smtp.example.com",
			"from": "sg@example.com", "port": "587"}})
	must(t, err)
	var doc configfile.Document
	must(t, yaml.Unmarshal([]byte(exportYAML(t, srv)), &doc))
	for _, c := range doc.Channels {
		keys := make([]string, 0, len(c.Config))
		for k := range c.Config {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := map[string]string{"old": "body,url", "mail": "from,host,port,to"}[c.Key]
		if got := strings.Join(keys, ","); got != want {
			t.Errorf("%s exported with %s, want %s", c.Key, got, want)
		}
	}
}
