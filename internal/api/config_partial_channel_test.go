package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// partialChannelFile is a hand-written file that touches two channels an
// import of withheldFile created without their credentials: it renames one
// and switches the other on, and says nothing about either one's settings.
// It also adds a monitor, so a refusal of the channels would show as the
// whole file writing nothing.
const partialChannelFile = `version: 1
channels:
  - key: discord
    name: Discord alerts
  - key: telegram
    enabled: true
monitors:
  - key: partial-probe
    name: Partial probe
    type: tcp
    target: partial.example.com:443
`

// A file that leaves a channel's settings out keeps the stored ones, and a
// channel stored without its credential still lacks it. Held to the full
// rule, the file above was refused whole ("config.url is required for a
// discord channel") on every instance that had not filled the value in,
// although it names no setting at all.
func TestPartialFileTouchingAChannelStoredWithoutItsCredential(t *testing.T) {
	srv, db := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, withheldFile, false); code != http.StatusOK {
		t.Fatalf("first import = %d: %s", code, body)
	}

	for _, dry := range []bool{true, false} {
		code, rep, body := importYAML(t, srv, partialChannelFile, dry)
		if code != http.StatusOK {
			t.Fatalf("partial file (dry_run=%v) = %d: %s", dry, code, body)
		}
		checkNeeds(t, "partial file", rep, map[string]string{"discord": "url", "telegram": "bot_token"})
		got := map[string]string{}
		for _, it := range rep.Channels {
			got[it.Key] = it.Action + ":" + strings.Join(it.Changes, ",")
		}
		// The rename is a change; switching on a channel that still lacks
		// its credential is not, because it stays off.
		if got["discord"] != actionUpdate+":name" || got["telegram"] != actionUnchanged+":" {
			t.Errorf("partial file (dry_run=%v) channels = %v", dry, got)
		}
		if rep.Summary.Create != 1 {
			t.Errorf("partial file (dry_run=%v) summary = %+v, want the monitor created", dry, rep.Summary)
		}
	}

	chans, err := db.ListChannels(context.Background())
	must(t, err)
	for _, c := range chans {
		switch c.Type {
		case store.ChannelDiscord:
			if c.Name != "Discord alerts" || c.Enabled || len(c.Config) != 0 {
				t.Errorf("discord after the partial file = %q enabled=%v config=%v", c.Name, c.Enabled, c.Config)
			}
		case store.ChannelTelegram:
			if c.Enabled || c.Config["bot_token"] != "" {
				t.Errorf("telegram switched on without its token: enabled=%v config=%v", c.Enabled, c.Config)
			}
		}
	}

	// Applied once, the same file changes nothing the second time.
	code, rep, body := importYAML(t, srv, partialChannelFile, false)
	if code != http.StatusOK || rep.Summary.Create != 0 || rep.Summary.Update != 0 {
		t.Errorf("partial file again = %d %+v: %s", code, rep.Summary, body)
	}
}

// A file that gives such a channel a new type or a new destination is held
// to the full rule still: the stored settings are not what it says.
func TestPartialFileStillHoldsANewDestinationToTheRules(t *testing.T) {
	srv, _ := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, withheldFile, false); code != http.StatusOK {
		t.Fatalf("first import = %d: %s", code, body)
	}
	for name, tc := range map[string]struct{ doc, field string }{
		"new type without config": {"version: 1\nchannels:\n  - key: discord\n    type: slack\n", "channels[0].config"},
		"new type, no url":        {"version: 1\nchannels:\n  - key: discord\n    type: slack\n    config: {}\n", "channels[0].config.url"},
		"bad destination":         {"version: 1\nchannels:\n  - key: discord\n    config:\n      url: ftp://discord.example.com/x\n", "channels[0].config.url"},
	} {
		code, _, body := importYAML(t, srv, tc.doc, true)
		if code != http.StatusBadRequest || !strings.Contains(body, `"field":"`+tc.field+`"`) {
			t.Errorf("%s = %d %s, want a refusal at %s", name, code, body, tc.field)
		}
	}
}
