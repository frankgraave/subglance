package notifier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// hostileError is a push monitor's failure message as anyone holding the push
// URL can write it: a Slack channel ping, a Slack link and a Discord masked
// link, each with a label that hides where it goes.
const hostileError = "<!channel> restore failed, see <https://evil.example|the runbook> " +
	"or [the runbook](https://evil.example)"

// markupAlert is an outage whose own text carries markup, and whose links
// are SubGlance's, which must keep working.
func markupAlert() Alert {
	at := time.Date(2026, 10, 12, 19, 30, 0, 0, time.UTC)
	return Alert{
		MonitorID: 7, MonitorName: "R&D <api> *beta*", MonitorType: "push",
		Target: "push", Event: string(state.EventIncidentConfirmed),
		IncidentID: 3, StartedAt: at.Add(-time.Minute), At: at,
		Cause: "push_reported", LastError: hostileError,
		IncidentURL: "https://status.example.com/incidents/3",
		MonitorURL:  "https://status.example.com/monitors/7",
	}
}

// sentJSON sends a through sender to an in-process server and decodes what
// arrived.
func sentJSON(t *testing.T, sender Sender, cfg func(url string) map[string]string, a Alert) map[string]any {
	t.Helper()
	srv, body := recordedBody(t)
	if err := sender.Send(context.Background(), cfg(srv.URL), a); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(*body, &got); err != nil {
		t.Fatalf("decode %s: %v", *body, err)
	}
	return got
}

func TestSlackAlertTextCannotPingOrLink(t *testing.T) {
	got := sentJSON(t, NewSlackSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url}
	}, markupAlert())

	blocks := got["blocks"].([]any)
	section := func(i int) string {
		return blocks[i].(map[string]any)["text"].(map[string]any)["text"].(string)
	}
	texts := map[string]string{
		"text": got["text"].(string), "title section": section(0), "body section": section(1),
	}
	for where, text := range texts {
		for _, raw := range []string{"<!channel>", "<https://evil.example|", "<api>", "R&D"} {
			if strings.Contains(text, raw) {
				t.Errorf("%s carries %q as markup: %q", where, raw, text)
			}
		}
	}
	if want := "R&amp;D &lt;api&gt; *beta* is down"; texts["text"] != want {
		t.Errorf("fallback text = %q, want %q", texts["text"], want)
	}
	if want := "&lt;!channel&gt; restore failed, see &lt;https://evil.example|the runbook&gt;"; !strings.Contains(texts["body section"], want) {
		t.Errorf("body section = %q, want it to contain %q", texts["body section"], want)
	}
	// SubGlance's own links are markup on purpose and stay links.
	links := section(2)
	if want := "<https://status.example.com/incidents/3|Open the incident>"; !strings.Contains(links, want) {
		t.Errorf("links section = %q, want it to contain %q", links, want)
	}
}

func TestDiscordAlertTextCannotLink(t *testing.T) {
	a := markupAlert()
	a.Target = "https://shop.example.com/a_b*c"
	got := sentJSON(t, NewDiscordSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url + "/discord"}
	}, a)

	embed := got["embeds"].([]any)[0].(map[string]any)
	desc := embed["description"].(string)
	if strings.Contains(desc, "or [the runbook]") {
		t.Errorf("description carries a masked link: %q", desc)
	}
	if want := `or \[the runbook\](https://evil.example)`; !strings.Contains(desc, want) {
		t.Errorf("description = %q, want it to contain %q", desc, want)
	}
	// An address in the text is left whole: a backslash inside it would
	// be part of the link Discord makes of it.
	if !strings.HasPrefix(desc, "https://shop.example.com/a_b*c\n") {
		t.Errorf("description = %q, want the target address untouched", desc)
	}
	// The title is plain text to Discord: escaping it would show.
	if want := "R&D <api> *beta* is down"; embed["title"] != want {
		t.Errorf("title = %q, want %q", embed["title"], want)
	}
	if want := "[Open the incident](https://status.example.com/incidents/3)"; !strings.Contains(desc, want) {
		t.Errorf("description = %q, want SubGlance's link %q", desc, want)
	}
}

func TestSlackText(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"plain text":        "plain text",
		"a & b":             "a &amp; b",
		"<!here>":           "&lt;!here&gt;",
		"<@U024BE7LH>":      "&lt;@U024BE7LH&gt;",
		"*bold* _it_ ~s~":   "*bold* _it_ ~s~",
		"&amp; stays typed": "&amp;amp; stays typed",
	} {
		if got := slackText(in); got != want {
			t.Errorf("slackText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscordText(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"no response within 10s":     "no response within 10s",
		"[x](https://evil.example)":  `\[x\](https://evil.example)`,
		"*a* _b_ ~~c~~ `d` ||e||":    "\\*a\\* \\_b\\_ \\~\\~c\\~\\~ \\`d\\` \\|\\|e\\|\\|",
		`C:\backup`:                  `C:\\backup`,
		"<@123> <t:1:R>":             `\<@123> \<t:1:R>`,
		"# heading\n> quote\n- item": "\\# heading\n\\> quote\n\\- item",
		"  # indented":               "  \\# indented",
		"2026-10-12 down - again":    "2026-10-12 down - again",
		"see https://x.example/a_b.": "see https://x.example/a_b.",
		"https://a.example/*\n# h":   "https://a.example/*\n\\# h",
		"$.status was \"*down*\"":    `$.status was "\*down\*"`,
		"cert_expiry a__b__c _x_":    `cert_expiry a\_\_b\_\_c \_x\_`,
	} {
		if got := discordText(in); got != want {
			t.Errorf("discordText(%q) = %q, want %q", in, got, want)
		}
	}
}
