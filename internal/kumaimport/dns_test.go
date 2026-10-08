package kumaimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
)

func dnsRow(over row) row {
	r := row{"id": int64(1), "name": "Mail DNS", "type": "dns", "hostname": "example.com", "active": int64(1),
		"interval": int64(60), "maxretries": int64(0), "port": int64(53), "dns_resolve_type": "A",
		"dns_resolve_server": "1.1.1.1"}
	for k, v := range over {
		r[k] = v
	}
	return r
}

// A Kuma DNS monitor becomes a dns monitor on the same name, record type and
// resolver, with the expected values its conditions stated.
func TestDNSMonitorsComeOver(t *testing.T) {
	cases := []struct {
		name     string
		row      row
		target   string
		record   string
		expected []string
		resolver string
		note     string
	}{
		{"1.23, no conditions", dnsRow(nil), "example.com", "A", []string{}, "1.1.1.1", ""},
		{"2.x, empty conditions", dnsRow(row{"conditions": "[]"}), "example.com", "A", []string{}, "1.1.1.1", ""},
		{"trailing dot and lower-case type", dnsRow(row{"hostname": "example.com.", "dns_resolve_type": "aaaa"}),
			"example.com", "AAAA", []string{}, "1.1.1.1", ""},
		{"no type falls back to Kuma's default", dnsRow(row{"dns_resolve_type": nil}), "example.com", "A", []string{}, "1.1.1.1", ""},
		{"resolver on another port", dnsRow(row{"port": int64(5353)}), "example.com", "A", []string{}, "1.1.1.1:5353", ""},
		{"no port column value", dnsRow(row{"port": nil}), "example.com", "A", []string{}, "1.1.1.1", ""},
		{"IPv6 resolver", dnsRow(row{"dns_resolve_server": "2606:4700:4700::1111"}),
			"example.com", "A", []string{}, "2606:4700:4700::1111", ""},
		{"bracketed IPv6 resolver on another port", dnsRow(row{"dns_resolve_server": "[2606:4700:4700::1111]", "port": int64(5353)}),
			"example.com", "A", []string{}, "[2606:4700:4700::1111]:5353", ""},
		{"resolver by host name", dnsRow(row{"dns_resolve_server": "adguard"}), "example.com", "A", []string{}, "adguard",
			"adguard is a host name; if it resolves to a private address, SubGlance asks it only when started with --allow-private-targets"},
		{"several resolvers", dnsRow(row{"dns_resolve_server": " 1.1.1.1, 8.8.8.8,9.9.9.9 "}), "example.com", "A", []string{}, "1.1.1.1",
			"Kuma tried 3 resolvers in turn; a dns monitor asks one, so 1.1.1.1 is kept and 8.8.8.8, 9.9.9.9 left out"},
		{"private resolver", dnsRow(row{"dns_resolve_server": "192.168.1.2"}), "example.com", "A", []string{}, "192.168.1.2",
			"--allow-private-targets"},
		{"CNAME equals", dnsRow(row{"dns_resolve_type": "CNAME", "hostname": "www.example.com",
			"conditions": `[{"type":"expression","andOr":"and","variable":"record","operator":"equals","value":"example.netlify.app"}]`}),
			"www.example.com", "CNAME", []string{"example.netlify.app"}, "1.1.1.1", ""},
		{"TXT equals with a numeric value", dnsRow(row{"dns_resolve_type": "TXT",
			"conditions": `[{"type":"expression","variable":"record","operator":"equals","value":42}]`}),
			"example.com", "TXT", []string{"42"}, "1.1.1.1", ""},
		{"TXT equals", dnsRow(row{"dns_resolve_type": "TXT", "hostname": "_dmarc.example.com",
			"conditions": `[{"type":"expression","variable":"record","operator":"equals","value":"v=DMARC1; p=reject"}]`}),
			"_dmarc.example.com", "TXT", []string{"v=DMARC1; p=reject"}, "1.1.1.1", ""},
		{"A equals", dnsRow(row{"conditions": `[{"type":"expression","variable":"record","operator":"equals","value":" 93.184.215.14 "}]`}),
			"example.com", "A", []string{"93.184.215.14"}, "1.1.1.1", "expects exactly the records listed"},
		{"MX equals", dnsRow(row{"dns_resolve_type": "MX",
			"conditions": `[{"type":"expression","variable":"record","operator":"equals","value":"mx1.example.com"}]`}),
			"example.com", "MX", []string{"mx1.example.com"}, "1.1.1.1", "expects exactly the records listed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			m, ok := convertMonitor(tc.row, &res)
			if !ok {
				t.Fatalf("skipped: %s", notesOf(res))
			}
			if m.Type != "dns" || m.Target != tc.target || m.DNS == nil {
				t.Fatalf("monitor = %+v", m)
			}
			if m.DNS.RecordType != tc.record || m.DNS.Resolver != tc.resolver ||
				m.DNS.Expected == nil || !slices.Equal(m.DNS.Expected, tc.expected) {
				t.Errorf("dns = %+v, want %s %q via %q", *m.DNS, tc.record, tc.expected, tc.resolver)
			}
			if deref(m.IntervalS) != 60 || !deref(m.Enabled) {
				t.Errorf("interval or enabled lost: %+v", m)
			}
			if got := notesOf(res); (tc.note == "") != (got == "") || !strings.Contains(got, tc.note) {
				t.Errorf("notes = %q, want one containing %q", got, tc.note)
			}
		})
	}
}

