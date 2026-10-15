package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/notifier"
)

// Kuma accepted server addresses that no request from SubGlance can reach.
// Written into the file as they are, each one failed the importer's dry run
// for the whole file; withheld, the channel comes over to be filled in.
func TestAnAddressTheSenderRefusesIsWithheld(t *testing.T) {
	cases := []struct {
		name   string
		config string
		notes  []string
		absent []string
	}{
		{"an ntfy port above the range", `{"type":"ntfy","ntfyserverurl":"https://ntfy.example.org:99999"}`,
			[]string{`Kuma's address "https://ntfy.example.org:99999" is not one SubGlance can post to`, "port 99999 is out of range"}, nil},
		{"an ntfy port of 0", `{"type":"ntfy","ntfyserverurl":"https://ntfy.example.org:0"}`,
			[]string{"port 0 is out of range"}, nil},
		{"an ntfy server without a scheme", `{"type":"ntfy","ntfyserverurl":"ntfy.example.org"}`,
			[]string{`"ntfy.example.org" is not one SubGlance can post to`}, nil},
		{"an ntfy server on an onion address", `{"type":"ntfy","ntfyserverurl":"http://abc.onion"}`,
			[]string{"Tor onion address"}, nil},
		{"an ntfy server with a path", `{"type":"ntfy","ntfyserverurl":"https://example.org/ntfy"}`,
			[]string{"must name the ntfy server"}, nil},
		{"a Gotify port above the range", `{"type":"gotify","gotifyserverurl":"https://gotify.example.org:99999"}`,
			[]string{"port 99999 is out of range"}, nil},
		{"a Gotify host with a space", `{"type":"gotify","gotifyserverurl":"https://gotify example.org"}`,
			[]string{"is not one SubGlance can post to"}, nil},
		{"a Gotify server Kuma had no address for", `{"type":"gotify"}`,
			[]string{"Kuma's settings gave no address; fill in the url"}, nil},
		{"a Matrix homeserver port above the range", `{"type":"matrix","homeserverUrl":"https://matrix.example.org:99999","internalRoomId":"!abc:example.org"}`,
			[]string{"port 99999 is out of range"}, nil},
		{"a Matrix hostname the resolver refuses", `{"type":"matrix","homeserverUrl":"https://bad_host!.example.org","internalRoomId":"!abc:example.org"}`,
			[]string{`contains "!"`}, nil},
		{"a Signal port above the range", `{"type":"signal","signalURL":"https://signal.example.org:99999/v2/send"}`,
			[]string{"port 99999 is out of range"}, nil},
		{"a WAHA port of 0", `{"type":"waha","wahaApiUrl":"https://waha.example.org:0","wahaSession":"default"}`,
			[]string{"port 0 is out of range"}, nil},
		{"an Evolution API port above the range", `{"type":"evolution","evolutionApiUrl":"https://evo.example.org:70000","evolutionInstanceName":"i"}`,
			[]string{"port 70000 is out of range"}, nil},
		{"an OpenWA port above the range", `{"type":"openwa","openwaApiUrl":"https://openwa.example.org:70000","openwaSession":"s"}`,
			[]string{"port 70000 is out of range"}, nil},
		{"a Home Assistant port above the range", `{"type":"HomeAssistant","homeAssistantUrl":"https://ha.example.org:70000","notificationService":"notify"}`,
			[]string{"port 70000 is out of range"}, nil},
		// A user name in front of the host can be a credential for a proxy,
		// so neither the address nor the sender's reason is quoted.
		{"a refused address with a user name", `{"type":"gotify","gotifyserverurl":"https://admin:hunter2@gotify.example.org:99999"}`,
			[]string{"Kuma's address is not one SubGlance can post to, so it was left out"}, []string{"hunter2", "99999"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, notes := convertKuma(t, tc.config)
			if c.Config["url"] != configfile.Placeholder {
				t.Errorf("url = %q, want it withheld", c.Config["url"])
			}
			for _, want := range append(tc.notes, "fill in the url") {
				if !strings.Contains(notes, want) {
					t.Errorf("notes = %q, want %q", notes, want)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(notes, unwanted) {
					t.Errorf("notes = %q, contain %q", notes, unwanted)
				}
			}
			if err := notifier.ValidateConfig(c.Type, withStandIns(c)); err != nil {
				t.Errorf("the converted channel is still refused: %v", err)
			}
		})
	}
}

// A setting other than the address that the sender refuses keeps the
// channel out of the file, with the sender's reason, rather than failing
// the import of every other channel and monitor.
func TestAChannelTheSenderStillRefusesIsSkipped(t *testing.T) {
	var res Result
	_, ok := convertChannel(row{"id": int64(1), "name": "Phone", "active": int64(1), "config": `{"type":"ntfy",` +
		`"ntfyserverurl":"https://ntfy.example.org:99999","ntfyAuthenticationMethod":"usernamePassword"}`}, &res)
	if ok {
		t.Fatalf("converted, want skipped: %s", notesOf(res))
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "username and password must be set together") {
		t.Errorf("skipped = %+v, want the sender's reason", res.Skipped)
	}
	// The address note belongs to a channel that comes over; a skipped one
	// is reported once.
	if len(res.Changed) != 0 {
		t.Errorf("changed = %+v, want nothing for a skipped channel", res.Changed)
	}
}

// An address the sender accepts comes over as it is, with no note about it.
func TestAnAddressTheSenderAcceptsIsKept(t *testing.T) {
	c, notes := convertKuma(t, `{"type":"gotify","gotifyserverurl":"https://gotify.example.org:8443"}`)
	if c.Config["url"] != "https://gotify.example.org:8443" || strings.Contains(notes, "fill in the url") {
		t.Errorf("url = %q, notes = %q, want the address kept", c.Config["url"], notes)
	}
}
