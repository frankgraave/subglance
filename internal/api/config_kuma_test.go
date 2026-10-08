package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			creates := len(res.Document.Monitors) + len(res.Document.Channels) + len(res.Document.Maintenance) + len(res.Document.StatusPages)
			if !rep.DryRun || rep.Summary.Create != creates || len(res.Document.Maintenance) == 0 {
				t.Errorf("dry run summary = %+v, want %d creates, maintenance among them", rep.Summary, creates)
			}

			if res.StatusPages != 2 || len(res.Document.StatusPages) != 1 || len(rep.StatusPages) != 1 ||
				rep.StatusPages[0].Key != "public" || rep.StatusPages[0].Action != actionCreate {
				t.Fatalf("dry run pages = %+v, want public created from 2 source pages", rep.StatusPages)
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
				"pushover":         "url",
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

			// Public groups become one ordered list of the imported monitors,
			// referring to their configuration keys rather than Kuma's ids.
			pages, err := db.ListStatusPages(context.Background())
			must(t, err)
			if len(pages) != 1 {
				t.Fatalf("stored status pages = %+v, want only public", pages)
			}
			page := pages[0]
			if page.Slug != "public" || page.Title != "Public status" || page.Description != "Shop and API status" ||
				page.Selection != store.StatusPageSelectMonitors || page.TagKey != "" || page.TagValue != "" ||
				!page.Enabled || !page.Indexable || !page.HideCredit || page.Timezone != "UTC" ||
				page.Language != "en" || page.Accent != "" || page.Logo != nil {
				t.Errorf("stored public page = %+v", page)
			}
			entries, err := db.ListStatusPageEntries(context.Background(), page.ID)
			must(t, err)
			keys, err := db.MonitorConfigKeys(context.Background())
			must(t, err)
			wantEntries := []struct{ key, name string }{
				{"shop-prod", "Shop (prod)"}, {"api-post", "API POST"}, {"status-json", "Status JSON"},
				{"postgres", "Postgres"}, {"router", "Router"},
			}
			if len(entries) != len(wantEntries) {
				t.Fatalf("public page entries = %+v, want %d entries", entries, len(wantEntries))
			}
			publicKeys := map[string]bool{}
			for i, entry := range entries {
				want := wantEntries[i]
				if keys[entry.MonitorID] != want.key || entry.DisplayName != want.name || entry.Position != i {
					t.Errorf("public page entry %d = %+v (monitor key %q), want %s / %s", i, entry,
						keys[entry.MonitorID], want.key, want.name)
				}
				if entry.PublicKey == "" || publicKeys[entry.PublicKey] {
					t.Errorf("public page entry %d has an empty or reused public key: %+v", i, entry)
				}
				publicKeys[entry.PublicKey] = true
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

			// The windows are on the monitors and the group tag they were
			// converted for, in the zones Kuma ran them in.
			windows, err := db.ListMaintenance(context.Background())
			must(t, err)
			ids := map[int64]string{}
			for _, m := range mons {
				ids[m.ID] = m.Name
			}
			var got []string
			for _, w := range windows {
				got = append(got, w.Name+"|"+ids[w.MonitorID]+"|"+w.TagKey+"="+w.TagValue+"|"+w.Timezone)
			}
			slices.Sort(got)
			want := []string{"Daily rotate|Status JSON|=|UTC", "Datacenter move|Postgres|=|",
				"Nightly deploy|API POST|=|Europe/Amsterdam", "Nightly deploy|Shop (prod)|=|Europe/Amsterdam",
				"Sunday cron|Postgres|=|Europe/Amsterdam", "Thursday reboot|Router|=|Europe/Amsterdam",
				"Weekend backup||group=Edge|America/New_York"}
			if !slices.Equal(got, want) {
				t.Errorf("windows = %v, want %v", got, want)
			}

			// Twice is the same as once, for a converted file too.
			code, rep, body = importYAML(t, srv, string(file), true)
			if code != http.StatusOK || rep.Summary.Create != 0 || rep.Summary.Update != 0 {
				t.Errorf("second dry run = %d %+v: %s", code, rep.Summary, body)
			}
			if len(rep.StatusPages) != 1 || rep.StatusPages[0].Action != actionUnchanged {
				t.Errorf("second dry run pages = %+v, want public unchanged", rep.StatusPages)
			}
			code, rep, body = importYAML(t, srv, string(file), false)
			if code != http.StatusOK || rep.Summary.Create != 0 || rep.Summary.Update != 0 ||
				len(rep.StatusPages) != 1 || rep.StatusPages[0].Action != actionUnchanged {
				t.Errorf("second import = %d %+v / %+v: %s", code, rep.Summary, rep.StatusPages, body)
			}
			again, err := db.GetStatusPageBySlug(context.Background(), "public")
			must(t, err)
			againEntries, err := db.ListStatusPageEntries(context.Background(), again.ID)
			must(t, err)
			if again != page || !slices.Equal(againEntries, entries) {
				t.Errorf("second import changed the page or its entry identities: %+v / %+v", again, againEntries)
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

// Password protection has no counterpart on a SubGlance public page. A
// converted page must stay private until an administrator publishes it.
func TestKumaPasswordStatusPageImportsDisabled(t *testing.T) {
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", fixture))
			must(t, err)
			path := filepath.Join(t.TempDir(), "kuma.db")
			must(t, os.WriteFile(path, original, 0o600))
			source, err := sql.Open("sqlite", path)
			must(t, err)
			t.Cleanup(func() { _ = source.Close() })
			const password = "kuma-secret-page"
			changed, err := source.ExecContext(ctx, "UPDATE status_page SET password = ?, published = 1 WHERE slug = 'public'", password)
			must(t, err)
			rows, err := changed.RowsAffected()
			must(t, err)
			if rows != 1 {
				t.Fatalf("password fixture update affected %d rows, want 1", rows)
			}
			must(t, source.Close())

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			if len(res.Document.StatusPages) != 1 {
				t.Fatalf("converted pages = %+v, want the disabled public page", res.Document.StatusPages)
			}
			converted := res.Document.StatusPages[0]
			if converted.Slug != "public" || converted.Enabled == nil || *converted.Enabled {
				t.Fatalf("password page is not explicitly disabled: %+v", converted)
			}
			file, err := kumaimport.Render(res)
			must(t, err)
			if strings.Contains(string(file), password) {
				t.Fatal("the converted file or its report contains the page password")
			}

			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, string(file), true)
			if code != http.StatusOK {
				t.Fatalf("password page dry run = %d: %s", code, body)
			}
			pages, err := db.ListStatusPages(ctx)
			must(t, err)
			if len(pages) != 0 {
				t.Fatalf("password page dry run wrote pages: %+v", pages)
			}
			code, rep, body := importYAML(t, srv, string(file), false)
			if code != http.StatusOK || len(rep.StatusPages) != 1 || rep.StatusPages[0].Action != actionCreate {
				t.Fatalf("password page import = %d %+v: %s", code, rep.StatusPages, body)
			}
			page, err := db.GetStatusPageBySlug(ctx, "public")
			must(t, err)
			if page.Enabled {
				t.Fatal("import published a password-protected Kuma page")
			}
			entries, err := db.ListStatusPageEntries(ctx, page.ID)
			must(t, err)
			if len(entries) != 5 {
				t.Errorf("disabled page entries = %+v, want its 5 converted monitors", entries)
			}
			for _, target := range []string{"/status/public", "/api/v1/status-pages/public"} {
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
				if rec.Code != http.StatusNotFound {
					t.Errorf("GET %s = %d, want 404: %s", target, rec.Code, rec.Body)
				}
				if strings.Contains(rec.Body.String(), page.Title) || strings.Contains(rec.Body.String(), password) {
					t.Errorf("GET %s discloses the protected page", target)
				}
			}
		})
	}
}

