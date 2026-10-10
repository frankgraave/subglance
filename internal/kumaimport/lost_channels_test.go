package kumaimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func notificationRow(id int64, name, config string) row {
	return row{"id": id, "name": name, "active": int64(1), "config": config}
}

func linkRow(id, monitor, notification int64) row {
	return row{"id": id, "monitor_id": monitor, "notification_id": notification}
}

// A monitor that Kuma alerted through a notification SubGlance has no
// channel for is listed with that notification, and, when none of its
// notifications came over, with what an empty channel list means: alerts go
// to a routing rule or the default channel, and to nobody without either.
func TestMonitorsNameTheNotificationsThatDidNotComeOver(t *testing.T) {
	notifications := []row{
		notificationRow(1, "Ops Discord", `{"type":"discord","discordWebhookUrl":"https://discord.example/x"}`),
		notificationRow(2, "On-call", `{"type":"PagerDuty","pagerdutyIntegrationKey":"x"}`),
		notificationRow(3, "Phones", `{"type":"line","lineChannelAccessToken":"x"}`),
		notificationRow(4, "", `{"type":"PushDeer"}`),
		notificationRow(5, "Night\nshift", `{"type":"opsgenie"}`),
	}
	const nobody = "tells nobody without either"
	cases := []struct {
		name     string
		links    []int64 // notification ids, in link order
		channels []string
		note     string // "" when the monitor gets no note about channels
	}{
		{"every notification skipped", []int64{2}, []string{},
			`none of the notifications Kuma alerted through came over ("On-call"), so it has no channels`},
		{"several skipped, named in link order", []int64{3, 2}, []string{},
			`came over ("Phones" and "On-call")`},
		{"some skipped", []int64{1, 2, 3}, []string{"ops-discord"},
			`Kuma also alerted through "On-call" and "Phones", which did not come over; the channels that did stay attached, and alert while they are enabled`},
		{"the same skipped notification linked twice", []int64{1, 2, 2}, []string{"ops-discord"},
			`Kuma also alerted through "On-call", which did not come over`},
		{"a skipped notification without a name", []int64{4}, []string{}, `came over ("Kuma PushDeer")`},
		{"a name with a line break", []int64{5}, []string{}, `came over ("Night shift")`},
		{"every notification came over", []int64{1}, []string{"ops-discord"}, ""},
		{"no notifications in Kuma", nil, []string{}, ""},
		{"a link to a notification that no longer exists", []int64{9}, []string{}, ""},
		{"a deleted notification beside a skipped one", []int64{9, 2}, []string{}, `came over ("On-call"), so`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var links []row
			for i, nid := range tc.links {
				links = append(links, linkRow(int64(i+1), 1, nid))
			}
			res := convert(source{monitors: []row{httpRow(nil)}, notifications: notifications, links: links})
			if len(res.Document.Monitors) != 1 {
				t.Fatalf("not converted: %s", notesOf(res))
			}
			if got := res.Document.Monitors[0].Channels; !slices.Equal(got, tc.channels) {
				t.Errorf("channels = %v, want %v", got, tc.channels)
			}
			var notes []string
			for _, n := range res.Changed {
				if n.Kind == "monitor" && (strings.Contains(n.Reason, "came over") || strings.Contains(n.Reason, "come over")) {
					notes = append(notes, n.String())
				}
			}
			if tc.note == "" {
				if len(notes) > 0 {
					t.Errorf("monitor notes = %q, want none about channels", notes)
				}
				return
			}
			if len(notes) != 1 || !strings.Contains(notes[0], tc.note) {
				t.Fatalf("monitor notes = %q, want one containing %q", notes, tc.note)
			}
			if !strings.HasPrefix(notes[0], `monitor "Site" (http): `) {
				t.Errorf("note %q does not name the monitor", notes[0])
			}
			if lostAll := len(tc.channels) == 0; strings.Contains(notes[0], nobody) != lostAll {
				t.Errorf("note %q: says who is told when nothing came over = %v, want %v", notes[0], !lostAll, lostAll)
			}
		})
	}
}

// Each monitor is judged on its own links: one monitor losing its only
// notification says nothing about another whose notification came over.
func TestLostNotificationsArePerMonitor(t *testing.T) {
	second := httpRow(row{"id": int64(2), "name": "Other", "url": "https://other.example/"})
	res := convert(source{
		monitors: []row{httpRow(nil), second},
		notifications: []row{
			notificationRow(1, "Ops Discord", `{"type":"discord","discordWebhookUrl":"https://discord.example/x"}`),
			notificationRow(2, "On-call", `{"type":"PagerDuty"}`),
		},
		links: []row{linkRow(1, 1, 2), linkRow(2, 2, 1)},
	})
	got := notesOf(res)
	if !strings.Contains(got, `monitor "Site" (http): none of the notifications`) {
		t.Errorf("Site is not listed: %s", got)
	}
	if strings.Contains(got, `monitor "Other"`) {
		t.Errorf("Other is listed although its notification came over: %s", got)
	}
}

// Every notification type in the two fixtures has a channel, so neither
// gains a note from this check.
func TestFixturesLoseNoNotifications(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			for _, n := range convertFixture(t, fixture).Changed {
				if strings.Contains(n.Reason, "did not come over") || strings.Contains(n.Reason, "notifications Kuma alerted through") {
					t.Errorf("unexpected note: %s", n)
				}
			}
		})
	}
}

func TestAndList(t *testing.T) {
	cases := map[string][]string{
		"":           nil,
		"a":          {"a"},
		"a and b":    {"a", "b"},
		"a, b and c": {"a", "b", "c"},
	}
	for want, in := range cases {
		if got := andList(in); got != want {
			t.Errorf("andList(%q) = %q, want %q", in, got, want)
		}
	}
}

// On a database Kuma wrote, with a PagerDuty notification added to a monitor
// that alerted only through it and to one that also had Discord, the file a
// user reads lists both, each in the way that applies to it.
func TestLostNotificationsInTheRenderedFile(t *testing.T) {
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
			for _, q := range []string{
				`INSERT INTO notification (id, name, active, user_id, is_default, config)
					VALUES (90, 'On-call', 1, 1, 0, '{"type":"PagerDuty","pagerdutyIntegrationKey":"kuma-secret"}')`,
				// Intranet has no notifications in the fixture; Shop (prod)
				// has Discord and Slack.
				`INSERT INTO monitor_notification (monitor_id, notification_id)
					SELECT id, 90 FROM monitor WHERE name IN ('Intranet', 'Shop (prod)')`,
			} {
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
			for _, want := range []string{
				`#   - channel "On-call" (PagerDuty): SubGlance has no PagerDuty channel`,
				`#   - monitor "Intranet" (http): none of the notifications Kuma alerted through came over ("On-call"), so it has no channels`,
				`#   - monitor "Shop (prod)" (http): Kuma also alerted through "On-call", which did not come over; the channels that did stay attached, and alert while they are enabled`,
			} {
				if !strings.Contains(file, want) {
					t.Errorf("the file has no line\n%s\nreport:\n%s", want, notesOf(res))
				}
			}
			if strings.Contains(file, "kuma-secret") {
				t.Error("the file holds a Kuma credential")
			}
		})
	}
}
