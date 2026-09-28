package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// Secrets the seeded instance holds. The export must contain none of them.
const (
	secretSlackURL  = "https://hooks.example.com/services/T000/B000/slack-secret-path"
	secretBotToken  = "123456:telegram-bot-secret"
	secretSMTPPass  = "smtp-password-secret"
	secretAuthValue = "Bearer header-secret"
	secretBody      = `{"api_key":"body-secret"}`
)

// seedConfig fills a database with one of everything a file carries, with a
// credential in every place one can hide.
func seedConfig(t *testing.T, db *store.DB) (pushToken string) {
	t.Helper()
	ctx := context.Background()

	slack, err := db.CreateChannel(ctx, store.Channel{Name: "Ops Slack", Type: store.ChannelSlack,
		Enabled: true, Config: map[string]string{"url": secretSlackURL}})
	must(t, err)
	tg, err := db.CreateChannel(ctx, store.Channel{Name: "On call", Type: store.ChannelTelegram,
		Enabled: true, Config: map[string]string{"bot_token": secretBotToken, "chat_id": "-100200"}})
	must(t, err)
	mail, err := db.CreateChannel(ctx, store.Channel{Name: "Mail", Type: store.ChannelEmail, Enabled: false,
		Config: map[string]string{"to": "ops@example.com", "from": "sg@example.com", "host": "smtp.example.com",
			"port": "587", "username": "sg", "password": secretSMTPPass}})
	must(t, err)
	must(t, db.SetDefaultChannel(ctx, mail.ID))
	must(t, db.SetQuietHours(ctx, store.QuietHours{ChannelID: tg.ID, Start: "23:00", End: "07:00",
		Timezone: "Europe/Amsterdam", During: store.QuietHold}))

	api, err := db.CreateMonitor(ctx, store.Monitor{Name: "API (prod)", Type: "http",
		Target: "https://api.example.com/health", IntervalS: 30, Retries: 3, Enabled: true,
		Method: "POST", Keyword: "ok", KeywordMode: "must_contain", FollowRedirects: true,
		Headers: map[string]string{"Authorization": secretAuthValue}, Body: secretBody,
		MinTLSVersion: 0x0303, RepeatAfterS: 900, CaptureResponse: true,
		Tags: map[string]string{"env": "prod", "team": "core"}})
	must(t, err)
	db1, err := db.CreateMonitor(ctx, store.Monitor{Name: "Database", Type: "tcp",
		Target: "db.example.com:5432", Enabled: true, Tags: map[string]string{"env": "prod"}})
	must(t, err)
	backup, err := db.CreateMonitor(ctx, store.Monitor{Name: "Nightly backup", Type: store.TypePush,
		PushIntervalS: 86400, PushGraceS: 600, Enabled: true})
	must(t, err)
	must(t, db.SetMonitorChannels(ctx, api.ID, []int64{slack.ID, tg.ID}))

	rule, err := db.CreateRoutingRule(ctx, store.RoutingRule{TagKey: "env", TagValue: "prod",
		ChannelIDs: []int64{tg.ID}})
	must(t, err)
	must(t, db.ExcludeMonitorFromRule(ctx, rule.ID, db1.ID))

	_, err = db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "Patch night", TagKey: "env",
		TagValue: "prod", Timezone: "Europe/Amsterdam", Weekdays: []int{2}, LocalTime: "02:00",
		DurationMinutes: 60})
	must(t, err)
	future := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	_, err = db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "Migration", MonitorID: db1.ID,
		StartsAt: future, EndsAt: future.Add(time.Hour)})
	must(t, err)
	past := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	_, err = db.CreateMaintenance(ctx, store.MaintenanceWindow{Name: "Done already", MonitorID: api.ID,
		StartsAt: past, EndsAt: past.Add(time.Hour)})
	must(t, err)
	return backup.PushToken
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exportYAML(t *testing.T, srv *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q", ct)
	}
	return rec.Body.String()
}

func importYAML(t *testing.T, srv *Server, body string, dryRun bool) (int, importReport, string) {
	t.Helper()
	path := "/api/v1/config/import"
	if dryRun {
		path += "?dry_run=true"
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	var rep importReport
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("decode report: %v", err)
		}
	}
	return rec.Code, rep, rec.Body.String()
}

