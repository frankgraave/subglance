package kumaimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
)

func domainSetting(value string) []row {
	return []row{{"key": "domainExpiryNotifyDays", "value": value, "type": "general"}}
}

// warned is a Kuma monitor with the domain expiry warning on.
func warned(r row) row {
	r["domain_expiry_notification"] = int64(1)
	return r
}

// domainMonitors returns the monitors the conversion added for Kuma's domain
// expiry warning.
func domainMonitors(res Result) []configfile.Monitor {
	var out []configfile.Monitor
	for _, m := range res.Document.Monitors {
		if m.Type == "domain" {
			out = append(out, m)
		}
	}
	return out
}

// Kuma warned before the registration of a monitor's domain expired; the
// domain becomes a domain monitor that warns as early, checked once a day.
func TestDomainWarningBecomesADomainMonitor(t *testing.T) {
	cases := []struct {
		name     string
		row      row
		settings []row
		target   string // "" when no domain monitor is added
		warn     int
	}{
		{"Kuma's default list", warned(httpRow(row{"url": "https://shop.example.com/health"})), nil, "example.com", 22},
		{"the largest listed day leads", warned(httpRow(nil)), domainSetting("[30, 7]"), "example.com", 31},
		{"digits as text", warned(httpRow(nil)), domainSetting(`["45"]`), "example.com", 46},
		{"a registrable domain under a two-label suffix", warned(httpRow(row{"url": "https://www.shop.example.co.uk/"})),
			nil, "example.co.uk", 22},
		{"an address with a port", warned(httpRow(row{"url": "https://api.example.org:8443/v1"})), nil, "example.org", 22},
		{"a trailing dot and capitals", warned(httpRow(row{"url": "https://WWW.Example.NET./"})), nil, "example.net", 22},
		{"keyword monitor", warned(httpRow(row{"type": "keyword", "keyword": "ok"})), nil, "example.com", 22},
		{"json query monitor", warned(httpRow(row{"type": "json-query", "json_path": "ok", "expected_value": "true"})),
			nil, "example.com", 22},
		{"port monitor", warned(portRow(nil)), nil, "example.com", 22},
		{"ping monitor", warned(row{"id": int64(1), "name": "Router", "type": "ping", "hostname": "router.example.net",
			"active": int64(1), "interval": int64(60)}), nil, "example.net", 22},
		{"dns monitor", warned(row{"id": int64(1), "name": "DNS", "type": "dns", "hostname": "example.org",
			"dns_resolve_type": "A", "dns_resolve_server": "1.1.1.1", "active": int64(1), "interval": int64(60)}),
			nil, "example.org", 22},
		{"a type SubGlance has no check for", warned(row{"id": int64(1), "name": "Broker", "type": "mqtt",
			"hostname": "mqtt.example.com", "active": int64(1), "interval": int64(60)}), nil, "example.com", 22},
		{"364 days is the most that fits", warned(httpRow(nil)), domainSetting("[364]"), "example.com", 365},
		{"more than a year is clamped", warned(httpRow(nil)), domainSetting("[400]"), "example.com", 365},

		{"warning off", httpRow(row{"domain_expiry_notification": int64(0)}), nil, "", 0},
		{"Kuma 1.23 has no such column", httpRow(nil), nil, "", 0},
		{"an empty list never warned", warned(httpRow(nil)), domainSetting("[]"), "", 0},
		{"days that are never reached", warned(httpRow(nil)), domainSetting("[0, -3]"), "", 0},
		{"an IP address", warned(httpRow(row{"url": "https://192.0.2.10/"})), nil, "", 0},
		{"an IPv6 address", warned(httpRow(row{"url": "https://[2001:db8::1]:8443/"})), nil, "", 0},
		{"a local name", warned(portRow(row{"hostname": "nas.lan"})), nil, "", 0},
		{"a name without a dot", warned(portRow(row{"hostname": "localhost"})), nil, "", 0},
		{"a public suffix on its own", warned(portRow(row{"hostname": "co.uk"})), nil, "", 0},
		{"a push monitor has no target", warned(row{"id": int64(1), "name": "Job", "type": "push", "active": int64(1),
			"interval": int64(60)}), nil, "", 0},
		{"a docker monitor never had the warning", warned(row{"id": int64(1), "name": "Container", "type": "docker",
			"hostname": "app.example.com", "active": int64(1), "interval": int64(60)}), nil, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: []row{tc.row}, settings: tc.settings})
			added := domainMonitors(res)
			if res.DomainMonitors != len(added) {
				t.Errorf("DomainMonitors = %d, but %d domain monitors are in the file", res.DomainMonitors, len(added))
			}
			if tc.target == "" {
				if len(added) != 0 {
					t.Fatalf("domain monitors = %+v, want none", added)
				}
				return
			}
			if len(added) != 1 {
				t.Fatalf("domain monitors = %+v, want one; notes: %s", added, notesOf(res))
			}
			m := added[0]
			if m.Target != tc.target || m.Name != tc.target+" registration" {
				t.Errorf("domain monitor = %q on %q, want %q on %q", m.Name, m.Target, tc.target+" registration", tc.target)
			}
			if got := deref(m.DomainWarnDays); got != tc.warn {
				t.Errorf("domain_warn_days = %d, want %d", got, tc.warn)
			}
			if deref(m.IntervalS) != 86400 || !deref(m.Enabled) {
				t.Errorf("interval_s = %d, enabled = %v, want 86400 and true", deref(m.IntervalS), deref(m.Enabled))
			}
			notes := notesOf(res)
			if !strings.Contains(notes, `monitor "`+tc.target+` registration": added to keep Kuma's domain expiry warning for "`) {
				t.Errorf("notes = %q, want the added monitor listed", notes)
			}
			if clamped := strings.Contains(notes, "SubGlance warns at most 365 days ahead"); clamped != (tc.warn == 365 && tc.name != "364 days is the most that fits") {
				t.Errorf("notes = %q: clamping listed = %v", notes, clamped)
			}
		})
	}
}

