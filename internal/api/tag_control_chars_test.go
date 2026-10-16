package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A tag value is one line of visible text: a chip, a group heading, and the
// exact string a routing rule, a maintenance window or a tag-selected status
// page matches. Every endpoint that takes one refuses NUL, escape, DEL and
// the C1 controls as it already refused line breaks and tabs, with a 400
// that names its field, and stores nothing.

const noControlTag = `value of tag "env" must not contain line breaks, tabs or other control characters`

// tagControls are JSON string bodies, escaped as a client sends them.
var tagControls = map[string]string{
	"NUL":        `pr\u0000od`,
	"escape":     `pr\u001bod`,
	"DEL":        `pr\u007fod`,
	"C1 control": `pr\u0085od`,
	"line feed":  `pr\nod`,
}

func TestTagValuesRefuseControlCharacters(t *testing.T) {
	ctx := t.Context()
	for label, value := range tagControls {
		t.Run(label, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			prober := &fakeProber{}
			srv.WithProber(prober)
			m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com",
				IntervalS: 60, TimeoutS: 10, Enabled: true, Tags: map[string]string{"env": "prod"}})
			id := itoa(m.ID)

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"web","type":"http","target":"https://example.com","tags":{"env":"`+value+`"}}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", noControlTag)
			rec = patch(t, srv, "/api/v1/monitors/"+id, `{"tags":{"env":"`+value+`"}}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", noControlTag)
			rec = doJSON(t, srv, http.MethodPost, "/api/v1/monitors/preview",
				`{"type":"http","target":"https://example.com","tags":{"env":"`+value+`"}}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", noControlTag)
			if prober.callCount() != 0 {
				t.Errorf("a refused preview probed the target %d times", prober.callCount())
			}

			for path, etag := range map[string]string{"/preview": "", "": staleTagETag} {
				rec = tagRequest(srv, path,
					`{"action":"apply","monitor_ids":[`+id+`],"key":"env","value":"`+value+`"}`, etag)
				wantStatusProblem(t, rec.Code, rec.Body.String(), "value", noControlTag)
				rec = tagRequest(srv, path,
					`{"action":"rename_value","key":"env","value":"prod","new_value":"`+value+`"}`, etag)
				wantStatusProblem(t, rec.Code, rec.Body.String(), "new_value", noControlTag)
			}

			rec = doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules", `{"tag_key":"env","tag_value":"`+value+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)
			mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules",
				`{"tag_key":"env","tag_value":"prod"}`), http.StatusCreated, "seed rule")
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/routing-rules/1", `{"tag_key":"env","tag_value":"`+value+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)

			rec = postMaintenance(t, srv, `{"name":"Deploy","tag_key":"env","tag_value":"`+value+`",`+oneOff+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)

			rec = doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
				`{"slug":"a","title":"A","selection":"tag","tag_key":"env","tag_value":"`+value+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)
			mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
				`{"slug":"b","title":"B","selection":"tag","tag_key":"env","tag_value":"prod"}`), http.StatusCreated, "seed page")
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/b",
				`{"slug":"b","title":"B","selection":"tag","tag_key":"env","tag_value":"`+value+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)

			mons, err := db.ListMonitors(ctx)
			must(t, err)
			if len(mons) != 1 || len(mons[0].Tags) != 1 || mons[0].Tags["env"] != "prod" {
				t.Errorf("monitors = %+v, want the seeded one with env=prod", mons)
			}
			rules, err := db.ListRoutingRules(ctx)
			must(t, err)
			if len(rules) != 1 || rules[0].TagValue != "prod" {
				t.Errorf("routing rules = %+v, want the seeded one", rules)
			}
			if windows, _ := db.ListMaintenance(ctx); len(windows) != 0 {
				t.Errorf("stored %d maintenance windows", len(windows))
			}
			pages, err := db.ListStatusPages(ctx)
			must(t, err)
			if len(pages) != 1 || pages[0].TagValue != "prod" {
				t.Errorf("status pages = %+v, want the seeded one on env=prod", pages)
			}
		})
	}
}

