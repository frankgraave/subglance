package notifier

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// noticeAlert is an alert about a certificate notice for the given event.
func noticeAlert(event state.Event) Alert {
	return Alert{
		MonitorID: 3, MonitorName: "api", MonitorType: "ssl", Target: "api.example.com:443",
		Event: string(event), IncidentID: 9, Notice: true, Cause: "cert_expiry",
		LastError: "certificate expires in 6 days (on 2026-10-12)",
		StartedAt: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC),
		At:        time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC),
	}
}

// None of a notice's messages says "down": the service answered every check.
func TestANoticeIsNeverWordedAsAnOutage(t *testing.T) {
	cases := []struct {
		event     state.Event
		title     string
		down      bool
		status    string
		smsStatus string
	}{
		{state.EventIncidentConfirmed, "api: certificate expires soon", true, "expiring", "CERT EXPIRING"},
		{state.EventIncidentReminder, "api: certificate still expires soon", true, "expiring", "CERT EXPIRING"},
		{state.EventIncidentResolved, "api: certificate no longer expires soon", false, "up", "CERT OK"},
	}
	for _, c := range cases {
		t.Run(string(c.event), func(t *testing.T) {
			a := noticeAlert(c.event)
			if got := a.Title(); got != c.title {
				t.Errorf("title = %q, want %q", got, c.title)
			}
			if got := a.Down(); got != c.down {
				t.Errorf("Down() = %v, want %v", got, c.down)
			}
			if got := alertStatus(a); got != c.status {
				t.Errorf("{{status}} = %q, want %q", got, c.status)
			}
			body := a.Body()
			for _, word := range []string{"Down for", "Since ", "is down"} {
				if strings.Contains(body, word) {
					t.Errorf("body says %q: %q", word, body)
				}
			}
			if c.down && !strings.Contains(body, "expires in 6 days") {
				t.Errorf("body = %q, want the days left", body)
			}
			sms := smsText(a, "UTC", 160)
			if !strings.HasPrefix(sms, c.smsStatus+" ") || strings.Contains(sms, "DOWN") {
				t.Errorf("sms = %q, want it to start with %q and never say DOWN", sms, c.smsStatus)
			}
		})
	}
}

// A notice is not batched with outages: "3 monitors are down" made of one
// outage and two certificates would be false twice over.
func TestANoticeIsNotGroupedWithAnOutage(t *testing.T) {
	db := groupDB(t)
	clock := newTestClock()
	n := groupNotifier(t, db, clock, 90*time.Second)
	ch := groupChannel(t, db, "ops")

	down := groupMonitor(t, db, "down", ch.ID)
	inc := openIncident(t, db, down.ID, clock.Now(), "connection refused")
	if err := n.Enqueue(context.Background(), down, inc, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatal(err)
	}
	cert := groupMonitor(t, db, "cert", ch.ID)
	notice, err := db.OpenNotice(context.Background(), cert.ID, clock.Now(), "cert_expiry", "certificate expires in 6 days")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Enqueue(context.Background(), cert, notice, state.EventIncidentConfirmed, clock.Now()); err != nil {
		t.Fatal(err)
	}

	if got := countDeliveries(t, db); got != 1 {
		t.Fatalf("deliveries while the window is open = %d, want the notice alone", got)
	}
	first := decodeOnlyDelivery(t, db)
	if !first.Notice || first.Grouped() || first.MonitorName != "cert" {
		t.Errorf("first delivery = %+v, want the notice on its own", first)
	}

	clock.Advance(2 * time.Minute)
	n.flushDue(context.Background())
	if got := countDeliveries(t, db); got != 2 {
		t.Fatalf("deliveries after the window = %d, want the notice and the outage", got)
	}
}

// A quiet night that only saw a certificate notice says so, not "went down".
func TestADigestCountsANoticeApart(t *testing.T) {
	outage := Alert{MonitorID: 1, MonitorName: "web", Event: string(state.EventIncidentConfirmed),
		StartedAt: time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC), At: time.Date(2026, 10, 6, 1, 1, 0, 0, time.UTC)}
	back := outage
	back.Event, back.At = string(state.EventIncidentResolved), time.Date(2026, 10, 6, 1, 20, 0, 0, time.UTC)
	notice := noticeAlert(state.EventIncidentConfirmed)

	only := BuildDigest([]Alert{notice}, "UTC", notice.At)
	if got := only.Title(); got != "api: certificate expires soon" {
		t.Errorf("title of a digest of one notice = %q", got)
	}
	if !only.Down() {
		t.Error("a digest with an open notice is not marked as needing attention")
	}
	if got := alertStatus(only); got != "expiring" {
		t.Errorf("{{status}} of a digest of one notice = %q, want expiring", got)
	}

	if got := alertStatus(BuildDigest([]Alert{outage, notice}, "UTC", notice.At)); got != "down" {
		t.Errorf("{{status}} of a digest with an outage still open = %q, want down", got)
	}
	mixed := BuildDigest([]Alert{outage, back, notice}, "UTC", notice.At)
	if got := alertStatus(mixed); got != "expiring" {
		t.Errorf("{{status}} of a digest whose outage ended and whose notice is open = %q, want expiring", got)
	}
	if got := mixed.Title(); got != "1 monitor went down and recovered during quiet hours" {
		t.Errorf("title = %q, want only the outage counted", got)
	}
	body := mixed.Body()
	if !strings.Contains(body, "• api: certificate expires soon") || !strings.Contains(body, "• web: down 01:00–01:20") {
		t.Errorf("body = %q, want a line for each", body)
	}
}

// A domain monitor's notice names the registration, not a certificate, in
// every place a certificate notice names the certificate: the title, the
// text message, and the quiet-hours digest.
func TestADomainNoticeNamesTheRegistration(t *testing.T) {
	cases := []struct {
		event     state.Event
		title     string
		smsStatus string
	}{
		{state.EventIncidentConfirmed, "example.com: domain registration expires soon", "DOMAIN EXPIRING"},
		{state.EventIncidentReminder, "example.com: domain registration still expires soon", "DOMAIN EXPIRING"},
		{state.EventIncidentResolved, "example.com: domain registration no longer expires soon", "DOMAIN OK"},
	}
	for _, c := range cases {
		t.Run(string(c.event), func(t *testing.T) {
			a := noticeAlert(c.event)
			a.MonitorName, a.MonitorType, a.Target = "example.com", "domain", "example.com"
			a.Cause, a.LastError = "domain_expiry", "domain registration of example.com expires in 12 days (on 2026-10-19)"
			if got := a.Title(); got != c.title {
				t.Errorf("title = %q, want %q", got, c.title)
			}
			if sms := smsText(a, "UTC", 160); !strings.HasPrefix(sms, c.smsStatus+" ") || strings.Contains(sms, "CERT") {
				t.Errorf("sms = %q, want it to start with %q", sms, c.smsStatus)
			}
		})
	}

	notice := noticeAlert(state.EventIncidentConfirmed)
	notice.MonitorID, notice.MonitorName, notice.MonitorType = 4, "example.com", "domain"
	cert := noticeAlert(state.EventIncidentConfirmed)
	body := BuildDigest([]Alert{cert, notice}, "UTC", notice.At).Body()
	if !strings.Contains(body, "• example.com: domain registration expires soon") || !strings.Contains(body, "• api: certificate expires soon") {
		t.Errorf("digest body = %q, want each notice named for what it is", body)
	}
}