// What a dns monitor cannot express is left out with a reason. Converted to
// "any record passes", a condition would make a check that passes where Kuma
// failed.
func TestDNSMonitorsThatCannotComeOver(t *testing.T) {
	cond := func(op, value string) string {
		return `[{"type":"expression","variable":"record","operator":"` + op + `","value":"` + value + `"}]`
	}
	cases := []struct {
		name   string
		row    row
		reason string
	}{
		{"NS record", dnsRow(row{"dns_resolve_type": "NS"}), "does not check NS records"},
		{"SRV record", dnsRow(row{"dns_resolve_type": "SRV"}), "does not check SRV records"},
		{"CAA record", dnsRow(row{"dns_resolve_type": "CAA"}), "does not check CAA records"},
		{"PTR on an address", dnsRow(row{"dns_resolve_type": "PTR", "hostname": "93.184.215.14"}), "does not check PTR records"},
		{"an address as the name", dnsRow(row{"hostname": "93.184.215.14"}), "is not a domain name"},
		{"no name", dnsRow(row{"hostname": ""}), "is not a domain name"},
		{"contains", dnsRow(row{"conditions": cond("contains", "93.")}), `compares the record with "contains"`},
		{"not equals", dnsRow(row{"conditions": cond("not_equals", "1.2.3.4")}), `"not_equals"`},
		{"two expressions", dnsRow(row{"conditions": `[` +
			`{"type":"expression","variable":"record","operator":"equals","value":"93.184.215.14"},` +
			`{"type":"expression","andOr":"or","variable":"record","operator":"equals","value":"93.184.215.15"}]`}),
			"go beyond one"},
		{"a group", dnsRow(row{"conditions": `[{"type":"group","children":[` +
			`{"type":"expression","variable":"record","operator":"equals","value":"93.184.215.14"}]}]`}), "go beyond one"},
		{"another variable", dnsRow(row{"conditions": `[{"type":"expression","variable":"ttl","operator":"equals","value":"300"}]`}),
			`tests "ttl"`},
		{"unreadable conditions", dnsRow(row{"conditions": "{"}), "cannot be read"},
		{"value that is not an address", dnsRow(row{"conditions": cond("equals", "example.com")}), "is not an IPv4 address"},
		{"resolver that is a URL", dnsRow(row{"dns_resolve_server": "https://dns.example/dns-query"}), "is not an IP address or a host name"},
		{"resolver port out of range", dnsRow(row{"port": int64(70000)}), "is not a port"},
		{"A equals with a resolver that cannot come over", dnsRow(row{"dns_resolve_server": "https://dns.example/dns-query",
			"conditions": cond("equals", "93.184.215.14")}), "is not an IP address or a host name"},
		{"MX equals with a port out of range", dnsRow(row{"dns_resolve_type": "MX", "port": int64(70000),
			"conditions": cond("equals", "mx1.example.com")}), "is not a port"},
		{"no resolver", dnsRow(row{"dns_resolve_server": ""}), "Kuma had no resolver set"},
		{"no resolver column value", dnsRow(row{"dns_resolve_server": nil}), "Kuma had no resolver set"},
		{"only separators as resolver", dnsRow(row{"dns_resolve_server": " , "}), "Kuma had no resolver set"},
		{"TXT value with a leading space", dnsRow(row{"dns_resolve_type": "TXT", "conditions": cond("equals", " v=spf1 -all")}),
			"starts or ends with spaces"},
		{"TXT value with a trailing space", dnsRow(row{"dns_resolve_type": "TXT", "conditions": cond("equals", "v=spf1 -all ")}),
			"starts or ends with spaces"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			if m, ok := convertMonitor(tc.row, &res); ok {
				t.Fatalf("converted: %+v", m)
			}
			if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, tc.reason) {
				t.Errorf("skipped = %v, want a reason containing %q", res.Skipped, tc.reason)
			}
			// A skipped monitor is not also reported as imported with a change.
			if len(res.Changed) != 0 {
				t.Errorf("changed = %v, want nothing for a skipped monitor", res.Changed)
			}
		})
	}
}

// The fixtures' DNS monitor, written by Kuma itself, comes over from both
// schemas.
func TestFixtureDNSMonitor(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			res := convertFixture(t, fixture)
			m, ok := monitorByName(res, "DNS example.com")
			if !ok {
				t.Fatalf("DNS example.com missing; skipped = %v", res.Skipped)
			}
			want := configfile.DNSCheck{RecordType: "A", Expected: []string{}, Resolver: "1.1.1.1"}
			if m.Type != "dns" || m.Target != "example.com" || m.DNS == nil ||
				m.DNS.RecordType != want.RecordType || m.DNS.Resolver != want.Resolver || len(m.DNS.Expected) != 0 {
				t.Errorf("DNS example.com = %+v (dns %+v)", m, m.DNS)
			}
		})
	}
}
