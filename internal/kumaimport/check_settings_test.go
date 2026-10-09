package kumaimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portRow(over row) row {
	r := row{"id": int64(1), "name": "Gateway", "type": "port", "hostname": "gw.example.com", "port": int64(443),
		"active": int64(1), "interval": int64(60)}
	for k, v := range over {
		r[k] = v
	}
	return r
}

func proxyRow(id int64, active bool) row {
	on := int64(0)
	if active {
		on = 1
	}
	return row{"id": id, "protocol": "socks5", "host": "proxy.lan", "port": int64(1080), "auth": int64(1),
		"username": "proxy-user", "password": "proxy-secret", "active": on}
}

// A setting that changed how Kuma ran a check, and that SubGlance has no
// field for, is listed on the monitor with what it means for the check; the
// same setting left at Kuma's default, or on a type Kuma ignores it for, is
// not.
func TestSettingsThatChangedTheCheckAreListed(t *testing.T) {
	proxies := []row{proxyRow(1, true), proxyRow(2, false)}
	cases := []struct {
		name string
		mon  row
		note string // "" when no note about the check is expected
	}{
		{"an active proxy", httpRow(row{"proxy_id": int64(1)}),
			`Kuma sent this check through the proxy "socks5://proxy.lan:1080"; SubGlance connects directly`},
		{"an active proxy on a keyword monitor", httpRow(row{"type": "keyword", "keyword": "ok", "proxy_id": int64(1)}),
			`through the proxy "socks5://proxy.lan:1080"`},
		{"an inactive proxy", httpRow(row{"proxy_id": int64(2)}), ""},
		{"a proxy that no longer exists", httpRow(row{"proxy_id": int64(9)}), ""},
		{"no proxy", httpRow(row{"proxy_id": nil}), ""},
		{"IPv4 only", httpRow(row{"ip_family": "ipv4"}),
			"Kuma checked over IPv4 only; SubGlance connects over IPv4 or IPv6, whichever answers, so a failure on IPv4 alone does not fail the check"},
		{"IPv6 only", httpRow(row{"ip_family": "ipv6"}), "so a failure on IPv6 alone does not fail the check"},
		{"either IP family", httpRow(row{"ip_family": nil}), ""},
		{"the cache buster", httpRow(row{"cache_bust": int64(1)}),
			"Kuma added a random uptime_kuma_cachebuster query parameter so no cache answered; SubGlance requests the URL as written"},
		{"no cache buster", httpRow(row{"cache_bust": int64(0)}), ""},
		{"an expected TLS alert", portRow(row{"expected_tls_alert": "certificate_required"}),
			"Kuma passed only when the TLS handshake ended with the alert certificate_required"},
		{"no expected TLS alert", portRow(row{"expected_tls_alert": "none"}), ""},
		{"no TLS alert column (Kuma 1.23)", portRow(nil), ""},
		{"a proxy on a port monitor, which Kuma does not use", portRow(row{"proxy_id": int64(1), "ip_family": "ipv6"}), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: []row{tc.mon}, proxies: proxies})
			if len(res.Document.Monitors) != 1 {
				t.Fatalf("monitor not converted: %s", notesOf(res))
			}
			notes := notesOf(res)
			if tc.note == "" {
				for _, marker := range []string{"proxy", "IPv", "cachebuster", "handshake"} {
					if strings.Contains(notes, marker) {
						t.Errorf("unexpected note about %s:\n%s", marker, notes)
					}
				}
				return
			}
			if !strings.Contains(notes, tc.note) {
				t.Errorf("report lacks %q:\n%s", tc.note, notes)
			}
			if strings.Contains(notes, "proxy-user") || strings.Contains(notes, "proxy-secret") {
				t.Errorf("report holds a proxy credential:\n%s", notes)
			}
		})
	}
}

