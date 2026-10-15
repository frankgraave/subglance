package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// A name is one line of text: a monitor's is the title of every alert about
// it, and a channel's, a token's and a maintenance window's are each one row
// in a list. Each refuses a line break, a tab and any other control
// character with a 400 that names the field, and stores nothing.

const noControl = "name must not contain line breaks, tabs or other control characters"

// controlNames are JSON string bodies, escaped as a client sends them.
var controlNames = map[string]string{
	"line feed":       `API\nprod`,
	"carriage return": `API\rprod`,
	"tab":             `API\tprod`,
	"NUL":             `API\u0000prod`,
	"escape":          `API\u001bprod`,
	"DEL":             `API\u007fprod`,
	"C1 control":      `API\u0085prod`,
}

func TestNamesRefuseControlCharacters(t *testing.T) {
	ctx := context.Background()
	for label, name := range controlNames {
		t.Run(label, func(t *testing.T) {
			srv, db := testServerWithDB(t)

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"`+name+`","type":"http","target":"https://example.com"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)

			m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
				IntervalS: 60, TimeoutS: 10, Enabled: true})
			rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"`+name+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)
			if got, err := db.GetMonitor(ctx, m.ID); err != nil || got.Name != "API" {
				t.Errorf("a refused rename stored %q (%v)", got.Name, err)
			}

			channel := `{"name":"` + name + `","type":"webhook","config":{"url":"https://hook.example.com/x"}}`
			rec = doJSON(t, srv, http.MethodPost, "/api/v1/channels", channel)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)
			ch := seedChannel(t, db, "webhook", map[string]string{"url": "https://hook.example.com/x"})
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+itoa(ch.ID), channel)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)
			if got, err := db.GetChannel(ctx, ch.ID); err != nil || got.Name != "Ops" {
				t.Errorf("a refused channel update stored %q (%v)", got.Name, err)
			}

			admin, err := db.GetUserByEmail(ctx, templateAdmin)
			must(t, err)
			before, err := db.ListAPITokens(ctx, admin.ID)
			must(t, err)
			rec = doJSON(t, srv, http.MethodPost, "/api/v1/tokens", `{"name":"`+name+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)
			if after, _ := db.ListAPITokens(ctx, admin.ID); len(after) != len(before) {
				t.Errorf("a refused token was stored: %d tokens, was %d", len(after), len(before))
			}

			rec = postMaintenance(t, srv, `{"name":"`+name+`","monitor_id":`+itoa(m.ID)+`,`+oneOff+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)

			if mons, _ := db.ListMonitors(ctx); len(mons) != 1 {
				t.Errorf("stored %d monitors, want the seeded one", len(mons))
			}
			if chans, _ := db.ListChannels(ctx); len(chans) != 1 {
				t.Errorf("stored %d channels, want the seeded one", len(chans))
			}
			if windows, _ := db.ListMaintenance(ctx); len(windows) != 0 {
				t.Errorf("stored %d maintenance windows", len(windows))
			}
		})
	}
}

// Format characters are not control characters: a zero-width joiner holds
// an emoji sequence together and a direction mark keeps a right-to-left
// name readable.
func TestNamesKeepFormatCharacters(t *testing.T) {
	srv, db := testServerWithDB(t)
	for _, name := range []string{"Ops \u200d team", "\u05d0\u05d1\u200f API", "Caf\u00e9 \u00a0 Z\u00fcrich"} {
		body, err := json.Marshal(map[string]string{"name": name, "type": "http", "target": "https://example.com"})
		must(t, err)
		mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/monitors", string(body)), http.StatusCreated, "create "+name)
	}
	if mons, _ := db.ListMonitors(context.Background()); len(mons) != 3 {
		t.Errorf("stored %d monitors, want 3", len(mons))
	}
}

