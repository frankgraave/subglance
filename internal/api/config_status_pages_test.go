package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// seedPages builds an instance with three monitors and two status pages: a
// published tag page with every setting changed from its default, and a
// draft that lists two monitors in an order other than their ids.
func seedPages(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for _, name := range []string{"Shop", "API", "Mail relay"} {
		m, err := db.CreateMonitor(ctx, store.Monitor{Name: name, Type: "tcp",
			Target: strings.ToLower(strings.ReplaceAll(name, " ", "-")) + ".example.com:443", Enabled: true,
			Tags: map[string]string{"public": "yes"}})
		must(t, err)
		ids = append(ids, m.ID)
	}
	tagged, err := db.CreateStatusPage(ctx, store.StatusPage{Slug: "acme", Title: "Acme services",
		Description: "Everything customers use.", Timezone: "Europe/Amsterdam", Language: "nl",
		Accent: "#3b82f6", HideCredit: true, Indexable: true, Enabled: true,
		Selection: store.StatusPageSelectTag, TagKey: "public", TagValue: "yes"})
	must(t, err)
	_, err = db.SetStatusPageEntries(ctx, tagged.ID, []store.StatusPageEntryInput{
		{MonitorID: ids[0], DisplayName: "Webshop"}, {MonitorID: ids[1], DisplayName: "Public API"}})
	must(t, err)
	draft, err := db.CreateStatusPage(ctx, store.StatusPage{Slug: "internal", Title: "Internal",
		Selection: store.StatusPageSelectMonitors})
	must(t, err)
	_, err = db.SetStatusPageEntries(ctx, draft.ID, []store.StatusPageEntryInput{
		{MonitorID: ids[2], DisplayName: "Mail"}, {MonitorID: ids[0], DisplayName: "Shop"}})
	must(t, err)
}

// pageSummary is a page as a visitor and its administrator would compare it:
// every setting and the entries in order, without ids or public keys.
func pageSummary(t *testing.T, db *store.DB, slug string) string {
	t.Helper()
	ctx := context.Background()
	p, err := db.GetStatusPageBySlug(ctx, slug)
	must(t, err)
	entries, err := db.ListStatusPageEntries(ctx, p.ID)
	must(t, err)
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	names := map[int64]string{}
	for _, m := range mons {
		names[m.ID] = m.Name
	}
	var b strings.Builder
	b.WriteString(strings.Join([]string{p.Slug, p.Title, p.Description, p.Timezone, p.Language, p.Accent,
		p.Selection, p.TagKey, p.TagValue}, "|"))
	for _, f := range []bool{p.HideCredit, p.Indexable, p.Enabled} {
		if f {
			b.WriteString("|1")
		} else {
			b.WriteString("|0")
		}
	}
	for _, e := range entries {
		b.WriteString("|" + names[e.MonitorID] + "=" + e.DisplayName)
	}
	return b.String()
}

func TestStatusPagesRoundTrip(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	seedPages(t, srcDB)
	exported := exportYAML(t, src)
	for _, want := range []string{"status_pages:", "slug: acme", "accent: '#3b82f6'", "language: nl",
		"selection: tag", "monitor: mail-relay", "name: Public API"} {
		if !strings.Contains(exported, want) {
			t.Errorf("export lacks %q:\n%s", want, exported)
		}
	}

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	// 3 monitors and 2 pages.
	if rep.Summary.Create != 5 || len(rep.StatusPages) != 2 || rep.StatusPages[0].Key != "acme" ||
		rep.StatusPages[0].Action != actionCreate {
		t.Errorf("report = %+v / %+v, want 5 creates with acme first", rep.Summary, rep.StatusPages)
	}
	for _, slug := range []string{"acme", "internal"} {
		if got, want := pageSummary(t, dstDB, slug), pageSummary(t, srcDB, slug); got != want {
			t.Errorf("page %s after import:\n got %s\nwant %s", slug, got, want)
		}
	}
	if again := exportYAML(t, dst); again != exported {
		t.Errorf("the imported instance exports differently.\nsource:\n%s\ndestination:\n%s", exported, again)
	}

	code, rep, body = importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("second import = %d: %s", code, body)
	}
	if rep.Summary.Create != 0 || rep.Summary.Update != 0 {
		t.Errorf("second import summary = %+v, want everything unchanged\n%s", rep.Summary, body)
	}
	pages, err := dstDB.ListStatusPages(context.Background())
	must(t, err)
	if len(pages) != 2 {
		t.Errorf("pages after two imports = %d, want 2", len(pages))
	}
}

