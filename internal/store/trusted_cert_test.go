package store

import (
	"strings"
	"testing"
	"time"
)

const testPin = "3F:9A:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2"

func TestTrustedCertificateRoundTripsAndTravelsWithItsTime(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)

	for _, typ := range []string{"http", "ssl"} {
		created, err := db.CreateMonitor(ctx, Monitor{
			Name: "pve", Type: typ, Target: "https://pve.home.arpa:8006", Enabled: true,
			TrustedCertSHA256: testPin, TrustedCertAt: at,
		})
		if err != nil {
			t.Fatalf("%s: create: %v", typ, err)
		}
		got, err := db.GetMonitor(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TrustedCertSHA256 != testPin || !got.TrustedCertAt.Equal(at) {
			t.Errorf("%s: pin = %q at %v, want %q at %v", typ, got.TrustedCertSHA256, got.TrustedCertAt, testPin, at)
		}

		got.TrustedCertSHA256, got.TrustedCertAt = "", time.Time{}
		if _, err := db.UpdateMonitor(ctx, got); err != nil {
			t.Fatalf("%s: clear: %v", typ, err)
		}
		cleared, err := db.GetMonitor(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cleared.TrustedCertSHA256 != "" || !cleared.TrustedCertAt.IsZero() {
			t.Errorf("%s: cleared pin reads back as %q at %v", typ, cleared.TrustedCertSHA256, cleared.TrustedCertAt)
		}
	}
}

// A pin is written only where a check reads it. A tcp monitor handed one —
// by a change of type the API did not catch — stores NULL rather than a
// value nothing would ever look at, and a pin without a time is stamped.
func TestTrustedCertificateIsStoredOnlyWhereACheckReadsIt(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	tcp, err := db.CreateMonitor(ctx, Monitor{Name: "db", Type: "tcp", Target: "db.internal:5432", Enabled: true, TrustedCertSHA256: testPin})
	if err != nil {
		t.Fatalf("create tcp: %v", err)
	}
	if got, _ := db.GetMonitor(ctx, tcp.ID); got.TrustedCertSHA256 != "" || !got.TrustedCertAt.IsZero() {
		t.Errorf("tcp monitor stored pin %q at %v", got.TrustedCertSHA256, got.TrustedCertAt)
	}

	before := time.Now().Add(-time.Second)
	ssl, err := db.CreateMonitor(ctx, Monitor{Name: "nas", Type: "ssl", Target: "nas.home.arpa:5001", Enabled: true, TrustedCertSHA256: testPin})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetMonitor(ctx, ssl.ID); got.TrustedCertAt.Before(before.Truncate(time.Second)) {
		t.Errorf("pin without a time stored at %v, want now", got.TrustedCertAt)
	}
}

// The schema is the last line: a direct write cannot leave a pin on another
// type, in another spelling, or without its time.
func TestTrustedCertificateSchemaRefusesWhatGoNeverWrites(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	m, err := db.CreateMonitor(ctx, Monitor{Name: "x", Type: "ssl", Target: "x.test:443", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := db.CreateMonitor(ctx, Monitor{Name: "y", Type: "tcp", Target: "y.test:22", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		id  int64
		pin any
		at  any
	}{
		"other type":   {tcp.ID, testPin, int64(1)},
		"bare hex":     {m.ID, strings.ReplaceAll(testPin, ":", ""), int64(1)},
		"pin, no time": {m.ID, testPin, nil},
		"time, no pin": {m.ID, nil, int64(1)},
	} {
		if _, err := db.Writer.ExecContext(ctx,
			`UPDATE monitors SET trusted_cert_sha256 = ?, trusted_cert_at = ? WHERE id = ?`, tc.pin, tc.at, tc.id); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := db.Writer.ExecContext(ctx,
		`UPDATE monitors SET trusted_cert_sha256 = ?, trusted_cert_at = 1 WHERE id = ?`, testPin, m.ID); err != nil {
		t.Errorf("a well-formed pin on an ssl monitor: %v", err)
	}
}
