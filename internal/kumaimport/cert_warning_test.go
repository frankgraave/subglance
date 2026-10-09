package kumaimport

import (
	"slices"
	"strings"
	"testing"
)

func certSetting(value string) []row {
	return []row{{"key": "tlsExpiryNotifyDays", "value": value, "type": "general"}}
}

// Kuma reads its list of warning days as JSON and falls back to [7, 14, 21]
// for anything that is not a list; a day is compared with the whole days
// left, so only whole days of one or more can ever be reached.
func TestCertWarnDays(t *testing.T) {
	cases := []struct {
		name     string
		settings []row
		want     []int
	}{
		{"never set", nil, []int{7, 14, 21}},
		{"other settings only", []row{{"key": "serverTimezone", "value": `"UTC"`}}, []int{7, 14, 21}},
		{"edited list", certSetting("[3, 30]"), []int{3, 30}},
		{"emptied list", certSetting("[]"), []int{}},
		{"not JSON", certSetting("7,14"), []int{7, 14, 21}},
		{"not a list", certSetting(`{"days":30}`), []int{7, 14, 21}},
		{"null", certSetting("null"), []int{7, 14, 21}},
		{"digits as text", certSetting(`["45", " 10 "]`), []int{45, 10}},
		{"fractions round down", certSetting("[30.9]"), []int{30}},
		{"days that are never reached", certSetting(`[0, -5, 0.5, "x", true, null]`), []int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := certWarnDays(tc.settings); !slices.Equal(got, tc.want) {
				t.Errorf("days = %v, want %v", got, tc.want)
			}
		})
	}
}

// A certificate is reported as early as Kuma warned about it: Kuma's first
// warning came at the largest listed day, with that many days or fewer
// left, and SubGlance warns when fewer than ssl_warn_days are left.
func TestCertWarningComesOver(t *testing.T) {
	on := row{"expiry_notification": int64(1)}
	with := func(over ...row) row {
		r := httpRow(nil)
		for _, o := range over {
			for k, v := range o {
				r[k] = v
			}
		}
		return r
	}
	port := func(security string) row {
		return row{"id": int64(2), "name": "Mail", "type": "port", "hostname": "mail.example.com", "port": int64(465),
			"active": int64(1), "interval": int64(60), "expiry_notification": int64(1), "smtp_security": security}
	}
	cases := []struct {
		name        string
		row         row
		settings    []row
		warn        int // 0: left to SubGlance's default
		note, never string
	}{
		{"Kuma's default list", with(on), nil, 22, "", "certificate"},
		{"the largest listed day leads", with(on), certSetting("[30, 7]"), 31, "", "certificate"},
		{"one day", with(on), certSetting("[1]"), 2, "", ""},
		{"keyword monitor", with(on, row{"type": "keyword", "keyword": "ok"}), certSetting("[10]"), 11, "", ""},
		{"json query monitor", with(on, row{"type": "json-query", "json_path": "ok", "expected_value": "true"}),
			certSetting("[10]"), 11, "", ""},
		{"warning off", with(row{"expiry_notification": int64(0)}), certSetting("[30]"), 0, "", "Kuma warned"},
		{"warning column missing", with(), certSetting("[30]"), 0, "", "Kuma warned"},
		{"certificate errors ignored", with(on, row{"ignore_tls": int64(1)}), certSetting("[30]"), 0, "", "Kuma warned"},
		{"an empty list never warned", with(on), certSetting("[]"), 0, "", "Kuma warned"},
		{"364 days is the most that fits", with(on), certSetting("[364]"), 365, "", "at most"},
		{"more than a year is clamped", with(on), certSetting("[400]"), 365,
			"Kuma warned 400 days before the certificate expired; SubGlance warns at most 365 days ahead", ""},
		{"implicit TLS port", port("secure"), nil, 0, "add an ssl monitor on mail.example.com:465", ""},
		{"STARTTLS port", port("starttls"), nil, 0, "SubGlance has no STARTTLS check", ""},
		{"plain port", port("nostarttls"), nil, 0, "", "certificate"},
		{"port without SMTP security", port(""), nil, 0, "", "certificate"},
		{"TLS port with the warning off", func() row { r := port("secure"); r["expiry_notification"] = int64(0); return r }(),
			nil, 0, "", "certificate"},
		{"TLS port with an empty list", port("secure"), certSetting("[]"), 0, "", "certificate"},
		{"ping is never read for a certificate", row{"id": int64(3), "name": "Router", "type": "ping", "hostname": "router.example.net",
			"active": int64(1), "interval": int64(60), "expiry_notification": int64(1)}, nil, 0, "", "certificate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{monitors: []row{tc.row}, settings: tc.settings})
			if len(res.Document.Monitors) != 1 {
				t.Fatalf("not converted: %s", notesOf(res))
			}
			if got := deref(res.Document.Monitors[0].SSLWarnDays); got != tc.warn {
				t.Errorf("ssl_warn_days = %d, want %d", got, tc.warn)
			}
			notes := notesOf(res)
			if tc.note != "" && !strings.Contains(notes, tc.note) {
				t.Errorf("notes = %q, want one containing %q", notes, tc.note)
			}
			if tc.never != "" && strings.Contains(notes, tc.never) {
				t.Errorf("notes = %q, want none containing %q", notes, tc.never)
			}
		})
	}
}
