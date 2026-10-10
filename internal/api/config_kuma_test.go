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
				// The type Kuma sent with the body is stored as it is; the
				// withheld key waits to be filled in, so it is not stored.
				if m.Name == "API POST" && (len(m.Headers) != 1 || m.Headers["Content-Type"] != "application/json") {
					t.Errorf("API POST stored headers = %v, want only Content-Type: application/json", m.Headers)
				}
				// Three retries in Kuma alert on the fourth failure, and so
				// does a threshold of four here; Kuma's default of none
				// alerts on the first, which a stored 0 does too.
				wantRetries := 0
				if m.Name == "Shop (prod)" {
					wantRetries = 4
				}
				if m.Retries != wantRetries {
					t.Errorf("%s stored retries = %d, want %d", m.Name, m.Retries, wantRetries)
				}
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
			sid := "AC" + strings.Repeat("0123456789abcdef", 2)
			config := `{"name":"SMS on-call","type":"twilio","isDefault":false,"applyExisting":false,` +
				`"twilioAccountSID":"` + sid + `","twilioAuthToken":"kuma-secret-twilio",` +
				`"twilioFromNumber":"+12025550100","twilioToNumber":"+12025550123"}`
			path := kumaWithNotification(t, fixture, "SMS on-call", config)

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

// kumaWithNotification copies a Kuma fixture and adds one notification to
// the copy, used by the monitor Shop (prod), as Kuma's own form would have
// stored it. The fixture itself is left alone, so a new notification type
// does not need the fixtures regenerated.
func kumaWithNotification(t *testing.T, fixture, name, config string) string {
	t.Helper()
	ctx := t.Context()
	original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", fixture))
	must(t, err)
	path := filepath.Join(t.TempDir(), "kuma.db")
	must(t, os.WriteFile(path, original, 0o600))
	source, err := sql.Open("sqlite", path)
	must(t, err)
	t.Cleanup(func() { _ = source.Close() })
	added, err := source.ExecContext(ctx,
		"INSERT INTO notification (name, active, user_id, is_default, config) VALUES (?, 1, 1, 0, ?)", name, config)
	must(t, err)
	id, err := added.LastInsertId()
	must(t, err)
	_, err = source.ExecContext(ctx,
		"INSERT INTO monitor_notification (monitor_id, notification_id) SELECT id, ? FROM monitor WHERE name = 'Shop (prod)'", id)
	must(t, err)
	must(t, source.Close())
	return path
}

// A Kuma Home Assistant notification becomes a webhook the importer accepts
// as it is: created switched off with the token's header left to fill in,
// its address and action carried over, and still attached to the monitors
// that used it in Kuma.
func TestKumaHomeAssistantImportsAsAWebhook(t *testing.T) {
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			config := `{"name":"Home","type":"HomeAssistant","isDefault":false,"applyExisting":false,` +
				`"homeAssistantUrl":"https://ha.example.org:8123/","longLivedAccessToken":"kuma-secret-ha",` +
				`"notificationService":"mobile_app_pixel"}`
			path := kumaWithNotification(t, fixture, "Home", config)

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			if strings.Contains(string(file), "kuma-secret-ha") {
				t.Errorf("the converted file contains the access token:\n%s", file)
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
				if it.Key == "home" {
					needs = strings.Join(it.NeedsSecrets, ",")
				}
			}
			if needs != "headers" {
				t.Errorf("home needs_secrets = %q, want headers", needs)
			}

			chans, err := db.ListChannels(ctx)
			must(t, err)
			var hook store.Channel
			for _, c := range chans {
				if c.Name == "Home" {
					hook = c
				}
			}
			if hook.ID == 0 || hook.Type != store.ChannelWebhook || hook.Enabled ||
				hook.Config["url"] != "https://ha.example.org:8123/api/services/notify/mobile_app_pixel" ||
				hook.Config["headers"] != "" || !strings.Contains(hook.Config["body"], `"title": "{{summary}}"`) {
				t.Errorf("stored channel = %+v", hook)
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
					linked = linked || c.ID == hook.ID
				}
			}
			if !linked {
				t.Error("Shop (prod) is not attached to the Home Assistant channel")
			}
		})
	}
}