// A Kuma Twilio notification becomes an SMS channel the importer accepts as
// it is: created switched off, with the auth token and the recipient left
// to fill in, and still attached to the monitors that used it in Kuma.
func TestKumaTwilioImportsAsAnSMSChannel(t *testing.T) {
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", fixture))
			must(t, err)
			path := filepath.Join(t.TempDir(), "kuma.db")
			must(t, os.WriteFile(path, original, 0o600))
			source, err := sql.Open("sqlite", path)
			must(t, err)
			t.Cleanup(func() { _ = source.Close() })
			sid := "AC" + strings.Repeat("0123456789abcdef", 2)
			config := `{"name":"SMS on-call","type":"twilio","isDefault":false,"applyExisting":false,` +
				`"twilioAccountSID":"` + sid + `","twilioAuthToken":"kuma-secret-twilio",` +
				`"twilioFromNumber":"+12025550100","twilioToNumber":"+12025550123"}`
			added, err := source.ExecContext(ctx,
				"INSERT INTO notification (name, active, user_id, is_default, config) VALUES ('SMS on-call', 1, 1, 0, ?)", config)
			must(t, err)
			id, err := added.LastInsertId()
			must(t, err)
			_, err = source.ExecContext(ctx,
				"INSERT INTO monitor_notification (monitor_id, notification_id) SELECT id, ? FROM monitor WHERE name = 'Shop (prod)'", id)
			must(t, err)
			must(t, source.Close())

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			for _, leak := range []string{"kuma-secret-twilio", "2025550123"} {
				if strings.Contains(string(file), leak) {
					t.Errorf("the converted file contains %q:\n%s", leak, file)
				}
			}

			srv, db := testServerWithDB(t)
			if code, _, body := importYAML(t, srv, string(file), true); code != http.StatusOK {
				t.Fatalf("dry run = %d: %s\n%s", code, body, file)
			}
			code, rep, body := importYAML(t, srv, string(file), false)
			if code != http.StatusOK {
				t.Fatalf("import = %d: %s", code, body)
			}
			var needs string
			for _, it := range rep.Channels {
				if it.Key == "sms-on-call" {
					needs = strings.Join(it.NeedsSecrets, ",")
				}
			}
			if needs != "auth_token,numbers" {
				t.Errorf("sms-on-call needs_secrets = %q, want auth_token,numbers", needs)
			}

			chans, err := db.ListChannels(ctx)
			must(t, err)
			var sms store.Channel
			for _, c := range chans {
				if c.Name == "SMS on-call" {
					sms = c
				}
			}
			if sms.ID == 0 || sms.Type != store.ChannelSMS || sms.Enabled ||
				sms.Config["provider"] != "twilio" || sms.Config["account_sid"] != sid ||
				sms.Config["from"] != "+12025550100" || sms.Config["auth_token"] != "" || sms.Config["numbers"] != "" {
				t.Errorf("stored channel = %+v", sms)
			}
			mons, err := db.ListMonitors(ctx)
			must(t, err)
			var linked bool
			for _, m := range mons {
				if m.Name != "Shop (prod)" {
					continue
				}
				own, err := db.ListMonitorChannels(ctx, m.ID)
				must(t, err)
				for _, c := range own {
					linked = linked || c.ID == sms.ID
				}
			}
			if !linked {
				t.Error("Shop (prod) is not attached to the SMS channel")
			}
		})
	}
}