// fillSecrets stands in for the person who fills in the placeholders.
func fillSecrets(doc string) string {
	r := strings.NewReplacer(
		"url: "+configfile.Placeholder, "url: "+secretSlackURL,
		"bot_token: "+configfile.Placeholder, "bot_token: "+secretBotToken,
		"password: "+configfile.Placeholder, "password: "+secretSMTPPass,
		"Authorization: "+configfile.Placeholder, "Authorization: "+secretAuthValue,
		"body: "+configfile.Placeholder, "body: '"+secretBody+"'",
	)
	return r.Replace(doc)
}

func TestExportContainsNoSecrets(t *testing.T) {
	srv, db := testServerWithDB(t)
	pushToken := seedConfig(t, db)

	out := exportYAML(t, srv)
	for _, secret := range []string{secretSlackURL, "slack-secret-path", secretBotToken, "telegram-bot-secret",
		secretSMTPPass, secretAuthValue, "header-secret", "body-secret", pushToken} {
		if strings.Contains(out, secret) {
			t.Errorf("export contains %q:\n%s", secret, out)
		}
	}
	// What is not a credential is kept, so the file still says where alerts go.
	for _, kept := range []string{"chat_id: \"-100200\"", "to: ops@example.com", "Authorization: " +
		configfile.Placeholder, "key: api-prod", "key: ops-slack", "default: true"} {
		if !strings.Contains(out, kept) {
			t.Errorf("export lacks %q:\n%s", kept, out)
		}
	}
	if strings.Contains(out, "Done already") {
		t.Errorf("export carries a maintenance window that has ended:\n%s", out)
	}
	if strings.Contains(out, "push_token") || strings.Contains(out, "push_url") {
		t.Errorf("export mentions the push credential:\n%s", out)
	}
}

// TestExportImportRoundTrip is the acceptance test: a file exported from one
// instance, with its placeholders filled in, recreates the same configuration
// on an empty one, and importing it a second time changes nothing.
func TestExportImportRoundTrip(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	seedConfig(t, srcDB)
	exported := exportYAML(t, src)

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, fillSecrets(exported), false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	// 3 channels, 3 monitors, 1 routing rule, 2 maintenance windows.
	if rep.Summary.Create != 9 || rep.Summary.Update != 0 || rep.Summary.NeedsSecrets != 0 {
		t.Errorf("summary = %+v, want 9 creates and nothing else", rep.Summary)
	}
	for _, m := range rep.Monitors {
		if m.Key == "nightly-backup" && !strings.Contains(m.PushURL, "/api/v1/push/") {
			t.Errorf("created push monitor carries no new push URL: %+v", m)
		}
	}

	if again := exportYAML(t, dst); again != exported {
		t.Errorf("the imported instance exports differently.\nsource:\n%s\ndestination:\n%s", exported, again)
	}

	// The secrets that were filled in arrived where they belong.
	ctx := context.Background()
	chans, err := dstDB.ListChannels(ctx)
	must(t, err)
	for _, c := range chans {
		if c.Type == store.ChannelSlack && c.Config["url"] != secretSlackURL {
			t.Errorf("slack url = %q", c.Config["url"])
		}
	}
	mons, err := dstDB.ListMonitors(ctx)
	must(t, err)
	for _, m := range mons {
		if m.Type == "http" && (m.Headers["Authorization"] != secretAuthValue || m.Body != secretBody) {
			t.Errorf("http monitor headers/body = %v / %q", m.Headers, m.Body)
		}
	}

	// Second import: a no-op.
	code, rep, body = importYAML(t, dst, fillSecrets(exported), false)
	if code != http.StatusOK {
		t.Fatalf("second import = %d: %s", code, body)
	}
	if rep.Summary.Create != 0 || rep.Summary.Update != 0 {
		t.Errorf("second import summary = %+v, want everything unchanged\n%s", rep.Summary, body)
	}
	after, err := dstDB.ListMonitors(ctx)
	must(t, err)
	if len(after) != len(mons) {
		t.Errorf("second import changed the monitor count from %d to %d", len(mons), len(after))
	}
	windows, err := dstDB.ListMaintenance(ctx)
	must(t, err)
	if len(windows) != 2 {
		t.Errorf("maintenance windows after two imports = %d, want 2", len(windows))
	}
}