// A Kuma Signal notification becomes a webhook the importer accepts as it
// is: created switched off with the body, which names phone numbers, left to
// fill in, its address carried over, and still attached to the monitors that
// used it in Kuma. No number is in the file.
func TestKumaSignalImportsAsAWebhook(t *testing.T) {
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			config := `{"name":"Phones","type":"signal","isDefault":false,"applyExisting":false,` +
				`"signalURL":"https://signal.example.org/v2/send","signalNumber":"+12025550123",` +
				`"signalRecipients":"+31612345678,+12025550199"}`
			path := kumaWithNotification(t, fixture, "Phones", config)

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			for _, number := range []string{"2025550123", "612345678", "2025550199"} {
				if strings.Contains(string(file), number) {
					t.Errorf("the converted file contains the phone number %s:\n%s", number, file)
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
				if it.Key == "phones" {
					needs = strings.Join(it.NeedsSecrets, ",")
				}
			}
			if needs != "body" {
				t.Errorf("phones needs_secrets = %q, want body", needs)
			}

			chans, err := db.ListChannels(ctx)
			must(t, err)
			var hook store.Channel
			for _, c := range chans {
				if c.Name == "Phones" {
					hook = c
				}
			}
			if hook.ID == 0 || hook.Type != store.ChannelWebhook || hook.Enabled ||
				hook.Config["url"] != "https://signal.example.org/v2/send" || hook.Config["body"] != "" {
				t.Errorf("stored channel = %+v", hook)
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
					linked = linked || c.ID == hook.ID
				}
			}
			if !linked {
				t.Error("Shop (prod) is not attached to the Signal channel")
			}
		})
	}
}

// A Kuma Bark notification becomes a webhook the importer accepts as it is:
// created switched off with the url, which ends in the device key, left to
// fill in, Kuma's group and sound in the body, and still attached to the
// monitors that used it in Kuma. The key is not in the file.
func TestKumaBarkImportsAsAWebhook(t *testing.T) {
	const key = "kumaSecretDeviceKey42"
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			config := `{"name":"iPhone","type":"Bark","isDefault":false,"applyExisting":false,` +
				`"barkEndpoint":"https://api.day.app/` + key + `","barkGroup":"Uptime","barkSound":"alarm"}`
			path := kumaWithNotification(t, fixture, "iPhone", config)

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			if strings.Contains(string(file), key) {
				t.Errorf("the converted file contains the device key:\n%s", file)
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
				if it.Key == "iphone" {
					needs = strings.Join(it.NeedsSecrets, ",")
				}
			}
			if needs != "url" {
				t.Errorf("iphone needs_secrets = %q, want url", needs)
			}

			chans, err := db.ListChannels(ctx)
			must(t, err)
			var hook store.Channel
			for _, c := range chans {
				if c.Name == "iPhone" {
					hook = c
				}
			}
			const wantBody = `{ "title": "{{summary}}", "body": "{{details}}", "group": "Uptime", "sound": "alarm" }` + "\n"
			if hook.ID == 0 || hook.Type != store.ChannelWebhook || hook.Enabled ||
				hook.Config["url"] != "" || hook.Config["body"] != wantBody {
				t.Errorf("stored channel = %+v", hook)
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
					linked = linked || c.ID == hook.ID
				}
			}
			if !linked {
				t.Error("Shop (prod) is not attached to the Bark channel")
			}
		})
	}
}

