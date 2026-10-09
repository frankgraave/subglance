package kumaimport

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
)

// maxSSLWarnDays is the most days ahead the API lets ssl_warn_days warn.
const maxSSLWarnDays = 365

// kumaCertDays is the list Kuma warns at until someone edits it under
// Settings, Notifications, TLS Certificate Expiry. Kuma writes it back when
// the setting is missing or is not a list (server/model/monitor.js in 1.23,
// server/util-server.js in 2.5).
var kumaCertDays = []int{7, 14, 21}

// certWarnDays reads the days before a certificate expires at which Kuma
// warned, one warning per day listed. An empty list means Kuma never warned.
//
// Kuma reads the setting as JSON and takes anything that is not a list, an
// unreadable value included, as its default. It compares each entry with the
// whole days left, so a string of digits counts as that number and a fraction
// as the day below it. A day of 0 or less is never reached: Kuma skips a
// certificate with no whole day left.
func certWarnDays(settings []row) []int {
	for _, s := range settings {
		if s.str("key") != "tlsExpiryNotifyDays" {
			continue
		}
		var raw any
		if err := json.Unmarshal([]byte(s.str("value")), &raw); err != nil {
			return kumaCertDays
		}
		list, ok := raw.([]any)
		if !ok {
			return kumaCertDays
		}
		days := []int{}
		for _, v := range list {
			var d float64
			switch v := v.(type) {
			case float64:
				d = v
			case string:
				n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil {
					continue
				}
				d = n
			default:
				continue
			}
			if d = math.Floor(d); d >= 1 && d <= math.MaxInt32 {
				days = append(days, int(d))
			}
		}
		return days
	}
	return kumaCertDays
}

// convertCertWarning carries Kuma's certificate expiry warning over, so a
// certificate running out is reported as early as it was there.
//
// Kuma warns per monitor when Certificate Expiry Notification is on and
// Ignore TLS/SSL errors is off, once for each listed day N, when the
// certificate has N or fewer days left (rounded in 1.23, whole days in 2.5);
// the first warning comes at the largest N. SubGlance's ssl_warn_days marks a check expiring when fewer than
// N days are left, so Kuma's N is N+1 here. A monitor Kuma never warned about
// keeps SubGlance's default: ssl_warn_days cannot say never.
//
// Kuma 2 also reads the certificate of a port monitor whose SMTP Security is
// secure or starttls. A tcp monitor reads none, so that warning is listed
// rather than lost without a word.
func convertCertWarning(m row, out *configfile.Monitor, typ string, days []int, note func(string)) {
	if !m.bool("expiry_notification") || m.bool("ignore_tls") || len(days) == 0 {
		return
	}
	switch typ {
	case "http", "keyword", "json-query":
	case "port":
		switch m.str("smtp_security") {
		case "secure":
			note("Kuma warned before the certificate on this port expired; a tcp monitor reads no certificate, " +
				"so add an ssl monitor on " + out.Target + " to keep that warning")
		case "starttls":
			note("Kuma warned before the certificate this port offers after STARTTLS expired; " +
				"SubGlance has no STARTTLS check, so that warning is not carried over")
		}
		return
	default:
		return
	}
	warn := slices.Max(days) + 1
	if warn > maxSSLWarnDays {
		note(fmt.Sprintf("Kuma warned %d days before the certificate expired; SubGlance warns at most %d days ahead",
			warn-1, maxSSLWarnDays))
		warn = maxSSLWarnDays
	}
	out.SSLWarnDays = ptr(warn)
}
