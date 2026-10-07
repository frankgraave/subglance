package notifier

import (
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// updateGolden rewrites the files under testdata/no-base-url from what the
// senders produce now. Run it only for a change that is meant to alter every
// message, and read the diff before committing it.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/no-base-url from the current senders")

// goldenAlerts is one alert of every shape a channel renders differently: an
// outage, a recovery, a grouped outage, a quiet-hours digest and a
// certificate notice. Each carries a monitor id and an incident id, so a link
// would have something to point at if one were added without a base URL.
func goldenAlerts() map[string]Alert {
	at := time.Date(2026, 10, 2, 3, 14, 10, 0, time.UTC)
	down := Alert{
		MonitorID: 7, MonitorName: "Checkout API", MonitorType: "http",
		Target: "https://shop.example.com/health", Event: string(state.EventIncidentConfirmed),
		IncidentID: 312, StartedAt: at.Add(-90 * time.Second), At: at,
		Cause: "timeout", LastError: "no response within 10s",
	}
	up := down
	up.Event = string(state.EventIncidentResolved)
	up.At = at.Add(4 * time.Minute)
	other := down
	other.MonitorID, other.MonitorName, other.IncidentID = 8, "Search", 313
	other.Target = "https://search.example.com"
	other.At = at.Add(20 * time.Second)
	notice := down
	notice.Notice, notice.Cause, notice.LastError = true, "cert_expiry", "certificate expires in 9 days"
	return map[string]Alert{
		"down":    down,
		"up":      up,
		"grouped": Summarise([]Alert{down, other}),
		"digest":  BuildDigest([]Alert{down, up, other}, "Europe/Amsterdam", at.Add(4*time.Hour)),
		"notice":  notice,
	}
}

// recordedBody is a server that keeps the body of the one request it gets.
func recordedBody(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// renderAll returns what every channel type sends for one alert, keyed by
// the channel's name. HTTP channels are sent to an in-process server and the
// request body recorded; e-mail and SMS are rendered directly, since their
// transports are not HTTP requests to a URL the test can point anywhere.
func renderAll(t *testing.T, a Alert) map[string]string {
	t.Helper()
	out := map[string]string{}
	send := func(name string, sender Sender, cfg func(url string) map[string]string) {
		srv, body := recordedBody(t)
		if err := sender.Send(context.Background(), cfg(srv.URL), a); err != nil {
			t.Fatalf("%s: Send: %v", name, err)
		}
		out[name] = string(*body)
	}
	plain := func(url string) map[string]string { return map[string]string{"url": url} }
	send("webhook", NewWebhookSender(nil), plain)
	send("webhook-body", NewWebhookSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url, "body": `{"text": "{{summary}}", "details": "{{details}}", "status": "{{status}}"}`}
	})
	send("slack", NewSlackSender(nil), plain)
	send("discord", NewDiscordSender(nil), func(url string) map[string]string {
		return map[string]string{"url": url + "/discord"}
	})
	tg := NewTelegramSender(nil)
	send("telegram", tg, func(url string) map[string]string {
		tg.apiBase = url
		return map[string]string{"bot_token": "123456:ABC", "chat_id": "42"}
	})
	send("ntfy", NewNtfySender(nil), func(url string) map[string]string {
		return map[string]string{"url": url, "topic": "alerts"}
	})
	send("gotify", NewGotifySender(nil), func(url string) map[string]string {
		return map[string]string{"url": url, "token": "app-token"}
	})
	out["email"] = buildMessage("subglance@example.com", []string{"ops@example.com"}, a)
	out["sms"] = smsMessage(a, "Europe/Amsterdam", 0)
	return out
}

// TestWithoutBaseURLEveryMessageIsUnchanged pins every channel's message,
// byte for byte, as it was before alerts could link back to SubGlance. An
// instance that never sets --base-url must not see one character of its
// alerts change: a receiver may be a script that parses them.
func TestWithoutBaseURLEveryMessageIsUnchanged(t *testing.T) {
	dir := filepath.Join("testdata", "no-base-url")
	for kind, a := range goldenAlerts() {
		for channel, got := range renderAll(t, a) {
			path := filepath.Join(dir, channel+"-"+kind+".txt")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run with -update-golden to create it)", path, err)
			}
			if got != string(want) {
				t.Errorf("%s %s alert changed without a base URL:\ngot:\n%s\nwant:\n%s", channel, kind, got, want)
			}
		}
	}
}
