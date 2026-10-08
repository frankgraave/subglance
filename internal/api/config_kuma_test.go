package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/kumaimport"
	"github.com/frankgraave/subglance/internal/store"
)

// A file converted from a real Kuma database must pass the importer as it
// is: the converter promises a file the user can review and import, and a
// dry run that refuses it would break that promise at the first step. The
// fixtures were written by Kuma 1.23 and 2.5 themselves.
func TestKumaConversionPassesTheImporter(t *testing.T) {
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			res, err := kumaimport.Convert(context.Background(), "../kumaimport/testdata/"+fixture)
			if err != nil {
				t.Fatal(err)
			}
			file, err := kumaimport.Render(res)
			if err != nil {
				t.Fatal(err)
			}
			srv, db := testServerWithDB(t)

			code, rep, body := importYAML(t, srv, string(file), true)
			if code != http.StatusOK {
				t.Fatalf("dry run = %d: %s\n%s", code, body, file)
			}
			if !rep.DryRun || rep.Summary.Create != len(res.Document.Monitors)+len(res.Document.Channels) {
				t.Errorf("dry run summary = %+v, want %d creates", rep.Summary,
					len(res.Document.Monitors)+len(res.Document.Channels))
			}

			code, rep, body = importYAML(t, srv, string(file), false)
			if code != http.StatusOK {
				t.Fatalf("import = %d: %s", code, body)
			}

			// Every channel came over without its credential, so every one is
			// switched off and says which value is missing.
			needs := map[string]string{}
			for _, it := range append(rep.Channels, rep.Monitors...) {
				needs[it.Key] = strings.Join(it.NeedsSecrets, ",")
			}
			for key, want := range map[string]string{
				"discord-ops":      "url",
				"slack-alerts":     "url",
				"telegram-on-call": "bot_token",
				"mail-admins":      "password",
				"ntfy-phone":       "password,topic",
				"gotify":           "token",
				"webhook-json":     "headers,url",
				"api-post":         "body,headers.X-Api-Key",
				"intranet":         "headers.Authorization",
				"shop-prod":        "",
			} {
				if needs[key] != want {
					t.Errorf("%s needs_secrets = %q, want %q", key, needs[key], want)
				}
			}
			chans, err := db.ListChannels(context.Background())
			must(t, err)
			for _, c := range chans {
				if c.Enabled {
					t.Errorf("channel %q is enabled without its credential", c.Name)
				}
			}

			mons, err := db.ListMonitors(context.Background())
			must(t, err)
			byName := map[string]bool{}
			for _, m := range mons {
				byName[m.Name] = m.Enabled
			}
			// Paused in Kuma stays paused; a monitor waiting for a header is
			// paused until it is filled in; the rest run.
			for name, enabled := range map[string]bool{"Shop (prod)": true, "Old site": false, "Intranet": false, "CDN": true} {
				if got, ok := byName[name]; !ok || got != enabled {
					t.Errorf("monitor %q enabled = %v (present %v), want %v", name, got, ok, enabled)
				}
			}

			// The push monitor is issued a new URL, shown once.
			var pushURL string
			for _, it := range rep.Monitors {
				if it.Key == "nightly-backup" {
					pushURL = it.PushURL
				}
			}
			if !strings.Contains(pushURL, "/push/") {
				t.Errorf("push_url = %q, want a new push URL", pushURL)
			}

			// Twice is the same as once, for a converted file too.
			code, rep, body = importYAML(t, srv, string(file), true)
			if code != http.StatusOK || rep.Summary.Create != 0 || rep.Summary.Update != 0 {
				t.Errorf("second dry run = %d %+v: %s", code, rep.Summary, body)
			}
			// The DNS monitor is a dns monitor with Kuma's resolver.
			if slices.ContainsFunc(res.Skipped, func(n kumaimport.Note) bool { return n.Type == "dns" }) {
				t.Errorf("the DNS monitor was skipped: %v", res.Skipped)
			}
			var dns *store.Monitor
			for i := range mons {
				if mons[i].Name == "DNS example.com" {
					dns = &mons[i]
				}
			}
			if dns == nil || dns.Type != store.TypeDNS || dns.Target != "example.com" || !dns.Enabled ||
				dns.DNS == nil || dns.DNS.RecordType != "A" || dns.DNS.Resolver != "1.1.1.1" || len(dns.DNS.Expected) != 0 {
				t.Errorf("DNS example.com = %+v", dns)
			}
		})
	}
}
