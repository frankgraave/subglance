package kumaimport

import (
	"net"
	"strconv"
	"strings"
)

// noteCheckSettings lists the settings of a converted monitor that changed
// how Kuma ran its check and that SubGlance has no field for.
//
// Each of them makes the imported monitor check something other than what
// Kuma checked, and the difference shows only as a missed or a false alert
// once Kuma is switched off, so each is a line in the report rather than a
// column nobody reads. Kuma reads the proxy, the IP family and the cache
// buster only on its HTTP types (server/model/monitor.js in 1.23 and 2.5),
// and the expected TLS alert only on a port monitor
// (server/monitor-types/tcp.js in 2.5); the other two do not exist in 1.23,
// where their columns are absent and read as empty.
//
// proxies holds Kuma's proxy rows by id. A proxy's credentials are never
// written: the note names its protocol, host and port only.
func noteCheckSettings(m row, typ string, proxies map[int64]row, note func(string)) {
	switch typ {
	case "http", "keyword", "json-query":
		// Kuma sends through a proxy only while the proxy is active, and
		// clears proxy_id when one is deleted.
		if p, ok := proxies[int64(m.int("proxy_id"))]; ok && p.bool("active") {
			note("Kuma sent this check through the proxy " + proxyAddress(p) + "; SubGlance connects directly, " +
				"so a site reachable only through the proxy fails, and one that answers differently on a direct path is judged on that")
		}
		switch fam := m.str("ip_family"); fam {
		case "ipv4", "ipv6":
			v := map[string]string{"ipv4": "IPv4", "ipv6": "IPv6"}[fam]
			note("Kuma checked over " + v + " only; SubGlance connects over IPv4 or IPv6, whichever answers, " +
				"so a failure on " + v + " alone does not fail the check")
		}
		if m.bool("cache_bust") {
			note("Kuma added a random uptime_kuma_cachebuster query parameter so no cache answered; SubGlance requests the URL as written, " +
				"so a cache in front of the site can keep answering while the server behind it is down")
		}
	case "port":
		switch alert := strings.TrimSpace(m.str("expected_tls_alert")); alert {
		case "", "none", "null":
		default:
			note("Kuma passed only when the TLS handshake ended with the alert " + alert + ", which checks that the server refuses " +
				"a client without a certificate; a tcp monitor only opens the connection, so it passes whether or not the server still refuses")
		}
	}
}

// proxyAddress writes a Kuma proxy as protocol://host:port, without its
// username or password.
func proxyAddress(p row) string {
	addr := strings.TrimSpace(p.str("host"))
	if port := p.int("port"); port > 0 {
		addr = net.JoinHostPort(addr, strconv.Itoa(port))
	}
	if proto := strings.TrimSpace(p.str("protocol")); proto != "" {
		addr = proto + "://" + addr
	}
	return strconv.Quote(oneLine(addr))
}

// notePausedGroup lists an enabled monitor inside a paused Kuma group.
//
// Kuma shows such a monitor as paused, and disables its Resume button,
// because it is active only while every group above it is (isParentActive in
// server/model/monitor.js). Its server does not act on that: pausing a group
// stops the group's own check only, startMonitors starts every monitor whose
// own flag is set, and an edited monitor is restarted only when active, while
// the instance already running keeps going (Kuma 1.23 and 2.5; upstream issue
// louislam/uptime-kuma#7242). So Kuma kept checking it and alerting on it.
// It is imported enabled, as Kuma ran it, rather than silently stopping a
// check that was running; the note says so, because the user saw it paused.
//
// byID holds every Kuma monitor row, groups included. The walk stops at a
// parent already visited, so a cycle in the parent column cannot hang it.
func notePausedGroup(m row, byID map[int64]row, note func(string)) {
	if !m.bool("active") {
		return
	}
	seen := map[int64]bool{int64(m.int("id")): true}
	for id := int64(m.int("parent")); id != 0 && !seen[id]; {
		seen[id] = true
		g, ok := byID[id]
		if !ok {
			return
		}
		if !g.bool("active") {
			note("Kuma shows it as paused because its group " + strconv.Quote(oneLine(strings.TrimSpace(g.str("name")))) +
				" is paused, but pausing a group does not stop the monitors in it, so Kuma kept checking this one and alerting; " +
				"it is imported enabled and alerts here too, so pause it if it should not")
			return
		}
		id = int64(g.int("parent"))
	}
}
