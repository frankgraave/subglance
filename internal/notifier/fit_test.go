package notifier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

func same(s string) string { return s }

// longAlert is an outage whose body is far over every service's limit: a
// signed URL with a long query as the target, and an error that quotes a
// long keyword. The cause, the error's start and the time under them are
// what a reader needs, so the tests below look for them.
func longAlert(target string) Alert {
	at := time.Date(2026, 10, 17, 3, 14, 0, 0, time.UTC)
	return Alert{
		MonitorID: 7, MonitorName: "Checkout API", MonitorType: "http",
		Target: target, Event: string(state.EventIncidentConfirmed),
		IncidentID: 312, StartedAt: at.Add(-2 * time.Minute), At: at,
		Cause:       "keyword",
		LastError:   `keyword "` + strings.Repeat("<li>R&D</li>", 400) + `" not found in response`,
		IncidentURL: "https://status.example.com/incidents/312",
		MonitorURL:  "https://status.example.com/monitors/7",
		MuteURL:     "https://status.example.com/mute/abc",
	}
}

// signedTarget is a target 6,000 characters long.
var signedTarget = "https://shop.example.com/health?sig=" + strings.Repeat("a1_b2", 1200)

// wantKept fails unless text still carries what the reader of a shortened
// alert needs: the start of every line of the body, the time, and the
// incident link SubGlance adds.
func wantKept(t *testing.T, where, text string) {
	t.Helper()
	for _, part := range []string{
		"https://shop.example.com/health?sig=a1", "\nkeyword\n", `keyword "<li>R&D`, `</li>" not found in response`,
		"Since 2026-10-17 03:12 UTC", "https://status.example.com/incidents/312",
	} {
		if !strings.Contains(text, part) && !strings.Contains(text, slackText(part)) && !strings.Contains(text, discordText(part)) {
			t.Errorf("%s lost %q: %.300q", where, part, text)
		}
	}
}

func TestFitBodyLeavesTextThatFitsAlone(t *testing.T) {
	body := "https://shop.example.com/health\ntimeout\nno response within 10s"
	if got := fitBody(body, len(body), utf16Len, same); got != body {
		t.Errorf("fitBody = %q, want it unchanged", got)
	}
}

func TestFitBodyShortensTheLongestLinesFirst(t *testing.T) {
	body := strings.Join([]string{
		"https://example.com/?q=" + strings.Repeat("x", 900),
		"keyword",
		"error " + strings.Repeat("y", 500),
		"Since 2026-10-17 03:12 UTC",
	}, "\n")
	got := fitBody(body, 300, utf16Len, same)
	if n := utf16Len(got); n > 300 || n < 290 {
		t.Fatalf("fitted to %d units, want close under 300: %q", n, got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want the 4 the body has: %q", len(lines), got)
	}
	if lines[1] != "keyword" || lines[3] != "Since 2026-10-17 03:12 UTC" {
		t.Errorf("short lines changed: %q", got)
	}
	for _, i := range []int{0, 2} {
		if !strings.Contains(lines[i], "…") || len(lines[i]) >= len(strings.Split(body, "\n")[i]) {
			t.Errorf("line %d = %q, want it cut and marked", i, lines[i])
		}
	}
	if !strings.HasPrefix(lines[0], "https://example.com/?q=x") || !strings.HasPrefix(lines[2], "error y") {
		t.Errorf("a cut line lost its start: %q", got)
	}
	// Both long lines were cut to the same length: neither gave up more
	// than the other.
	if a, b := utf16Len(lines[0]), utf16Len(lines[2]); a != b {
		t.Errorf("cut lines are %d and %d units long, want one length", a, b)
	}
}

// The body is cut before the sender escapes it, so the limit holds for the
// escaped text and no entity is left half written.
func TestFitBodyMeasuresTheRenderedText(t *testing.T) {
	body := "target\n" + strings.Repeat("<&>", 2000)
	got := slackText(fitBody(body, slackSectionMax, utf16Len, slackText))
	if n := utf16Len(got); n > slackSectionMax {
		t.Fatalf("escaped body is %d units, over %d", n, slackSectionMax)
	}
	if strings.Count(got, "&") != strings.Count(got, "&amp;")+strings.Count(got, "&lt;")+strings.Count(got, "&gt;") {
		t.Errorf("an entity was split: %.80q", got[len(got)-80:])
	}
}

// Lines that each fit but are too many together are cut from the end.
func TestFitBodyCutsManyShortLinesFromTheEnd(t *testing.T) {
	body := strings.Repeat("abcdef\n", 200)
	got := fitBody(body, 100, utf16Len, same)
	if n := utf16Len(got); n > 100 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "abcdef\nabcdef") {
		t.Errorf("fitBody = %q (%d units), want the start of the text, marked, at most 100", got, n)
	}
}

