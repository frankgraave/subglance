package statuspage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A certificate that expires soon is not an outage. The public page keeps the
// lamp at up, adds the note, lists no outage and charges no downtime; and it
// never says when the certificate expires.
func TestAnExpiringCertificateIsUpWithANote(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := seed(t, now)
	const secretExpiry = "certificate expires in 6 days (on 2026-10-12)"
	if _, err := f.db.OpenNotice(t.Context(), f.monitor.ID, now.Add(-time.Hour), "cert_expiry", secretExpiry); err != nil {
		t.Fatal(err)
	}
	f.beat(t, now.Add(-time.Minute), true, "up")

	page := f.build(t, fakeRecovery{f.monitor.ID: true}, now)
	e := page.Entries[0]
	if e.Status != StatusUp || !e.CertificateExpiring {
		t.Fatalf("entry = status %q, certificate_expiring %v; want up with the note", e.Status, e.CertificateExpiring)
	}
	if page.Summary.Up != 1 || page.Summary.Down != 0 || page.Summary.Degraded != 0 {
		t.Errorf("summary = %+v, want one up", page.Summary)
	}
	if len(page.Outages) != 0 {
		t.Errorf("a notice is listed as an outage: %+v", page.Outages)
	}
	if e.Uptime90d == nil || *e.Uptime90d != 100 {
		t.Errorf("uptime = %v, want 100", e.Uptime90d)
	}

	html := render(t, page)
	if !strings.Contains(html, "Certificate expires soon") {
		t.Error("the page does not show the note")
	}
	if !strings.Contains(html, `<span class="led-label">Up</span>`) {
		t.Error("the lamp beside the note is not up")
	}
	for _, leak := range []string{secretExpiry, "2026-10-12", "6 days"} {
		if strings.Contains(html, leak) {
			t.Errorf("the page publishes %q", leak)
		}
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "2026-10-12") || !strings.Contains(string(raw), `"certificate_expiring":true`) {
		t.Errorf("public JSON = %s, want the flag and no date", raw)
	}
}

// The note is left out beside anything but up: "expires soon" next to a
// paused service would read as the reason it is not watched.
func TestTheNoteIsOnlyBesideUp(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := seed(t, now)
	if _, err := f.db.OpenNotice(t.Context(), f.monitor.ID, now.Add(-time.Hour), "cert_expiry", "soon"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMonitorEnabled(t.Context(), f.monitor.ID, false); err != nil {
		t.Fatal(err)
	}
	e := f.build(t, nil, now).Entries[0]
	if e.Status != StatusNotMonitored || e.CertificateExpiring {
		t.Errorf("entry = status %q, certificate_expiring %v; want not monitored and no note", e.Status, e.CertificateExpiring)
	}
}
