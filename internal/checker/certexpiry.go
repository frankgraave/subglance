package checker

import (
	"crypto/x509"
	"fmt"
	"time"
)

// lifetimeShare is the part of a certificate's lifetime, counted back from
// its expiry, inside which a check warns at the most: a quarter.
//
// A threshold in days is an assumption about how long certificates last.
// Let's Encrypt issues certificates that last 160 hours, and with the default
// of 14 days one of those would be expiring from the moment it is issued: a
// notice that never goes away, and teaches its reader to ignore the day the
// renewal really fails. A quarter of 160 hours is 40 hours, after the point
// where an ACME client renews (Caddy does it with a third of the lifetime
// left, 53 hours here), so a certificate still unrenewed then has a renewal
// that failed. A 90-day certificate's quarter is 22.5 days, so 14 days is
// what it always was; a 45-day one warns at 11 days, after the usual renewal
// 15 days before expiry.
//
// The cap applies to a threshold the user set as well as to the default: a
// window longer than the certificate's whole life can only mean always
// warning, which says nothing.
const lifetimeShare = 4

// warnWindow is how long before leaf expires a monitor with warnDays warns
// about it: warnDays, or a quarter of the certificate's lifetime when that is
// shorter. capped reports the second case, so the notice can say why it came
// later than the days on the monitor.
//
// A certificate whose NotBefore is not before its NotAfter has no lifetime to
// take a share of, and keeps the monitor's threshold.
func warnWindow(leaf *x509.Certificate, warnDays int) (window time.Duration, capped bool) {
	window = time.Duration(warnDays) * 24 * time.Hour
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	if lifetime <= 0 {
		return window, false
	}
	if share := lifetime / lifetimeShare; share < window {
		return share, true
	}
	return window, false
}

// expiryNotice is the notice for a certificate that is still valid at now but
// expires inside its warning window, or "" when it does not. The window
// includes its first instant: with exactly 40 hours left, a 160-hour
// certificate is warned about, as the documentation says the warning starts
// then. The ssl check and
// an https check both write it, so a monitor of either type says the same
// sentence about the same certificate.
//
// "certificate expires in 6 days (on 2026-10-12)", and when the certificate's
// lifetime shortened the window: "certificate expires in 35 hours (on
// 2026-10-24); it lasts 6 days, so the warning starts 40 hours before
// expiry". Without the second half, a monitor set to warn at 14 days that
// first speaks with 40 hours left looks like a monitor that was late.
func expiryNotice(leaf *x509.Certificate, warnDays int, now time.Time) string {
	if warnDays <= 0 {
		return ""
	}
	remaining := leaf.NotAfter.Sub(now)
	window, capped := warnWindow(leaf, warnDays)
	if remaining <= 0 || remaining > window {
		return ""
	}
	notice := CertExpiryNotice(remaining, leaf.NotAfter)
	if capped {
		notice += fmt.Sprintf("; it lasts %s, so the warning starts %s before expiry",
			timeLeft(leaf.NotAfter.Sub(leaf.NotBefore)), timeLeft(window))
	}
	return notice
}

// CertExpiryNotice is the first half of that notice, "certificate expires in
// 6 days (on 2026-10-12)", for a certificate with remaining left before
// notAfter. It is exported so the demo seeder writes the checker's sentence
// rather than a copy that drifts.
func CertExpiryNotice(remaining time.Duration, notAfter time.Time) string {
	return fmt.Sprintf("certificate expires in %s (on %s)",
		timeLeft(remaining), notAfter.Format(time.DateOnly))
}

// timeLeft writes a span of time before an expiry in the unit a reader acts
// on: days from two days up, hours below that, minutes in the last hour.
// Days alone said "expires in 0 days" for a certificate with 23 hours left,
// and "1 days" for one with 47: of a six-day certificate, the whole of its
// warning window would read as one of those two.
func timeLeft(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "1 hour"
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return "1 minute"
	}
}