func TestImportStatusPageOmittedFieldsKeepCurrentValues(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedPages(t, db)
	exportYAML(t, srv) // hands out the monitor keys
	before := pageSummary(t, db, "acme")

	code, rep, body := importYAML(t, srv, "version: 1\nstatus_pages:\n  - slug: ACME\n    title: Acme status\n", false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if it := rep.StatusPages[0]; it.Action != actionUpdate || strings.Join(it.Changes, ",") != "title" {
		t.Errorf("item = %+v, want an update of title only", it)
	}
	if got, want := pageSummary(t, db, "acme"), strings.Replace(before, "Acme services", "Acme status", 1); got != want {
		t.Errorf("page after a title-only import:\n got %s\nwant %s", got, want)
	}

	code, rep, body = importYAML(t, srv, "version: 1\nstatus_pages:\n  - slug: internal\n    monitors: []\n", false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if it := rep.StatusPages[0]; strings.Join(it.Changes, ",") != "monitors" {
		t.Errorf("item = %+v, want a change of monitors", it)
	}
	p, err := db.GetStatusPageBySlug(context.Background(), "internal")
	must(t, err)
	entries, err := db.ListStatusPageEntries(context.Background(), p.ID)
	must(t, err)
	if len(entries) != 0 {
		t.Errorf("monitors: [] left %d entries", len(entries))
	}
}

func TestImportRejectsInvalidStatusPagesAndWritesNothing(t *testing.T) {
	head := "version: 1\nmonitors:\n  - key: shop\n    name: Shop\n    type: tcp\n    target: shop.example.com:443\nstatus_pages:\n"
	cases := []struct {
		name, page, field string
	}{
		{"no slug", "  - title: A\n", "status_pages[0].slug"},
		{"slug twice", "  - slug: a\n    title: A\n  - slug: A\n    title: B\n", "status_pages[1].slug"},
		{"bad slug", "  - slug: a_b\n    title: A\n", "status_pages[0].slug"},
		{"reserved slug", "  - slug: api\n    title: A\n", "status_pages[0].slug"},
		{"no title", "  - slug: a\n", "status_pages[0].title"},
		{"bad timezone", "  - slug: a\n    title: A\n    timezone: Mars/Olympus\n", "status_pages[0].timezone"},
		{"bad language", "  - slug: a\n    title: A\n    language: fr\n", "status_pages[0].language"},
		{"unreadable accent", "  - slug: a\n    title: A\n    accent: '#ffff00'\n", "status_pages[0].accent"},
		{"tag without selection", "  - slug: a\n    title: A\n    tag_key: env\n    tag_value: prod\n", "status_pages[0].selection"},
		{"tag page without tag", "  - slug: a\n    title: A\n    selection: tag\n", "status_pages[0].tag_key"},
		{"unknown monitor", "  - slug: a\n    title: A\n    monitors:\n      - monitor: nope\n        name: N\n", "status_pages[0].monitors[0].monitor"},
		{"monitor twice", "  - slug: a\n    title: A\n    monitors:\n      - monitor: shop\n        name: A\n      - monitor: shop\n        name: B\n", "status_pages[0].monitors[1].monitor"},
		{"empty name", "  - slug: a\n    title: A\n    monitors:\n      - monitor: shop\n        name: \" \"\n", "status_pages[0].monitors[0].name"},
		{"unknown field", "  - slug: a\n    title: A\n    logo: x.png\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, head+tc.page, false)
			if code != http.StatusBadRequest {
				t.Fatalf("import = %d %s, want 400", code, body)
			}
			if tc.field != "" && !strings.Contains(body, `"field":"`+tc.field+`"`) {
				t.Errorf("refusal = %s, want field %s", body, tc.field)
			}
			ctx := context.Background()
			pages, err := db.ListStatusPages(ctx)
			must(t, err)
			mons, err := db.ListMonitors(ctx)
			must(t, err)
			if len(pages) != 0 || len(mons) != 0 {
				t.Errorf("a refused file wrote %d pages and %d monitors", len(pages), len(mons))
			}
		})
	}
}

// TestStatusPagesInAFileAreForAdministrators keeps the file from being a way
// around the status page API's admin-only rule, in either direction.
func TestStatusPagesInAFileAreForAdministrators(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedPages(t, db)
	editor := seedUser(t, srv, db, "editor@example.com", store.RoleEditor)
	asEditor := func(req *http.Request) *httptest.ResponseRecorder {
		req.Header.Set("Authorization", "Bearer "+editor)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec := asEditor(httptest.NewRequest(http.MethodGet, "/api/v1/config/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("editor export = %d: %s", rec.Code, rec.Body)
	}
	if out := rec.Body.String(); strings.Contains(out, "status_pages") || !strings.Contains(out, "key: shop") {
		t.Errorf("an editor's export carries status pages, or lost its monitors:\n%s", out)
	}

	doc := "version: 1\nmonitors:\n  - key: new\n    name: New\n    type: tcp\n    target: new.example.com:443\n" +
		"status_pages:\n  - slug: fresh\n    title: Fresh\n"
	rec = asEditor(httptest.NewRequest(http.MethodPost, "/api/v1/config/import", strings.NewReader(doc)))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"field":"status_pages"`) {
		t.Errorf("editor import of status pages = %d %s, want 403 at status_pages", rec.Code, rec.Body)
	}
	ctx := context.Background()
	if _, err := db.GetStatusPageBySlug(ctx, "fresh"); err == nil {
		t.Error("an editor's import created a status page")
	}
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	if len(mons) != 3 {
		t.Errorf("a refused import left %d monitors, want the 3 seeded", len(mons))
	}
}

// TestImportedPageChangeShowsAtOnce: the public page caches what it renders,
// and an import that changes a page must empty that cache as the API does.
func TestImportedPageChangeShowsAtOnce(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedPages(t, db)
	exportYAML(t, srv)
	public := func() string {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status-pages/acme", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("public page = %d: %s", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if !strings.Contains(public(), "Acme services") {
		t.Fatal("the seeded page does not show its title")
	}
	if code, _, body := importYAML(t, srv, "version: 1\nstatus_pages:\n  - slug: acme\n    title: Acme status\n", false); code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if got := public(); !strings.Contains(got, "Acme status") {
		t.Errorf("the public page still shows the title from before the import:\n%s", got)
	}
}

// TestImportedPageStaysADraftWhenItsMonitorsFail: a published page is
// created switched off and switched on only once its monitors are on it, so
// an import that stops in between publishes no empty page.
func TestImportedPageStaysADraftWhenItsMonitorsFail(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "Shop", Type: "tcp", Target: "shop.example.com:443", Enabled: true})
	must(t, err)
	must(t, db.SetMonitorConfigKey(ctx, m.ID, "shop"))
	doc, err := configfile.Parse([]byte("version: 1\nstatus_pages:\n  - slug: acme\n    title: Acme\n    enabled: true\n" +
		"    monitors:\n      - monitor: shop\n        name: Webshop\n"))
	must(t, err)
	plan, err := srv.planImport(ctx, doc)
	must(t, err)

	// The monitor goes away between planning and writing.
	must(t, db.DeleteMonitor(ctx, m.ID))
	if err := srv.applyImport(ctx, &plan, func(string) string { return "" }); err == nil {
		t.Fatal("applyImport succeeded although the page's monitor was gone")
	}
	p, err := db.GetStatusPageBySlug(ctx, "acme")
	must(t, err)
	if p.Enabled {
		t.Error("a page whose monitors could not be set was published")
	}
}