// An emoji is one character to Go and two UTF-16 units to the services.
func TestFitBodyCountsUTF16Units(t *testing.T) {
	body := strings.Repeat("😀", 3000)
	got := fitBody(body, 4096, utf16Len, same)
	if n := utf16Len(got); n > 4096 || n < 4000 {
		t.Errorf("fitted to %d units, want close under 4096", n)
	}
}

func TestSlackFitsALongAlertInItsSections(t *testing.T) {
	got := sentJSON(t, NewSlackSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url}
	}, longAlert(signedTarget))
	var all string
	for i, b := range got["blocks"].([]any) {
		text := b.(map[string]any)["text"].(map[string]any)["text"].(string)
		if n := utf16Len(text); n > slackSectionMax {
			t.Errorf("section %d is %d units, over Slack's %d", i, n, slackSectionMax)
		}
		all += text + "\n"
	}
	wantKept(t, "slack", all)
}

func TestDiscordFitsALongAlertInItsDescription(t *testing.T) {
	got := sentJSON(t, NewDiscordSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url + "/discord"}
	}, longAlert(signedTarget))
	desc := got["embeds"].([]any)[0].(map[string]any)["description"].(string)
	if n := utf16Len(desc); n > discordDescriptionMax {
		t.Errorf("description is %d units, over Discord's %d", n, discordDescriptionMax)
	}
	wantKept(t, "discord", desc)
	if !strings.HasSuffix(desc, "["+MuteLabel+"](https://status.example.com/mute/abc)") {
		t.Errorf("description lost its links: %.200q", desc[len(desc)-200:])
	}
}

func TestTelegramFitsALongAlertInOneMessage(t *testing.T) {
	tg := NewTelegramSender(nil)
	got := sentJSON(t, tg, func(url string) map[string]string {
		tg.apiBase = url
		return map[string]string{"bot_token": "123456:ABC", "chat_id": "42"}
	}, longAlert(signedTarget+strings.Repeat("😀", 500)))
	text := got["text"].(string)
	if n := utf16Len(text); n > telegramTextMax {
		t.Errorf("text is %d units, over Telegram's %d", n, telegramTextMax)
	}
	if !strings.HasPrefix(text, "🔴 Checkout API is down\n\n") {
		t.Errorf("text lost its title: %.80q", text)
	}
	wantKept(t, "telegram", text)
}

func TestNtfyFitsALongAlertInItsMessageLimit(t *testing.T) {
	srv, body := recordedBody(t)
	if err := NewNtfySender(nil).Send(t.Context(), map[string]string{"url": srv.URL, "topic": "alerts"},
		longAlert(signedTarget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(*body, &got); err != nil {
		t.Fatal(err)
	}
	message := got["message"].(string)
	if n := len(message); n > ntfyMessageMax {
		t.Errorf("message is %d bytes, over ntfy's %d", n, ntfyMessageMax)
	}
	if n := len(*body); n > 2*ntfyMessageMax {
		t.Errorf("request body is %d bytes, over the %d ntfy reads", n, 2*ntfyMessageMax)
	}
	wantKept(t, "ntfy", message+"\n"+got["click"].(string))
}

// TestNtfySendsWholeAMessageThatFitsDecoded checks that ntfy's limit is
// counted on the decoded message. A thousand < are 1,000 bytes of message
// and 6,000 in the JSON request, which ntfy takes unchanged.
func TestNtfySendsWholeAMessageThatFitsDecoded(t *testing.T) {
	a := longAlert("https://shop.example.com/health")
	a.LastError = `keyword "` + strings.Repeat("<", 1000) + `" not found in response`
	srv, body := recordedBody(t)
	if err := NewNtfySender(nil).Send(t.Context(), map[string]string{"url": srv.URL, "topic": "alerts"}, a); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(*body, &got); err != nil {
		t.Fatal(err)
	}
	if message := got["message"].(string); message != a.Body() {
		t.Errorf("message was shortened to %d bytes, want the %d-byte body whole", len(message), len(a.Body()))
	}
	if n := len(*body); n > 2*ntfyMessageMax {
		t.Errorf("request body is %d bytes, over the %d ntfy reads", n, 2*ntfyMessageMax)
	}
}
