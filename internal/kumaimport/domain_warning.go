package kumaimport

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/publicsuffix"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
)

// maxDomainWarnDays is the most days ahead the API lets domain_warn_days
// warn.
const maxDomainWarnDays = 365

// kumaDomainDays is the list Kuma 2 warns at until someone edits it under
// Settings, Notifications, Domain Name Expiry. Kuma writes it back when the
// setting is missing or is not a list (server/model/domain_expiry.js in 2.5).
var kumaDomainDays = []int{7, 14, 21}

// domainTargetField names, per Kuma type, the column Kuma reads a monitor's
// domain from (TYPES_WITH_DOMAIN_EXPIRY_SUPPORT_VIA_FIELD in src/util.ts in
// 2.5, as the migration that added the feature spells the columns). A type
// that is not listed never had the warning.
var domainTargetField = map[string]string{
	"http": "url", "keyword": "url", "json-query": "url", "real-browser": "url", "websocket-upgrade": "url",
	"grpc-keyword": "grpc_url",
	"port":         "hostname", "ping": "hostname", "dns": "hostname", "smtp": "hostname", "snmp": "hostname",
	"gamedig": "hostname", "steam": "hostname", "mqtt": "hostname", "radius": "hostname",
	"tailscale-ping": "hostname", "sip-options": "hostname",
}

// domainWarning is one domain Kuma warned about, and what asked for it.
type domainWarning struct {
	domain   string
	monitors []string        // the Kuma monitors that asked, in Kuma's order
	channels map[string]bool // their channels that came over
	active   bool            // one of them was running
}

// convertDomainWarnings carries Kuma 2's domain expiry warning over as domain
// monitors, so a registration running out is reported as early as it was
// there.
//
// Kuma has the warning per monitor (Domain Name Expiry Notification, on by
// default since the migration that added it), but keeps its state per
// domain: every running monitor with it on looks up the registrable domain
// of its target over RDAP, compares the days left with the days listed under
// Settings, Notifications, and sends a notice through its own notifications
// unless one for that day went out already. So a notice went out once per
// domain, through whichever of its monitors looked first. SubGlance has a
// check of its own for this, so each domain becomes one domain monitor, with
// the channels of every Kuma monitor that asked for it: any of them could
// have been the one a notice went through. Kuma ran nothing for a paused
// monitor, so a domain only paused monitors asked for comes over switched
// off.
//
// Kuma warns when the whole days left are at most a listed day N; a domain
// monitor warns when fewer than domain_warn_days are left, so Kuma's N is
// N+1 here, as for certificates. With no day listed Kuma sent nothing, and no
// monitor is added. Kuma 1.23 has no such warning and its monitors no such
// column, which reads as off.
//
// Kuma looks up only names under a suffix in the ICANN section of the Public
// Suffix List (tldts with private domains ignored): an IP address, a local
// name such as router.lan and a TLD nobody delegated were never looked up,
// and add nothing. A name under a shared suffix in the private section, such
// as a GitHub Pages site, Kuma looked up as the shared suffix's own domain,
// which a domain monitor does not watch, and an internationalised name a
// domain monitor takes only in its xn-- form; the warning of such a monitor
// is listed as not imported instead.
//
// links holds the channel keys of each Kuma monitor, by id; taken holds the
// monitor keys already used. It returns the monitors to add.
func convertDomainWarnings(src source, links map[int64][]string, taken map[string]bool, res *Result) []configfile.Monitor {
	days := notifyDays(src.settings, "domainExpiryNotifyDays", kumaDomainDays)
	if len(days) == 0 {
		return nil
	}
	var order []string
	byDomain := map[string]*domainWarning{}
	for _, m := range src.monitors {
		field, ok := domainTargetField[m.str("type")]
		if !ok || !m.bool("domain_expiry_notification") {
			continue
		}
		name := strings.TrimSpace(m.str("name"))
		if name == "" {
			name = "Kuma monitor " + m.str("id")
		}
		domain, lost := kumaDomain(m.str(field))
		if lost != "" {
			// Listed as a warning that did not come over, whether or not
			// the monitor did: one of a type SubGlance has no check for
			// had the warning too.
			res.Skipped = append(res.Skipped, Note{Kind: "domain warning", Name: name, Type: m.str("type"), Reason: lost})
			continue
		}
		if domain == "" {
			continue
		}
		w := byDomain[domain]
		if w == nil {
			w = &domainWarning{domain: domain, channels: map[string]bool{}}
			byDomain[domain] = w
			order = append(order, domain)
		}
		w.monitors = append(w.monitors, strconv.Quote(oneLine(name)))
		for _, key := range links[int64(m.int("id"))] {
			w.channels[key] = true
		}
		w.active = w.active || m.bool("active")
	}

	warn := slices.Max(days) + 1
	capped := warn > maxDomainWarnDays
	if capped {
		warn = maxDomainWarnDays
	}
	var out []configfile.Monitor
	for _, domain := range order {
		w := byDomain[domain]
		mon := configfile.Monitor{
			Name:           domain + " registration",
			Type:           "domain",
			Target:         domain,
			Enabled:        ptr(w.active),
			IntervalS:      ptr(int(checker.DefaultDomainInterval.Seconds())),
			DomainWarnDays: ptr(warn),
			Tags:           map[string]string{},
			Channels:       []string{},
		}
		for key := range w.channels {
			mon.Channels = append(mon.Channels, key)
		}
		sort.Strings(mon.Channels)
		mon.Key = configfile.DeriveKey(mon.Name, "monitor", func(k string) bool { return taken[k] })
		taken[mon.Key] = true

		note := func(reason string) { changed(res, "monitor", mon.Name, "", reason) }
		note(fmt.Sprintf("added to keep Kuma's domain expiry warning for %s: Kuma warned with %d days or fewer left, "+
			"this domain monitor warns with fewer than %d", andList(w.monitors), warn-1, warn))
		if capped {
			note(fmt.Sprintf("Kuma warned %d days before the registration expired; SubGlance warns at most %d days ahead",
				slices.Max(days), maxDomainWarnDays))
		}
		if !w.active {
			note("switched off: every Kuma monitor that asked for this warning was paused, so Kuma did not look the domain up")
		}
		if len(mon.Channels) == 0 {
			note("none of the Kuma monitors that asked for this warning has a channel that came over, so until it gets one, " +
				"it alerts only through a tag routing rule or the default channel, and tells nobody without either")
		}
		out = append(out, mon)
	}
	return out
}