// A Kuma WhatsApp (WAHA) notification becomes a webhook the importer accepts
// as it is: created switched off with the body, which names a phone number,
// and the header with the API key left to fill in, its address carried over,
// and still attached to the monitors that used it in Kuma. Neither the
// number nor the key is in the file.
func TestKumaWAHAImportsAsAWebhook(t *testing.T) {
	const key = "kumaSecretWahaKey42"
	for _, fixture := range []string{"kuma-1.23.16.db", "kuma-2.5.5.db"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := t.Context()
			config := `{"name":"WhatsApp","type":"waha","isDefault":false,"applyExisting":false,` +
				`"wahaApiUrl":"https://wa.example.org/","wahaApiKey":"` + key + `",` +
				`"wahaSession":"default","wahaChatId":"31612345678@c.us"}`
			path := kumaWithNotification(t, fixture, "WhatsApp", config)

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			for _, secret := range []string{key, "612345678"} {
				if strings.Contains(string(file), secret) {
					t.Errorf("the converted file contains %s:\n%s", secret, file)
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
				if it.Key == "whatsapp" {
					needs = strings.Join(it.NeedsSecrets, ",")
				}
			}
			if needs != "body,headers" {
				t.Errorf("whatsapp needs_secrets = %q, want body,headers", needs)
			}

			chans, err := db.ListChannels(ctx)
			must(t, err)
			var hook store.Channel
			for _, c := range chans {
				if c.Name == "WhatsApp" {
					hook = c
				}
			}
			if hook.ID == 0 || hook.Type != store.ChannelWebhook || hook.Enabled ||
				hook.Config["url"] != "https://wa.example.org/api/sendText" || hook.Config["body"] != "" || hook.Config["headers"] != "" {
				t.Errorf("stored channel = %+v", hook)
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
					linked = linked || c.ID == hook.ID
				}
			}
			if !linked {
				t.Error("Shop (prod) is not attached to the WhatsApp channel")
			}
		})
	}
}

// Kuma retries a push monitor before alerting; SubGlance confirms the first
// missed report. The wait comes over as grace, which the importer stores.
func TestKumaPushRetriesImportAsGrace(t *testing.T) {
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
			changed, err := source.ExecContext(ctx,
				"UPDATE monitor SET maxretries = 2, retry_interval = 3600 WHERE name = 'Nightly backup'")
			must(t, err)
			if rows, err := changed.RowsAffected(); err != nil || rows != 1 {
				t.Fatalf("fixture update affected %d rows (%v), want 1", rows, err)
			}
			must(t, source.Close())

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			srv, db := testServerWithDB(t)
			if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
				t.Fatalf("import = %d: %s", code, body)
			}
			mons, err := db.ListMonitors(ctx)
			must(t, err)
			i := slices.IndexFunc(mons, func(m store.Monitor) bool { return m.Name == "Nightly backup" })
			if i < 0 {
				t.Fatal("Nightly backup was not imported")
			}
			if m := mons[i]; m.PushIntervalS != 86400 || m.PushGraceS != 7200 {
				t.Errorf("Nightly backup push window = %ds + %ds, want 86400s + 7200s", m.PushIntervalS, m.PushGraceS)
			}
		})
	}
}

// Kuma warned about an expiring certificate at the days listed in its
// settings; the import stores a warning that starts as early, and leaves the
// default on a monitor whose certificate errors Kuma ignored.
func TestKumaCertificateWarningImportsAsSSLWarnDays(t *testing.T) {
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
			changed, err := source.ExecContext(ctx,
				"UPDATE monitor SET expiry_notification = 1 WHERE name IN ('Shop (prod)', 'API POST')")
			must(t, err)
			if rows, err := changed.RowsAffected(); err != nil || rows != 2 {
				t.Fatalf("fixture update affected %d rows (%v), want 2", rows, err)
			}
			_, err = source.ExecContext(ctx,
				"INSERT INTO setting (key, value, type) VALUES ('tlsExpiryNotifyDays', '[7, 30]', 'general')")
			must(t, err)
			must(t, source.Close())

			res, err := kumaimport.Convert(ctx, path)
			must(t, err)
			file, err := kumaimport.Render(res)
			must(t, err)
			srv, db := testServerWithDB(t)
			if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
				t.Fatalf("import = %d: %s", code, body)
			}
			mons, err := db.ListMonitors(ctx)
			must(t, err)
			for name, want := range map[string]int{"Shop (prod)": 31, "API POST": 14, "Intranet": 14} {
				i := slices.IndexFunc(mons, func(m store.Monitor) bool { return m.Name == name })
				if i < 0 {
					t.Fatalf("%s was not imported", name)
				}
				if got := mons[i].SSLWarnDays; got != want {
					t.Errorf("%s ssl_warn_days = %d, want %d", name, got, want)
				}
			}
		})
	}
}