// Kuma keeps a domain's warning state per domain, so the monitors on one
// domain become one domain monitor, alerting through every channel any of
// them had, and running when any of them ran.
func TestDomainWarningsAreOnePerDomain(t *testing.T) {
	notifications := []row{
		notificationRow(1, "Ops Discord", `{"type":"discord","discordWebhookUrl":"https://discord.example/x"}`),
		notificationRow(2, "Ops Slack", `{"type":"slack","slackwebhookURL":"https://hooks.slack.example/x"}`),
		notificationRow(3, "On-call", `{"type":"PagerDuty"}`),
	}
	monitors := []row{
		warned(httpRow(row{"id": int64(1), "name": "Shop", "url": "https://shop.example.com/"})),
		warned(httpRow(row{"id": int64(2), "name": "API", "url": "https://api.example.com/", "active": int64(0)})),
		warned(portRow(row{"id": int64(3), "name": "Mail", "hostname": "mail.example.com"})),
		warned(httpRow(row{"id": int64(4), "name": "Old blog", "url": "https://blog.example.org/", "active": int64(0)})),
		warned(httpRow(row{"id": int64(5), "name": "Pager", "url": "https://status.example.net/"})),
		httpRow(row{"id": int64(6), "name": "Docs", "url": "https://docs.example.net/"}),
		// A Kuma monitor already named like the added one keeps its key.
		httpRow(row{"id": int64(7), "name": "example.com registration", "url": "https://whois.example/"}),
	}
	links := []row{linkRow(1, 1, 2), linkRow(2, 2, 1), linkRow(3, 3, 2), linkRow(4, 5, 3), linkRow(5, 6, 1)}
	res := convert(source{monitors: monitors, notifications: notifications, links: links})

	added := domainMonitors(res)
	if len(added) != 3 || res.DomainMonitors != 3 {
		t.Fatalf("domain monitors = %+v (counted %d), want example.com, example.org and example.net", added, res.DomainMonitors)
	}
	if got := len(res.Document.Monitors) - res.DomainMonitors; got != len(monitors) {
		t.Errorf("%d converted Kuma monitors, want %d", got, len(monitors))
	}
	want := []struct {
		target   string
		key      string
		enabled  bool
		channels []string
	}{
		{"example.com", "example-com-registration-2", true, []string{"ops-discord", "ops-slack"}},
		{"example.org", "example-org-registration", false, []string{}},
		{"example.net", "example-net-registration", true, []string{}},
	}
	for i, w := range want {
		m := added[i]
		if m.Target != w.target || m.Key != w.key || deref(m.Enabled) != w.enabled || !slices.Equal(m.Channels, w.channels) {
			t.Errorf("domain monitor %d = %s (%s) enabled=%v channels=%v, want %s (%s) enabled=%v channels=%v",
				i, m.Target, m.Key, deref(m.Enabled), m.Channels, w.target, w.key, w.enabled, w.channels)
		}
	}
	notes := notesOf(res)
	for _, line := range []string{
		`monitor "example.com registration": added to keep Kuma's domain expiry warning for "Shop", "API" and "Mail": ` +
			`Kuma warned with 21 days or fewer left, this domain monitor warns with fewer than 22`,
		`monitor "example.org registration": switched off: every Kuma monitor that asked for this warning was paused`,
		`monitor "example.net registration": none of the Kuma monitors that asked for this warning has a channel that came over`,
	} {
		if !strings.Contains(notes, line) {
			t.Errorf("notes = %s\nwant a line containing %q", notes, line)
		}
	}
	if strings.Contains(notes, `"example.com registration": switched off`) ||
		strings.Contains(notes, `"example.com registration": none of the Kuma monitors`) {
		t.Errorf("example.com is listed as paused or without channels: %s", notes)
	}
	if strings.Contains(notes, `"Docs"`) {
		t.Errorf("Docs had the warning off and is listed: %s", notes)
	}
}