// kumaDomain returns the registrable domain Kuma looked up for a target, or
// "" when Kuma looked nothing up. When Kuma looked up a name a domain monitor
// cannot watch, it returns "" and, as lost, the reason the warning does not
// come over.
func kumaDomain(target string) (domain, lost string) {
	host := strings.TrimSpace(target)
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil {
			return "", ""
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return "", ""
	}
	// Kuma looks an internationalised name up as it is written; a domain
	// monitor takes the xn-- form the registry holds. Converting it here
	// would add a dependency for a rare case, so it is listed instead.
	if strings.ContainsFunc(host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return "", "Kuma warned before the registration of " + host + " expired; a domain monitor takes the name in the xn-- " +
			"form its registry holds, so add one by hand to keep that warning"
	}
	if _, icann := publicsuffix.PublicSuffix(host); !icann {
		// Kuma ignored the private section, so the suffix it saw is the
		// longest ICANN suffix of the name, and the domain the label in
		// front of it: github.io for user.github.io, myspreadshop.co.uk
		// for shop.myspreadshop.co.uk. A name with no ICANN suffix at all, a local
		// one or an undelegated TLD, it did not look up.
		labels := strings.Split(host, ".")
		for i := 1; i < len(labels); i++ {
			suffix := strings.Join(labels[i:], ".")
			if ps, ok := publicsuffix.PublicSuffix(suffix); ok && ps == suffix {
				return "", "Kuma warned before the registration of " + labels[i-1] + "." + suffix + " expired, the domain " +
					"of the shared suffix this target is under; a domain monitor watches a domain someone registered, " +
					"not a shared suffix, so that warning is not carried over"
			}
		}
		return "", ""
	}
	reg, err := checker.RegisteredDomain(host)
	if err != nil {
		return "", ""
	}
	return reg, ""
}