// A name saved before the rule existed is kept through every other edit and
// through an import of the instance's own export, which sends it back
// unchanged. Only a new name is held to the rule.
func TestANameSavedBeforeTheRuleIsKept(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)
	const old = "API\nprod"
	m := seedMonitor(t, db, store.Monitor{Name: old, Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"interval_s":120}`), http.StatusOK, "edit the interval")
	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"API\nprod"}`), http.StatusOK, "send the same name")

	ch, err := db.CreateChannel(ctx, store.Channel{Name: old, Type: "webhook", Enabled: true,
		Config: map[string]string{"url": "https://hook.example.com/x"}})
	must(t, err)
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+itoa(ch.ID),
		`{"name":"API\nprod","type":"webhook","config":{"url":"https://hook.example.com/y"}}`)
	mustStatus(t, rec, http.StatusOK, "edit a channel and send its name back")

	// Stored as the store keeps a window, past the validation a create
	// runs, as one saved before the rule was. It ends long after any run
	// of this test: the export leaves out a one-off window that has ended.
	spec, err := json.Marshal(store.MaintenanceWindow{Name: old, MonitorID: m.ID,
		StartsAt: time.Date(2099, 10, 1, 10, 0, 0, 0, time.UTC), EndsAt: time.Date(2099, 10, 1, 11, 0, 0, 0, time.UTC)})
	must(t, err)
	_, err = db.Writer.ExecContext(ctx, `INSERT INTO maintenance_windows(monitor_id,spec) VALUES(?,?)`, m.ID, string(spec))
	must(t, err)

	file := exportYAML(t, srv)
	code, rep, body := importYAML(t, srv, file, false)
	if code != http.StatusOK {
		t.Fatalf("import of the instance's own export = %d: %s", code, body)
	}
	if len(rep.Maintenance) != 1 || rep.Maintenance[0].Action != actionUnchanged {
		t.Errorf("maintenance report = %+v, want the window unchanged", rep.Maintenance)
	}
	if got, err := db.GetMonitor(ctx, m.ID); err != nil || got.Name != old || got.IntervalS != 120 {
		t.Errorf("monitor = %q interval %d (%v)", got.Name, got.IntervalS, err)
	}
	if got, err := db.GetChannel(ctx, ch.ID); err != nil || got.Name != old {
		t.Errorf("channel = %q (%v)", got.Name, err)
	}
	if windows, _ := db.ListMaintenance(ctx); len(windows) != 1 {
		t.Errorf("stored %d windows, want the one", len(windows))
	}

	// A new window with that name is still refused.
	parsed, err := configfile.Parse([]byte(file))
	must(t, err)
	doc := "version: 1\nmaintenance:\n  - name: \"API\\nprod\"\n    monitor: " + parsed.Monitors[0].Key +
		"\n    starts_at: 2026-11-01T10:00:00Z\n    ends_at: 2026-11-01T11:00:00Z\n"
	code, _, body = importYAML(t, srv, doc, true)
	wantStatusProblem(t, code, body, "maintenance[0].name", noControl)
}

// An import refuses such a name at the entry that holds it, and writes
// nothing.
func TestImportRefusesAControlCharacterInANameAtItsEntry(t *testing.T) {
	cases := map[string]struct{ doc, field string }{
		"monitor": {"version: 1\nmonitors:\n  - key: a\n    name: \"API\\tprod\"\n" +
			"    type: tcp\n    target: a.example.com:1\n", "monitors[0].name"},
		"channel": {"version: 1\nchannels:\n  - key: c\n    name: \"Ops\\0\"\n" +
			"    type: webhook\n    config:\n      url: https://hook.example.com/x\n", "channels[0].name"},
		"maintenance": {"version: 1\nmaintenance:\n  - name: \"Deploy\\nnight\"\n    tag_key: env\n    tag_value: prod\n" +
			"    starts_at: 2026-11-01T10:00:00Z\n    ends_at: 2026-11-01T11:00:00Z\n", "maintenance[0].name"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, tc.doc, false)
			wantStatusProblem(t, code, body, tc.field, noControl)
			ctx := context.Background()
			mons, _ := db.ListMonitors(ctx)
			chans, _ := db.ListChannels(ctx)
			windows, _ := db.ListMaintenance(ctx)
			if len(mons)+len(chans)+len(windows) != 0 {
				t.Errorf("a refused file wrote %d monitors, %d channels, %d windows", len(mons), len(chans), len(windows))
			}
		})
	}
}

// An old name is not a licence to rename the object to a different invalid
// name or create another object with that name under a new configuration key.
func TestLegacyNameExemptionBelongsToTheUnchangedObject(t *testing.T) {
	ctx := t.Context()
	srv, db := testServerWithDB(t)
	const old = "API\nprod"
	m := seedMonitor(t, db, store.Monitor{Name: old, Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	ch, err := db.CreateChannel(ctx, store.Channel{Name: old, Type: "webhook", Enabled: true,
		Config: map[string]string{"url": "https://hook.example.com/x"}})
	must(t, err)

	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"Other\nname"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+itoa(ch.ID),
		`{"name":"Other\nname","type":"webhook","config":{"url":"https://hook.example.com/x"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "name", noControl)

	exported, err := configfile.Parse([]byte(exportYAML(t, srv)))
	must(t, err)
	for _, create := range []bool{false, true} {
		mon, channel := exported.Monitors[0], exported.Channels[0]
		if create {
			// Same name, different objects: no legacy exemption.
			mon.Key, channel.Key = "another-monitor", "another-channel"
		} else {
			// Same objects, different names: no legacy exemption either.
			mon.Name, channel.Name = "Other\nname", "Other\nname"
		}
		for field, doc := range map[string]configfile.Document{
			"monitors[0].name": {Version: 1, Monitors: []configfile.Monitor{mon}},
			"channels[0].name": {Version: 1, Channels: []configfile.Channel{channel}},
		} {
			file, err := configfile.Marshal(doc)
			must(t, err)
			for _, dryRun := range []bool{false, true} {
				code, _, body := importYAML(t, srv, string(file), dryRun)
				wantStatusProblem(t, code, body, field, noControl)
			}
		}
	}
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	chans, err := db.ListChannels(ctx)
	must(t, err)
	if len(mons) != 1 || mons[0].Name != old || len(chans) != 1 || chans[0].Name != old {
		t.Errorf("refused renames or creates changed the stored objects: monitors=%+v channels=%+v", mons, chans)
	}
}