// A name a domain monitor cannot watch had Kuma's warning all the same; the
// warning is listed as not imported, naming the domain Kuma looked up.
func TestDomainWarningsThatCannotComeOverAreListed(t *testing.T) {
	cases := []struct {
		name, host, note string
	}{
		{"a shared suffix directly under a TLD", "frank.github.io",
			`domain warning "Gateway" (port): Kuma warned before the registration of github.io expired, the domain of the shared suffix`},
		{"a shared suffix under a two-label suffix", "shop.myspreadshop.co.uk",
			"Kuma warned before the registration of myspreadshop.co.uk expired"},
		{"an internationalised name", "bücher.example",
			"Kuma warned before the registration of bücher.example expired; a domain monitor takes the name in the xn-- form"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: []row{warned(portRow(row{"hostname": tc.host}))}})
			if added := domainMonitors(res); len(added) != 0 {
				t.Errorf("domain monitors = %+v, want none", added)
			}
			if len(res.Document.Monitors) != 1 {
				t.Errorf("the port monitor itself did not come over: %s", notesOf(res))
			}
			var found bool
			for _, n := range res.Skipped {
				found = found || (n.Kind == "domain warning" && strings.Contains(n.String(), tc.note))
			}
			if !found {
				t.Errorf("not imported = %s, want a domain warning containing %q", notesOf(res), tc.note)
			}
		})
	}
}

// Kuma's list of domain warning days is a setting of its own, read the same
// way as the certificate one and independent of it.
func TestDomainWarnDaysAreTheirOwnSetting(t *testing.T) {
	settings := append(certSetting("[60]"), domainSetting("[3]")...)
	r := warned(httpRow(row{"expiry_notification": int64(1)}))
	res := convert(source{monitors: []row{r}, settings: settings})
	added := domainMonitors(res)
	if len(added) != 1 || deref(added[0].DomainWarnDays) != 4 {
		t.Fatalf("domain monitors = %+v, want one with domain_warn_days 4", added)
	}
	if got := deref(res.Document.Monitors[0].SSLWarnDays); got != 61 {
		t.Errorf("ssl_warn_days = %d, want 61", got)
	}
	if got := notifyDays(domainSetting(`{"days": 30}`), "domainExpiryNotifyDays", kumaDomainDays); !slices.Equal(got, []int{7, 14, 21}) {
		t.Errorf("a setting that is not a list = %v, want Kuma's default", got)
	}
}
