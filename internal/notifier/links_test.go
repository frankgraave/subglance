package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

const testBase = "https://status.example.com/sg"

func TestParseBaseURL(t *testing.T) {
	ok := map[string]string{
		"":                                    "",
		"  ":                                  "",
		"https://status.example.com":          "https://status.example.com",
		"https://status.example.com/":         "https://status.example.com",
		"HTTPS://status.example.com":          "https://status.example.com",
		"http://192.0.2.10:8080":              "http://192.0.2.10:8080",
		"https://example.com/subglance":       "https://example.com/subglance",
		"https://example.com/subglance/":      "https://example.com/subglance",
		"https://example.com/sub glance//":    "https://example.com/sub%20glance",
		" https://status.example.com ":        "https://status.example.com",
		"https://[2001:db8::1]:8443/monitor/": "https://[2001:db8::1]:8443/monitor",
	}
	for in, want := range ok {
		got, err := ParseBaseURL(in)
		if err != nil || got != want {
			t.Errorf("ParseBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"status.example.com":                  "absolute",
		"/subglance":                          "absolute",
		"ftp://status.example.com":            "absolute",
		"https://":                            "no host",
		"https:status.example.com":            "no host",
		"https://admin:pw@status.example.com": "password",
		"https://status.example.com/?a=1":     "query",
		"https://status.example.com/?":        "query",
		"https://status.example.com/#top":     "fragment",
		"https://status.example.com/#":        "fragment",
		"https://status example.com":          "not a URL",
	}
	for in, want := range bad {
		if _, err := ParseBaseURL(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseBaseURL(%q) error = %v; want one mentioning %q", in, err, want)
		}
	}
}

// TestParseBaseURLKeepsCredentialsOutOfErrors: the refusal is printed at
// start-up, so it must not repeat the password it refuses, whichever check
// catches the address first.
func TestParseBaseURLKeepsCredentialsOutOfErrors(t *testing.T) {
	bad := map[string]string{
		"https://admin:s3cret@status.example.com":       "password",
		"https://admin:s3cret@status example.com":       "not a URL",
		"https://admin:s3c#ret@status.example.com":      "not a URL",
		"ftp://admin:s3cret@status.example.com":         "absolute",
		"admin:s3cret@status.example.com":               "absolute",
		"https://admin:s3cret@status.example.com/?a=1":  "password",
		"https://admin:s3cret@status.example.com/#top":  "password",
		"https://admin:s3c%zzret@status.example.com/sg": "not a URL",
		// A slash in the password ends the host early, and the parser
		// then names the rest of the password as a bad port.
		"https://admin:s3c/ret@status.example.com": "not a URL",
	}
	for in, want := range bad {
		_, err := ParseBaseURL(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseBaseURL(%q) error = %v; want one mentioning %q", in, err, want)
			continue
		}
		if strings.Contains(err.Error(), "s3c") {
			t.Errorf("ParseBaseURL(%q) error = %q; it repeats the password", in, err)
		}
	}
}

func TestWithLinks(t *testing.T) {
	a := goldenAlerts()

	if got := withLinks(a["down"], ""); got.IncidentURL != "" || got.MonitorURL != "" {
		t.Errorf("no base URL: links %q %q, want none", got.IncidentURL, got.MonitorURL)
	}

	down := withLinks(a["down"], testBase)
	if down.IncidentURL != testBase+"/incidents#incident-312" || down.MonitorURL != testBase+"/monitors/7" {
		t.Errorf("single alert links = %q, %q", down.IncidentURL, down.MonitorURL)
	}

	noIncident := a["down"]
	noIncident.IncidentID = 0
	if got := withLinks(noIncident, testBase); got.IncidentURL != "" || got.MonitorURL != testBase+"/monitors/7" {
		t.Errorf("alert without an incident: links %q, %q; want the monitor only", got.IncidentURL, got.MonitorURL)
	}

	for _, kind := range []string{"grouped", "digest"} {
		got := withLinks(a[kind], testBase)
		if got.IncidentURL != testBase+"/incidents" || got.MonitorURL != "" {
			t.Errorf("%s: top-level links %q, %q; want the incidents screen only", kind, got.IncidentURL, got.MonitorURL)
		}
		for _, m := range got.Members {
			if !strings.HasPrefix(m.IncidentURL, testBase+"/incidents#incident-") || !strings.HasPrefix(m.MonitorURL, testBase+"/monitors/") {
				t.Errorf("%s member %s: links %q, %q", kind, m.MonitorName, m.IncidentURL, m.MonitorURL)
			}
		}
		if a[kind].Members[0].IncidentURL != "" {
			t.Errorf("%s: withLinks wrote into the caller's members", kind)
		}
	}

	for _, self := range []Alert{
		{MonitorName: testAlertName, Event: string(state.EventIncidentResolved)},
		{MonitorName: "SubGlance", Event: EventBackupFailed, IncidentID: 4},
		{MonitorName: "SubGlance", Event: EventChannelFailing},
	} {
		if got := withLinks(self, testBase); got.IncidentURL != "" || got.MonitorURL != "" {
			t.Errorf("%s: a message about SubGlance itself got links %q, %q", self.Event, got.IncidentURL, got.MonitorURL)
		}
	}
}

// TestEveryChannelLinksToTheIncident is the other half of the golden test:
// with a base URL, every channel's message carries the incident link, and
// every channel with room for two carries the monitor's too.
func TestEveryChannelLinksToTheIncident(t *testing.T) {
	incident := testBase + "/incidents#incident-312"
	monitor := testBase + "/monitors/7"
	for channel, got := range renderAll(t, withLinks(goldenAlerts()["down"], testBase)) {
		if channel == "webhook-body" {
			// A body of one's own carries what its template asks for;
			// TestWebhookPayloadCarriesTheLinks checks the placeholders.
			continue
		}
		if !strings.Contains(got, incident) {
			t.Errorf("%s: no incident link in\n%s", channel, got)
		}
		if channel != "sms" && !strings.Contains(got, monitor) {
			t.Errorf("%s: no monitor link in\n%s", channel, got)
		}
	}
	for channel, got := range renderAll(t, withLinks(goldenAlerts()["grouped"], testBase)) {
		if channel == "webhook-body" {
			continue
		}
		if !strings.Contains(got, testBase+"/incidents") {
			t.Errorf("%s grouped: no link to the incidents screen in\n%s", channel, got)
		}
	}
}

func TestWebhookPayloadCarriesTheLinks(t *testing.T) {
	srv, body := recordedBody(t)
	cfg := map[string]string{"url": srv.URL}
	if err := NewWebhookSender(nil).Send(context.Background(), cfg, withLinks(goldenAlerts()["down"], testBase)); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(*body, &got); err != nil {
		t.Fatal(err)
	}
	if got["incident_url"] != testBase+"/incidents#incident-312" || got["monitor_url"] != testBase+"/monitors/7" {
		t.Errorf("payload links = %v, %v", got["incident_url"], got["monitor_url"])
	}

	srv, body = recordedBody(t)
	cfg = map[string]string{"url": srv.URL, "body": `{"i": "{{incident_url}}", "m": "{{ monitor_url }}"}`}
	if err := NewWebhookSender(nil).Validate(cfg); err != nil {
		t.Fatalf("a body using the link placeholders is refused: %v", err)
	}
	if err := NewWebhookSender(nil).Send(context.Background(), cfg, withLinks(goldenAlerts()["down"], testBase)); err != nil {
		t.Fatal(err)
	}
	if want := `{"i": "` + testBase + `/incidents#incident-312", "m": "` + testBase + `/monitors/7"}`; string(*body) != want {
		t.Errorf("body = %s, want %s", *body, want)
	}
}

func TestSMSLinkMakesRoomOrStaysOut(t *testing.T) {
	a := goldenAlerts()["down"]
	a.MonitorName = "A monitor with a name long enough to be shortened when a link needs the room"
	a.LastError = strings.Repeat("connection refused by the upstream ", 4)

	got := smsMessage(withLinks(a, testBase), "Europe/Amsterdam", 0)
	if !strings.HasSuffix(got, " "+testBase+"/incidents#incident-312") {
		t.Errorf("SMS does not end with the incident link: %q", got)
	}
	if n := smsSeptetLen(got); n > smsSeptets {
		t.Errorf("SMS with a link is %d septets, more than one part", n)
	}

	// A short alert keeps its link beside the held-back note, unshortened.
	short := goldenAlerts()["down"]
	withNote := smsMessage(withLinks(short, testBase), "Europe/Amsterdam", 3)
	if want := smsText(short, "Europe/Amsterdam", smsSeptets) + " " + testBase + "/incidents#incident-312 (+3 alerts not sent by SMS, see SubGlance)"; withNote != want {
		t.Errorf("SMS with a link and the held-back note:\n got %q\nwant %q", withNote, want)
	}
	// A long one beside the note would drop under half a message: no link.
	if got := smsMessage(withLinks(a, testBase), "Europe/Amsterdam", 3); strings.Contains(got, "http") || smsSeptetLen(got) > smsSeptets {
		t.Errorf("a long alert with the note kept a link that left it under half a message: %q", got)
	}

	long := withLinks(a, "https://status.example.com/"+strings.Repeat("deep/", 12))
	if got := smsMessage(long, "Europe/Amsterdam", 0); strings.Contains(got, "http") {
		t.Errorf("a link that leaves the alert under half the message is sent anyway: %q", got)
	}
	if got, want := smsMessage(long, "Europe/Amsterdam", 0), smsMessage(a, "Europe/Amsterdam", 0); got != want {
		t.Errorf("without its link the SMS is %q, want the linkless %q", got, want)
	}

	// A host name in another script: ParseBaseURL keeps it as written, and
	// GSM-7 cannot write it.
	wide := withLinks(a, "https://статус.example.com")
	if got := smsMessage(wide, "Europe/Amsterdam", 0); strings.Contains(got, "http") {
		t.Errorf("a link outside GSM-7 is sent, changed or not: %q", got)
	}
}

// TestNotifierAddsLinksAtSendTimeOnly checks the wiring: the base URL from
// Options reaches the sender, and the outbox row stays the alert itself, so
// a retry after the address changes links to the new one.
func TestNotifierAddsLinksAtSendTimeOnly(t *testing.T) {
	db := groupDB(t)
	ch := groupChannel(t, db, "ops")
	m := groupMonitor(t, db, "api", ch.ID)
	at := time.Date(2026, 10, 2, 3, 14, 0, 0, time.UTC)
	inc := openIncident(t, db, m.ID, at, "timeout")

	sender := &fakeSender{}
	n := New(Options{
		DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders:     map[string]Sender{store.ChannelWebhook: sender},
		GroupWindow: GroupingDisabled, BaseURL: testBase, Now: func() time.Time { return at },
	})
	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, at); err != nil {
		t.Fatal(err)
	}
	if raw := decodeOnlyDelivery(t, db); raw.IncidentURL != "" || raw.MonitorURL != "" {
		t.Errorf("the outbox row carries links %q, %q; they belong to the send", raw.IncidentURL, raw.MonitorURL)
	}
	if _, err := n.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := sender.delivered()
	if len(got) != 1 {
		t.Fatalf("%d deliveries, want 1", len(got))
	}
	wantIncident := testBase + "/incidents#incident-" + fmt.Sprint(inc.ID)
	if got[0].IncidentURL != wantIncident || got[0].MonitorURL != testBase+"/monitors/"+fmt.Sprint(m.ID) {
		t.Errorf("sent links = %q, %q; want %q and the monitor", got[0].IncidentURL, got[0].MonitorURL, wantIncident)
	}
}