// A zero-width joiner is a format character, not a control: it holds an
// emoji sequence together and stays part of the value.
func TestTagValuesKeepFormatCharacters(t *testing.T) {
	srv, db := testServerWithDB(t)
	const value = "team\u200dops"
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"web","type":"http","target":"https://example.com","tags":{"env":"team\u200dops"}}`)
	mustStatus(t, rec, http.StatusCreated, "a tag value with a zero-width joiner")
	mons, err := db.ListMonitors(t.Context())
	must(t, err)
	if len(mons) != 1 || mons[0].Tags["env"] != value {
		t.Errorf("monitors = %+v, want env=%q", mons, value)
	}
}

// Tags saved before the rule are kept through every other edit and through
// an import of the instance's own export, which sends them back unchanged.
// The rules, windows and pages that name such a value keep naming it, and
// the incident list, which checks every stored window, keeps answering.
func TestTagValuesSavedBeforeTheRuleAreKept(t *testing.T) {
	ctx := t.Context()
	srv, db, m, _, _ := reminderFixture(t)
	const old = "pr\x00od"
	id := itoa(m.ID)

	// Written past the store's checks, as values saved before the rule were.
	_, err := db.Writer.ExecContext(ctx, `INSERT INTO monitor_tags(monitor_id,key,value) VALUES(?,?,?),(?,?,?)`,
		m.ID, "env", old, m.ID, "team", "web")
	must(t, err)
	_, err = db.Writer.ExecContext(ctx, `INSERT INTO routing_rules(tag_key,tag_value,created_at) VALUES(?,?,?)`,
		"env", old, time.Now().Unix())
	must(t, err)
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"acme","title":"Acme","selection":"tag","tag_key":"env","tag_value":"prod"}`), http.StatusCreated, "seed page")
	_, err = db.Writer.ExecContext(ctx, `UPDATE status_pages SET tag_value = ? WHERE slug = 'acme'`, old)
	must(t, err)
	// Active now, so the incident list has to read it, and ending well
	// after any run of this test: the export leaves out a one-off window
	// that has ended. A second window holds a name saved before names were
	// held to one line.
	now := time.Now()
	for _, w := range []store.MaintenanceWindow{
		{Name: "Deploy", TagKey: "env", TagValue: old, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 300)},
		{Name: "Night\nwork", MonitorID: m.ID, StartsAt: now.AddDate(70, 0, 0), EndsAt: now.AddDate(70, 0, 1)},
	} {
		spec, err := json.Marshal(w)
		must(t, err)
		_, err = db.Writer.ExecContext(ctx, `INSERT INTO maintenance_windows(monitor_id,spec) VALUES(?,?)`,
			sqlNullID(w.MonitorID), string(spec))
		must(t, err)
	}

	// The incident list checks every stored window before it answers.
	if rows := readReminderRows(t, srv, "/api/v1/incidents"); len(rows) != 1 {
		t.Errorf("incident list = %d rows, want 1", len(rows))
	}

	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+id, `{"interval_s":120}`), http.StatusOK, "edit the interval")
	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+id, `{"tags":{"env":"pr\u0000od","team":"ops"}}`),
		http.StatusOK, "change another tag and send the old value back")
	mustStatus(t, doJSON(t, srv, http.MethodPut, "/api/v1/routing-rules/1", `{"tag_key":"env","tag_value":"pr\u0000od"}`),
		http.StatusOK, "save the rule with its own value")
	mustStatus(t, doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme status","selection":"tag","tag_key":"env","tag_value":"pr\u0000od"}`),
		http.StatusOK, "retitle the page and send its tag back")
	// A bulk operation on another key leaves the old value where it is.
	apply := `{"action":"apply","monitor_ids":[` + id + `],"key":"tier","value":"gold"}`
	preview := previewTags(t, srv, apply)
	mustStatus(t, tagRequest(srv, "", apply, preview.Header().Get("ETag")), http.StatusOK, "apply a tag on another key")

	code, rep, body := importYAML(t, srv, exportYAML(t, srv), false)
	if code != http.StatusOK {
		t.Fatalf("import of the instance's own export = %d: %s", code, body)
	}
	if len(rep.RoutingRules) != 1 || rep.RoutingRules[0].Action != actionUnchanged {
		t.Errorf("routing rule report = %+v, want the rule unchanged", rep.RoutingRules)
	}
	if len(rep.Maintenance) != 2 || rep.Maintenance[0].Action != actionUnchanged || rep.Maintenance[1].Action != actionUnchanged {
		t.Errorf("maintenance report = %+v, want both windows unchanged", rep.Maintenance)
	}
	if len(rep.StatusPages) != 1 || rep.StatusPages[0].Action != actionUnchanged {
		t.Errorf("status page report = %+v, want the page unchanged", rep.StatusPages)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	must(t, err)
	if got.Tags["env"] != old || got.Tags["team"] != "ops" || got.Tags["tier"] != "gold" || got.IntervalS != 120 {
		t.Errorf("monitor tags %q interval %d, want env kept, team=ops, tier=gold, 120", got.Tags, got.IntervalS)
	}
	if rules, _ := db.ListRoutingRules(ctx); len(rules) != 1 || rules[0].TagValue != old {
		t.Errorf("routing rules = %+v, want the old value kept", rules)
	}
	if windows, _ := db.ListMaintenance(ctx); len(windows) != 2 {
		t.Errorf("stored %d windows, want the two", len(windows))
	}
	page, err := db.GetStatusPageBySlug(ctx, "acme")
	must(t, err)
	if page.TagValue != old || page.Title != "Acme status" {
		t.Errorf("page = %q on %q, want the new title and the old value", page.Title, page.TagValue)
	}
	if rows := readReminderRows(t, srv, "/api/v1/incidents"); len(rows) != 1 {
		t.Errorf("incident list after the import = %d rows, want 1", len(rows))
	}

	// The old value on a window, a rule or a page that does not have it yet
	// is still refused.
	for field, doc := range map[string]string{
		"maintenance[0].tag_value": "version: 1\nmaintenance:\n  - name: Other\n    tag_key: env\n    tag_value: \"pr\\0od\"\n" +
			"    starts_at: 2026-11-01T10:00:00Z\n    ends_at: 2026-11-01T11:00:00Z\n",
		"routing_rules[0].tag_value": "version: 1\nrouting_rules:\n  - tag_key: stage\n    tag_value: \"pr\\0od\"\n",
		"status_pages[0].tag_value": "version: 1\nstatus_pages:\n  - slug: other\n    title: Other\n    selection: tag\n" +
			"    tag_key: env\n    tag_value: \"pr\\0od\"\n",
	} {
		code, _, body := importYAML(t, srv, doc, false)
		wantStatusProblem(t, code, body, field, "must not contain line breaks, tabs or other control characters")
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"copy","title":"Copy","selection":"tag","tag_key":"env","tag_value":"pr\u0000od"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)
	rec = postMaintenance(t, srv, `{"name":"Other","tag_key":"env","tag_value":"pr\u0000od",`+oneOff+`}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "tag_value", noControlTag)
	if windows, _ := db.ListMaintenance(ctx); len(windows) != 2 {
		t.Errorf("stored %d windows, want the two", len(windows))
	}
}

// The exemption belongs to the value an object has: a different invalid
// value is refused, and so is the old value on an object that does not have
// it. Renaming the old value away, which only names it, is allowed.
func TestTagValueExemptionBelongsToTheStoredValue(t *testing.T) {
	ctx := t.Context()
	srv, db := testServerWithDB(t)
	const old = "pr\x00od"
	a := seedMonitor(t, db, store.Monitor{Name: "a", Type: "http", Target: "https://a.example.com", IntervalS: 60, TimeoutS: 10})
	b := seedMonitor(t, db, store.Monitor{Name: "b", Type: "http", Target: "https://b.example.com", IntervalS: 60, TimeoutS: 10})
	_, err := db.Writer.ExecContext(ctx, `INSERT INTO monitor_tags(monitor_id,key,value) VALUES(?,?,?)`, a.ID, "env", old)
	must(t, err)

	rec := patch(t, srv, "/api/v1/monitors/"+itoa(a.ID), `{"tags":{"env":"pr\u001bod"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", noControlTag)
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(a.ID), `{"tags":{"stage":"pr\u0000od"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", `value of tag "stage" must not contain`)
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(b.ID), `{"tags":{"env":"pr\u0000od"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "tags", noControlTag)
	rec = tagRequest(srv, "/preview", `{"action":"apply","monitor_ids":[`+itoa(b.ID)+`],"key":"env","value":"pr\u0000od"}`, "")
	wantStatusProblem(t, rec.Code, rec.Body.String(), "value", noControlTag)
	// Renaming a key moves the old value with it; that writes nothing new.
	rec = tagRequest(srv, "/preview", `{"action":"rename_key","key":"env","new_key":"stage"}`, "")
	mustStatus(t, rec, http.StatusOK, "preview moving the old value to another key")

	req := `{"action":"rename_value","key":"env","value":"pr\u0000od","new_value":"prod"}`
	preview := previewTags(t, srv, req)
	mustStatus(t, tagRequest(srv, "", req, preview.Header().Get("ETag")), http.StatusOK, "rename the old value away")
	if got, err := db.GetMonitor(ctx, a.ID); err != nil || got.Tags["env"] != "prod" {
		t.Errorf("monitor a tags = %q (%v), want env=prod", got.Tags, err)
	}
	if got, err := db.GetMonitor(ctx, b.ID); err != nil || len(got.Tags) != 0 {
		t.Errorf("monitor b tags = %q (%v), want none", got.Tags, err)
	}
}

// A file that creates a rule, a window or a monitor with such a value is
// refused at the place that holds it, and writes nothing.
func TestImportRefusesAControlCharacterInATagValueAtItsPath(t *testing.T) {
	cases := map[string]struct{ doc, field string }{
		"monitor": {"version: 1\nmonitors:\n  - key: a\n    name: A\n    type: tcp\n    target: a.example.com:1\n" +
			"    tags:\n      env: \"pr\\0od\"\n", "monitors[0].tags"},
		"routing rule": {"version: 1\nrouting_rules:\n  - tag_key: env\n    tag_value: \"pr\\eod\"\n", "routing_rules[0].tag_value"},
		"maintenance": {"version: 1\nmaintenance:\n  - name: Deploy\n    tag_key: env\n    tag_value: \"pr\\0od\"\n" +
			"    starts_at: 2026-11-01T10:00:00Z\n    ends_at: 2026-11-01T11:00:00Z\n", "maintenance[0].tag_value"},
		"status page": {"version: 1\nstatus_pages:\n  - slug: a\n    title: A\n    selection: tag\n    tag_key: env\n" +
			"    tag_value: \"pr\\eod\"\n", "status_pages[0].tag_value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			for _, dryRun := range []bool{true, false} {
				code, _, body := importYAML(t, srv, tc.doc, dryRun)
				wantStatusProblem(t, code, body, tc.field, noControlTag)
			}
			ctx := t.Context()
			mons, _ := db.ListMonitors(ctx)
			rules, _ := db.ListRoutingRules(ctx)
			windows, _ := db.ListMaintenance(ctx)
			pages, _ := db.ListStatusPages(ctx)
			if n := len(mons) + len(rules) + len(windows) + len(pages); n != 0 {
				t.Errorf("a refused file wrote %d objects", n)
			}
		})
	}
}

// staleTagETag is an If-Match no preview produces, for a request that must
// be refused before the snapshot is compared.
var staleTagETag = `"tags-` + strings.Repeat("0", 64) + `"`

// sqlNullID stores 0 as NULL, as a window on a tag pair has no monitor.
func sqlNullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