// A monitor in a paused Kuma group is shown as paused there but still
// checked; it comes over enabled, with a note naming the paused group.
func TestMonitorsInAPausedGroupAreListed(t *testing.T) {
	group := func(id, parent int64, active bool) row {
		on := int64(0)
		if active {
			on = 1
		}
		return row{"id": id, "name": "Group " + string(rune('A'+id-10)), "type": "group", "active": on, "parent": parent}
	}
	child := func(parent int64, active bool) row {
		r := httpRow(row{"id": int64(1), "parent": parent})
		if !active {
			r["active"] = int64(0)
		}
		return r
	}
	cases := []struct {
		name     string
		monitors []row
		note     string
	}{
		{"its group is paused", []row{group(10, 0, false), child(10, true)},
			`monitor "Site" (http): Kuma shows it as paused because its group "Group A" is paused, but pausing a group does not stop the monitors in it`},
		{"a group above its group is paused", []row{group(10, 0, false), group(11, 10, true), child(11, true)},
			`its group "Group A" is paused`},
		{"its group is active", []row{group(10, 0, true), child(10, true)}, ""},
		{"it is paused itself", []row{group(10, 0, false), child(10, false)}, ""},
		{"no group", []row{child(0, true)}, ""},
		{"a group that no longer exists", []row{child(42, true)}, ""},
		{"groups that are each other's parent", []row{group(10, 11, true), group(11, 10, true), child(10, true)}, ""},
		{"a cycle above a paused group", []row{group(10, 11, true), group(11, 12, false), group(12, 10, true), child(10, true)},
			`its group "Group B" is paused`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: tc.monitors})
			var site *bool
			for _, m := range res.Document.Monitors {
				if m.Name == "Site" {
					site = m.Enabled
				}
			}
			if site == nil {
				t.Fatalf("monitor not converted: %s", notesOf(res))
			}
			notes := notesOf(res)
			if tc.note == "" {
				if strings.Contains(notes, "paused") {
					t.Errorf("unexpected note:\n%s", notes)
				}
				return
			}
			if !strings.Contains(notes, tc.note) {
				t.Errorf("report lacks %q:\n%s", tc.note, notes)
			}
			if !*site {
				t.Error("the monitor Kuma kept checking is imported paused")
			}
		})
	}
}

// Against both real databases: a proxy, the Kuma 2 settings and a paused
// group show up in the rendered file, and the proxy's credentials do not.
func TestCheckSettingsInTheRenderedFile(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			orig, err := os.ReadFile(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "kuma.db")
			if err := os.WriteFile(path, orig, 0o600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			stmts := []string{
				`INSERT INTO proxy (id, user_id, protocol, host, port, auth, username, password, active, "default")
					VALUES (1, 1, 'http', 'proxy.lan', 3128, 1, 'kuma-user', 'kuma-secret', 1, 0)`,
				`UPDATE monitor SET proxy_id = 1 WHERE name = 'Intranet'`,
				`UPDATE monitor SET active = 0 WHERE name = 'Edge'`,
			}
			want := []string{
				`#   - monitor "Intranet" (http): Kuma sent this check through the proxy "http://proxy.lan:3128"`,
				`#   - monitor "CDN" (http): Kuma shows it as paused because its group "Edge" is paused`,
			}
			if fixture == "kuma-2.5.5.db" {
				stmts = append(stmts,
					`UPDATE monitor SET ip_family = 'ipv6', cache_bust = 1 WHERE name = 'Shop (prod)'`,
					`UPDATE monitor SET expected_tls_alert = 'certificate_required' WHERE name = 'Postgres'`)
				want = append(want,
					`#   - monitor "Shop (prod)" (http): Kuma checked over IPv6 only`,
					`#   - monitor "Shop (prod)" (http): Kuma added a random uptime_kuma_cachebuster query parameter`,
					`#   - monitor "Postgres" (port): Kuma passed only when the TLS handshake ended with the alert certificate_required`)
			}
			for _, q := range stmts {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			_ = db.Close()

			res, err := Convert(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			out, err := Render(res)
			if err != nil {
				t.Fatal(err)
			}
			file := string(out)
			for _, w := range want {
				if !strings.Contains(file, w) {
					t.Errorf("the file has no line\n%s\nreport:\n%s", w, notesOf(res))
				}
			}
			if strings.Contains(file, "kuma-user") || strings.Contains(file, "kuma-secret") {
				t.Error("the file holds a proxy credential")
			}
			for _, m := range res.Document.Monitors {
				if m.Name == "CDN" && (m.Enabled == nil || !*m.Enabled) {
					t.Error("CDN, which Kuma kept checking, is imported paused")
				}
			}
		})
	}
}
