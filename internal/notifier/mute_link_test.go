package notifier

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/mutelink"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

func testMuteSigner(t *testing.T) *mutelink.Signer {
	t.Helper()
	s, err := mutelink.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// muteToken is the token in a mute URL, checked against signer at now.
func muteToken(t *testing.T, signer *mutelink.Signer, url string, now time.Time) mutelink.Claim {
	t.Helper()
	token, ok := strings.CutPrefix(url, testBase+mutelink.PathPrefix)
	if !ok {
		t.Fatalf("mute URL %q is not under %s%s", url, testBase, mutelink.PathPrefix)
	}
	claim, err := signer.Verify(token, now)
	if err != nil {
		t.Fatalf("mute URL %q: %v", url, err)
	}
	return claim
}

func TestWithMuteLink(t *testing.T) {
	signer := testMuteSigner(t)
	now := time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)
	a := goldenAlerts()

	down := withMuteLink(withLinks(a["down"], testBase), testBase, signer, 4, now)
	claim := muteToken(t, signer, down.MuteURL, now)
	if claim.IncidentID != 312 || claim.ChannelID != 4 || !claim.Expires.Equal(now.Add(mutelink.Lifetime)) {
		t.Errorf("claim = %+v; want incident 312, channel 4, expiring a day after the send", claim)
	}

	reminder := a["down"]
	reminder.Event, reminder.ReminderCount = string(state.EventIncidentReminder), 2
	if got := withMuteLink(reminder, testBase, signer, 4, now); got.MuteURL == "" {
		t.Error("a reminder has no mute link")
	}
	notice := a["notice"]
	if got := withMuteLink(notice, testBase, signer, 4, now); got.MuteURL == "" {
		t.Error("a certificate notice, which reminds like an outage, has no mute link")
	}

	if got := withMuteLink(a["down"], "", signer, 4, now); got.MuteURL != "" {
		t.Errorf("without a base URL: mute link %q", got.MuteURL)
	}
	if got := withMuteLink(a["down"], testBase, nil, 4, now); got.MuteURL != "" {
		t.Errorf("without a signer: mute link %q", got.MuteURL)
	}

	// Nothing to mute: a recovery, an unconfirmed failure, an alert with no
	// incident, a message about SubGlance itself.
	opened := a["down"]
	opened.Event = string(state.EventIncidentOpened)
	noIncident := a["down"]
	noIncident.IncidentID = 0
	for name, alert := range map[string]Alert{
		"recovery":       a["up"],
		"unconfirmed":    opened,
		"no incident":    noIncident,
		"backup failed":  {MonitorName: "SubGlance", Event: EventBackupFailed, IncidentID: 4},
		"channel failed": {MonitorName: "SubGlance", Event: EventChannelFailing},
	} {
		if got := withMuteLink(alert, testBase, signer, 4, now); got.MuteURL != "" {
			t.Errorf("%s: mute link %q, want none", name, got.MuteURL)
		}
	}

	// A grouped alert or a digest: none at the top, one per open member,
	// each for its own incident.
	for _, kind := range []string{"grouped", "digest"} {
		got := withMuteLink(a[kind], testBase, signer, 4, now)
		if got.MuteURL != "" {
			t.Errorf("%s: top-level mute link %q", kind, got.MuteURL)
		}
		for _, m := range got.Members {
			if !mutable(m) {
				if m.MuteURL != "" {
					t.Errorf("%s member %s (%s): mute link %q, want none", kind, m.MonitorName, m.Event, m.MuteURL)
				}
				continue
			}
			if c := muteToken(t, signer, m.MuteURL, now); c.IncidentID != m.IncidentID {
				t.Errorf("%s member %s: link mutes incident %d, want %d", kind, m.MonitorName, c.IncidentID, m.IncidentID)
			}
		}
		if a[kind].Members[0].MuteURL != "" {
			t.Errorf("%s: withMuteLink wrote into the caller's members", kind)
		}
	}
}

// TestEveryChannelButSMSCarriesTheMuteLink: the link reaches every channel
// that has room for more than one, and SMS stays as it was.
func TestEveryChannelButSMSCarriesTheMuteLink(t *testing.T) {
	signer := testMuteSigner(t)
	now := time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)
	plain := withLinks(goldenAlerts()["down"], testBase)
	alert := withMuteLink(plain, testBase, signer, 4, now)
	for channel, got := range renderAll(t, alert) {
		switch channel {
		case "webhook-body":
			continue // carries what its template asks for
		case "sms":
			if strings.Contains(got, mutelink.PathPrefix) {
				t.Errorf("sms carries the mute link: %q", got)
			}
			if want := smsMessage(plain, "Europe/Amsterdam", 0); got != want {
				t.Errorf("sms changed with a mute link:\n got %q\nwant %q", got, want)
			}
			continue
		}
		if !strings.Contains(got, alert.MuteURL) {
			t.Errorf("%s: no mute link in\n%s", channel, got)
		}
	}
}

// TestNotifierSignsTheMuteLinkPerChannel checks the wiring: the signer from
// Options reaches the send, the link names the channel it went to, and the
// outbox row stays without it.
func TestNotifierSignsTheMuteLinkPerChannel(t *testing.T) {
	db := groupDB(t)
	ops := groupChannel(t, db, "ops")
	oncall := groupChannel(t, db, "on-call")
	m := groupMonitor(t, db, "api", ops.ID, oncall.ID)
	at := time.Date(2026, 10, 2, 3, 14, 0, 0, time.UTC)
	inc := openIncident(t, db, m.ID, at, "timeout")

	signer := testMuteSigner(t)
	sender := &fakeSender{}
	n := New(Options{
		DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders:     map[string]Sender{store.ChannelWebhook: sender},
		GroupWindow: GroupingDisabled, BaseURL: testBase, MuteLinks: signer,
		Now: func() time.Time { return at },
	})
	if err := n.Enqueue(context.Background(), m, inc, state.EventIncidentConfirmed, at); err != nil {
		t.Fatal(err)
	}
	if _, err := n.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := sender.delivered()
	if len(got) != 2 {
		t.Fatalf("%d deliveries, want 2", len(got))
	}
	channels := map[int64]bool{}
	for _, a := range got {
		c := muteToken(t, signer, a.MuteURL, at)
		if c.IncidentID != inc.ID {
			t.Errorf("link mutes incident %d, want %d", c.IncidentID, inc.ID)
		}
		channels[c.ChannelID] = true
	}
	if !channels[ops.ID] || !channels[oncall.ID] {
		t.Errorf("links signed for channels %v, want %d and %d", channels, ops.ID, oncall.ID)
	}
}