func TestImportKeepsSecretsTheInstanceAlreadyHas(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	exported := exportYAML(t, srv)

	// Placeholders untouched, re-imported where they came from: nothing to do,
	// and no credential is overwritten with the placeholder text.
	code, rep, body := importYAML(t, srv, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if rep.Summary.Create != 0 || rep.Summary.Update != 0 || rep.Summary.NeedsSecrets != 0 {
		t.Errorf("summary = %+v, want all unchanged\n%s", rep.Summary, body)
	}
	chans, err := db.ListChannels(context.Background())
	must(t, err)
	for _, c := range chans {
		for k, v := range c.Config {
			if v == configfile.Placeholder {
				t.Errorf("channel %q config %s was overwritten with the placeholder", c.Name, k)
			}
		}
	}
}

func TestImportWithoutSecretsDisablesAndReports(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	seedConfig(t, srcDB)
	exported := exportYAML(t, src)

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	needs := map[string][]string{}
	for _, it := range append(rep.Channels, rep.Monitors...) {
		if len(it.NeedsSecrets) > 0 {
			needs[it.Key] = it.NeedsSecrets
		}
	}
	want := map[string]string{"ops-slack": "url", "on-call": "bot_token", "mail": "password",
		"api-prod": "body,headers.Authorization"}
	for key, fields := range want {
		if strings.Join(needs[key], ",") != fields {
			t.Errorf("%s needs_secrets = %v, want %s", key, needs[key], fields)
		}
	}
	if rep.Summary.NeedsSecrets != len(want) {
		t.Errorf("summary.needs_secrets = %d, want %d", rep.Summary.NeedsSecrets, len(want))
	}

	ctx := context.Background()
	chans, err := dstDB.ListChannels(ctx)
	must(t, err)
	for _, c := range chans {
		if c.Enabled {
			t.Errorf("channel %q is enabled without its credential", c.Name)
		}
		for k, v := range c.Config {
			if v == configfile.Placeholder || strings.Contains(v, "invalid") {
				t.Errorf("channel %q stored %s = %q", c.Name, k, v)
			}
		}
	}
	mons, err := dstDB.ListMonitors(ctx)
	must(t, err)
	for _, m := range mons {
		if m.Type == "http" {
			if m.Enabled || len(m.Headers) != 0 || m.Body != "" {
				t.Errorf("http monitor without its secrets: enabled=%v headers=%v body=%q",
					m.Enabled, m.Headers, m.Body)
			}
		} else if !m.Enabled {
			t.Errorf("monitor %q was disabled although it needs no secret", m.Name)
		}
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	seedConfig(t, srcDB)
	exported := exportYAML(t, src)

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, fillSecrets(exported), true)
	if code != http.StatusOK || !rep.DryRun {
		t.Fatalf("dry run = %d: %s", code, body)
	}
	if rep.Summary.Create != 9 {
		t.Errorf("dry run summary = %+v, want 9 creates", rep.Summary)
	}
	ctx := context.Background()
	mons, _ := dstDB.ListMonitors(ctx)
	chans, _ := dstDB.ListChannels(ctx)
	rules, _ := dstDB.ListRoutingRules(ctx)
	windows, _ := dstDB.ListMaintenance(ctx)
	keys, _ := dstDB.MonitorConfigKeys(ctx)
	if len(mons)+len(chans)+len(rules)+len(windows)+len(keys) != 0 {
		t.Errorf("dry run wrote: %d monitors, %d channels, %d rules, %d windows, %d keys",
			len(mons), len(chans), len(rules), len(windows), len(keys))
	}
}

func TestImportReportsUpdatesByField(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	exported := exportYAML(t, srv)

	edited := strings.Replace(exported, "interval_s: 30", "interval_s: 120", 1)
	code, rep, body := importYAML(t, srv, edited, true)
	if code != http.StatusOK {
		t.Fatalf("dry run = %d: %s", code, body)
	}
	for _, m := range rep.Monitors {
		if m.Key == "api-prod" {
			if m.Action != actionUpdate || strings.Join(m.Changes, ",") != "interval_s" {
				t.Errorf("api-prod = %+v, want an update of interval_s only", m)
			}
		} else if m.Action != actionUnchanged {
			t.Errorf("%s = %+v, want unchanged", m.Key, m)
		}
	}
}

func TestImportOmittedFieldsKeepCurrentValues(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	exportYAML(t, srv) // assigns keys

	// A hand-written file that renames one monitor and says nothing else.
	code, rep, body := importYAML(t, srv, "version: 1\nmonitors:\n  - key: api-prod\n    name: API\n", false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if got := strings.Join(rep.Monitors[0].Changes, ","); got != "name" {
		t.Errorf("changes = %q, want name", got)
	}
	mons, err := db.ListMonitors(context.Background())
	must(t, err)
	for _, m := range mons {
		if m.Name == "API" && (m.IntervalS != 30 || m.Headers["Authorization"] != secretAuthValue ||
			m.Tags["team"] != "core") {
			t.Errorf("rename reset other fields: %+v", m)
		}
	}
	chans, err := db.MonitorChannelSummaries(context.Background())
	must(t, err)
	total := 0
	for _, c := range chans {
		total += len(c)
	}
	if total != 2 {
		t.Errorf("channel assignments = %d after an import that did not mention them, want 2", total)
	}
}

func TestImportRejectsInvalidFilesAndWritesNothing(t *testing.T) {
	cases := map[string]struct {
		doc, field string
	}{
		"bad interval": {"version: 1\nchannels:\n  - key: ops\n    name: Ops\n    type: webhook\n" +
			"    config:\n      url: https://hook.example.com/x\nmonitors:\n  - key: a\n    name: A\n    type: tcp\n" +
			"    target: a.example.com:1\n  - key: b\n    name: B\n    type: tcp\n    target: b.example.com:1\n" +
			"    interval_s: 5\n", "monitors[1].interval_s"},
		"unknown channel": {"version: 1\nmonitors:\n  - key: a\n    name: A\n    type: tcp\n" +
			"    target: a.example.com:1\n    channels: [nope]\n", "monitors[0].channels[0]"},
		"unknown type":  {"version: 1\nmonitors:\n  - key: a\n    name: A\n    type: gopher\n    target: x\n", "monitors[0].type"},
		"bad channel":   {"version: 1\nchannels:\n  - key: c\n    name: C\n    type: fax\n", "channels[0]"},
		"future format": {"version: 9\n", "version"},
		"bad window": {"version: 1\nmaintenance:\n  - name: W\n    tag_key: env\n    tag_value: prod\n" +
			"    timezone: Local\n", "maintenance[0]"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, tc.doc, false)
			if code != http.StatusBadRequest {
				t.Fatalf("import = %d, want 400: %s", code, body)
			}
			var e errorResponse
			must(t, json.Unmarshal([]byte(body), &e))
			if e.Field != tc.field {
				t.Errorf("field = %q, want %q (%s)", e.Field, tc.field, e.Error)
			}
			ctx := context.Background()
			mons, _ := db.ListMonitors(ctx)
			chans, _ := db.ListChannels(ctx)
			if len(mons)+len(chans) != 0 {
				t.Errorf("a rejected file wrote %d monitors and %d channels", len(mons), len(chans))
			}
		})
	}
}

func TestConfigEndpointsNeedAnEditor(t *testing.T) {
	srv, db := testServerWithDB(t)
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/config/export", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/config/import?dry_run=true", strings.NewReader("version: 1\n")),
	} {
		req.Header.Set("Authorization", "Bearer "+viewer)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as viewer = %d, want 403", req.Method, req.URL.Path, rec.Code)
		}
	}
}

func TestExportKeysSurviveRenames(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := context.Background()
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "Shop", Type: "tcp",
		Target: "shop.example.com:443", Enabled: true})
	must(t, err)
	exportYAML(t, srv)

	m.Name = "Webshop"
	_, err = db.UpdateMonitor(ctx, m)
	must(t, err)
	if out := exportYAML(t, srv); !strings.Contains(out, "key: shop\n") {
		t.Errorf("a renamed monitor got a new key:\n%s", out)
	}
}
