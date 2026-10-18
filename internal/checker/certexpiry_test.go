package checker

import (
	"context"
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

// shortLived is the lifetime of a Let's Encrypt certificate from its
// shortlived profile: 160 hours, a little over six days.
const shortLived = 160 * time.Hour

// certAged describes a certificate that lasts lifetime and was issued age
// before now.
func certAged(now time.Time, lifetime, age time.Duration) certOpts {
	return certOpts{notBefore: now.Add(-age), notAfter: now.Add(lifetime - age)}
}

func TestWarnWindowIsAtMostAQuarterOfTheLifetime(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		lifetime   time.Duration
		warnDays   int
		want       time.Duration
		wantCapped bool
	}{
		{"a 90-day certificate keeps 14 days", 90 * 24 * time.Hour, 14, 14 * 24 * time.Hour, false},
		{"a 160-hour certificate warns at 40 hours", shortLived, 14, 40 * time.Hour, true},
		{"a set threshold is capped too", shortLived, 30, 40 * time.Hour, true},
		{"a 45-day certificate warns at 11 days and 6 hours", 45 * 24 * time.Hour, 14, 270 * time.Hour, true},
		{"a threshold below the quarter stays", 90 * 24 * time.Hour, 7, 7 * 24 * time.Hour, false},
		{"a certificate without a lifetime keeps the threshold", 0, 14, 14 * 24 * time.Hour, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			leaf := &x509.Certificate{NotBefore: now, NotAfter: now.Add(tc.lifetime)}
			got, capped := warnWindow(leaf, tc.warnDays)
			if got != tc.want || capped != tc.wantCapped {
				t.Errorf("warnWindow = %v, capped %v; want %v, capped %v", got, capped, tc.want, tc.wantCapped)
			}
		})
	}
}

func TestExpiryNotice(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	leaf := func(lifetime, left time.Duration) *x509.Certificate {
		return &x509.Certificate{NotBefore: now.Add(left - lifetime), NotAfter: now.Add(left)}
	}
	tests := []struct {
		name string
		leaf *x509.Certificate
		days int
		want string
	}{
		{"a 90-day certificate inside 14 days", leaf(90*24*time.Hour, 13*24*time.Hour+time.Hour), 14,
			"certificate expires in 13 days (on 2026-10-31)"},
		{"a 90-day certificate outside 14 days", leaf(90*24*time.Hour, 15*24*time.Hour), 14, ""},
		{"a short-lived certificate inside its window", leaf(shortLived, 35*time.Hour), 14,
			"certificate expires in 35 hours (on 2026-10-19); it lasts 6 days, so the warning starts 40 hours before expiry"},
		{"a short-lived certificate at the start of its window", leaf(shortLived, 40*time.Hour), 14,
			"certificate expires in 40 hours (on 2026-10-20); it lasts 6 days, so the warning starts 40 hours before expiry"},
		{"a 90-day certificate with exactly 14 days left", leaf(90*24*time.Hour, 14*24*time.Hour), 14,
			"certificate expires in 14 days (on 2026-11-01)"},
		{"a short-lived certificate just renewed", leaf(shortLived, shortLived-time.Hour), 14, ""},
		{"a short-lived certificate at its renewal point", leaf(shortLived, 80*time.Hour), 14, ""},
		{"one day left reads in hours", leaf(90*24*time.Hour, 23*time.Hour), 14,
			"certificate expires in 23 hours (on 2026-10-19)"},
		{"the last hour reads in minutes", leaf(90*24*time.Hour, 25*time.Minute), 14,
			"certificate expires in 25 minutes (on 2026-10-18)"},
		{"the warning off", leaf(shortLived, time.Hour), 0, ""},
		{"an expired certificate is not a notice", leaf(shortLived, -time.Hour), 14, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := expiryNotice(tc.leaf, tc.days, now); got != tc.want {
				t.Errorf("expiryNotice = %q\n                    want %q", got, tc.want)
			}
		})
	}
}