// Kuma 2 warned before the registration of a monitor's domain expired. The
// import adds one domain monitor per domain, warning as early as Kuma did and
// attached to every channel the Kuma monitors on that domain alerted through.
func TestKumaDomainWarningImportsAsDomainMonitors(t *testing.T) {
	ctx := t.Context()
	original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", "kuma-2.5.5.db"))
	must(t, err)
	path := filepath.Join(t.TempDir(), "kuma.db")
	must(t, os.WriteFile(path, original, 0o600))
	source, err := sql.Open("sqlite", path)
	must(t, err)
	t.Cleanup(func() { _ = source.Close() })
	changed, err := source.ExecContext(ctx,
		"UPDATE monitor SET domain_expiry_notification = 1 WHERE name IN ('Shop (prod)', 'API POST', 'Router')")
	must(t, err)
	if rows, err := changed.RowsAffected(); err != nil || rows != 3 {
		t.Fatalf("fixture update affected %d rows (%v), want 3", rows, err)
	}
	_, err = source.ExecContext(ctx,
		"INSERT INTO setting (key, value, type) VALUES ('domainExpiryNotifyDays', '[10, 30]', 'general')")
	must(t, err)
	must(t, source.Close())

	res, err := kumaimport.Convert(ctx, path)
	must(t, err)
	if res.DomainMonitors != 2 {
		t.Fatalf("DomainMonitors = %d, want 2", res.DomainMonitors)
	}
	file, err := kumaimport.Render(res)
	must(t, err)
	srv, db := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, string(file), true); code != http.StatusOK {
		t.Fatalf("dry run = %d: %s\n%s", code, body, file)
	}
	if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}

	mons, err := db.ListMonitors(ctx)
	must(t, err)
	want := map[string][]string{
		"example.com registration": {"Discord ops", "Mail admins", "Slack alerts"},
		"example.net registration": {"Gotify", "Pushover"},
	}
	found := 0
	for _, m := range mons {
		if m.Type != store.TypeDomain {
			continue
		}
		found++
		channels, ok := want[m.Name]
		if !ok {
			t.Errorf("unexpected domain monitor %q", m.Name)
			continue
		}
		if m.DomainWarnDays != 31 || m.IntervalS != 86400 || !m.Enabled || m.Target != strings.TrimSuffix(m.Name, " registration") {
			t.Errorf("%s = target %q, domain_warn_days %d, interval %ds, enabled %v; want its domain, 31, 86400s, true",
				m.Name, m.Target, m.DomainWarnDays, m.IntervalS, m.Enabled)
		}
		own, err := db.ListMonitorChannels(ctx, m.ID)
		must(t, err)
		var names []string
		for _, c := range own {
			names = append(names, c.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, channels) {
			t.Errorf("%s channels = %v, want %v", m.Name, names, channels)
		}
	}
	if found != len(want) {
		t.Errorf("%d domain monitors imported, want %d", found, len(want))
	}
}

// Kuma 2 kept the response of a failed check unless the monitor said not to.
// A monitor that said not to arrives with capture off, rather than with the
// capture a new SubGlance monitor gets by default.
func TestKumaSavedErrorResponseImportsAsCaptureResponse(t *testing.T) {
	ctx := t.Context()
	original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", "kuma-2.5.5.db"))
	must(t, err)
	path := filepath.Join(t.TempDir(), "kuma.db")
	must(t, os.WriteFile(path, original, 0o600))
	source, err := sql.Open("sqlite", path)
	must(t, err)
	t.Cleanup(func() { _ = source.Close() })
	changed, err := source.ExecContext(ctx, "UPDATE monitor SET save_error_response = 0 WHERE name = 'API POST'")
	must(t, err)
	if rows, err := changed.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("fixture update affected %d rows (%v), want 1", rows, err)
	}
	must(t, source.Close())

	res, err := kumaimport.Convert(ctx, path)
	must(t, err)
	file, err := kumaimport.Render(res)
	must(t, err)
	srv, db := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	for name, want := range map[string]bool{"API POST": false, "Shop (prod)": true} {
		i := slices.IndexFunc(mons, func(m store.Monitor) bool { return m.Name == name })
		if i < 0 {
			t.Fatalf("%s was not imported", name)
		}
		if got := mons[i].CaptureResponse; got != want {
			t.Errorf("%s capture_response = %v, want %v", name, got, want)
		}
	}
}