func TestTimeLeft(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "1 minute"},
		{90 * time.Second, "1 minute"},
		{2 * time.Minute, "2 minutes"},
		{59 * time.Minute, "59 minutes"},
		{time.Hour, "1 hour"},
		{119 * time.Minute, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{47 * time.Hour, "47 hours"},
		{48 * time.Hour, "2 days"},
		{shortLived, "6 days"},
		{13*24*time.Hour + 23*time.Hour, "13 days"},
	}
	for _, tc := range tests {
		if got := timeLeft(tc.d); got != tc.want {
			t.Errorf("timeLeft(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// The acceptance for the ssl check, against a real handshake: a six-day
// certificate is not expiring for most of its life, and is once it is a
// quarter of its life from the end without having been renewed.
func TestSSLCheckWarnsAboutAShortLivedCertificateOnlyNearItsEnd(t *testing.T) {
	// A certificate's dates are whole seconds, so a clock on a whole second
	// keeps "35 hours left" from reading as 34 hours and 59 minutes.
	now := time.Now().Truncate(time.Second)
	check := func(t *testing.T, opts certOpts, warnDays int) Result {
		t.Helper()
		cert, pool := newTestCert(t, opts)
		return sslCheckerTrusting(pool, now).Check(context.Background(), Monitor{
			Type: TypeSSL, Target: startTLSServer(t, cert), Timeout: 5 * time.Second, SSLWarnDays: warnDays,
		})
	}

	for _, age := range []time.Duration{time.Hour, 80 * time.Hour} {
		res := check(t, certAged(now, shortLived, age), 14)
		if !res.OK || res.Expiring || res.Kind != FailNone || res.Error != "" {
			t.Errorf("160-hour certificate %v old: ok %v expiring %v kind %q error %q, want a plain pass",
				age, res.OK, res.Expiring, res.Kind, res.Error)
		}
	}

	res := check(t, certAged(now, shortLived, 125*time.Hour), 14)
	if !res.OK || !res.Expiring || res.Kind != FailCertExpiry {
		t.Fatalf("160-hour certificate with 35 hours left: ok %v expiring %v kind %q, want a pass marked expiring (%s)",
			res.OK, res.Expiring, res.Kind, res.Error)
	}
	if !strings.HasPrefix(res.Error, "certificate expires in 35 hours") ||
		!strings.HasSuffix(res.Error, "; it lasts 6 days, so the warning starts 40 hours before expiry") {
		t.Errorf("error = %q, want the hours left and the lifetime that shortened the window", res.Error)
	}

	// A threshold the user set is capped the same way.
	if res := check(t, certAged(now, shortLived, 100*time.Hour), 30); !res.OK || res.Expiring {
		t.Errorf("ssl_warn_days 30 with 60 hours of 160 left: expiring %v (%s), want a plain pass", res.Expiring, res.Error)
	}

	// A 90-day certificate is warned about as it always was.
	if res := check(t, certAged(now, 90*24*time.Hour, 77*24*time.Hour), 14); !res.Expiring ||
		!strings.HasPrefix(res.Error, "certificate expires in 13 days (on ") || strings.Contains(res.Error, "lasts") {
		t.Errorf("90-day certificate with 13 days left: expiring %v error %q, want the notice as before", res.Expiring, res.Error)
	}
	if res := check(t, certAged(now, 90*24*time.Hour, 75*24*time.Hour), 14); res.Expiring {
		t.Errorf("90-day certificate with 15 days left is expiring: %s", res.Error)
	}
}

// An https check reads the same rule and says the same sentence.
func TestHTTPCheckWarnsAboutAShortLivedCertificateOnlyNearItsEnd(t *testing.T) {
	check := func(t *testing.T, age time.Duration) Result {
		t.Helper()
		cert, pool := newTestCert(t, certAged(time.Now(), shortLived, age))
		m := monitor(httpsServer(t, &cert, nil).URL)
		m.SSLWarnDays = 14
		return httpCheckerTrusting(pool).Check(context.Background(), m)
	}

	if res := check(t, 80*time.Hour); !res.OK || res.Expiring || res.Kind != FailNone {
		t.Errorf("160-hour certificate 80 hours old: ok %v expiring %v kind %q (%s), want a plain pass",
			res.OK, res.Expiring, res.Kind, res.Error)
	}
	// The http checker reads the wall clock, so the half hour keeps the
	// hours left at 35 however long the handshake takes.
	res := check(t, 124*time.Hour+30*time.Minute)
	if !res.OK || !res.Expiring || res.Kind != FailCertExpiry {
		t.Fatalf("160-hour certificate with 35 hours left: ok %v expiring %v kind %q, want a pass marked expiring (%s)",
			res.OK, res.Expiring, res.Kind, res.Error)
	}
	if !strings.HasPrefix(res.Error, "certificate expires in 35 hours (on ") ||
		!strings.HasSuffix(res.Error, "; it lasts 6 days, so the warning starts 40 hours before expiry") {
		t.Errorf("error = %q, want the ssl check's sentence", res.Error)
	}
}
