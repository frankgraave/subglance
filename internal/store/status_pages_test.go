package store

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func seedStatusPage(t *testing.T, db *DB, p StatusPage) StatusPage {
	t.Helper()
	got, err := db.CreateStatusPage(t.Context(), p)
	if err != nil {
		t.Fatalf("CreateStatusPage: %v", err)
	}
	return got
}

func TestCreateStatusPageNormalisesAndDefaultsToOffAndUTC(t *testing.T) {
	db := openTestDB(t)
	p := seedStatusPage(t, db, StatusPage{Slug: "  Acme  ", Title: " Acme services ", Selection: StatusPageSelectMonitors})

	if p.Slug != "acme" || p.Title != "Acme services" {
		t.Errorf("slug, title = %q, %q; want trimmed and lowercased slug", p.Slug, p.Title)
	}
	if p.Timezone != "UTC" {
		t.Errorf("timezone = %q, want UTC", p.Timezone)
	}
	// A draft must publish nothing and must not ask to be indexed.
	if p.Enabled || p.Indexable {
		t.Errorf("enabled, indexable = %v, %v; want both off by default", p.Enabled, p.Indexable)
	}
	if p.ID == 0 || p.CreatedAt.IsZero() {
		t.Errorf("stored page lacks id or created_at: %+v", p)
	}
}

func TestStatusPageValidation(t *testing.T) {
	ok := StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors}
	cases := map[string]func(*StatusPage){
		"empty slug":          func(p *StatusPage) { p.Slug = "" },
		"slug with space":     func(p *StatusPage) { p.Slug = "my page" },
		"slug leading dash":   func(p *StatusPage) { p.Slug = "-status" },
		"slug too long":       func(p *StatusPage) { p.Slug = strings.Repeat("a", 64) },
		"reserved api":        func(p *StatusPage) { p.Slug = "api" },
		"reserved assets":     func(p *StatusPage) { p.Slug = "Assets" },
		"empty title":         func(p *StatusPage) { p.Title = "  " },
		"long title":          func(p *StatusPage) { p.Title = strings.Repeat("t", 121) },
		"title line break":    func(p *StatusPage) { p.Title = "a\nb" },
		"long description":    func(p *StatusPage) { p.Description = strings.Repeat("d", 501) },
		"unknown zone":        func(p *StatusPage) { p.Timezone = "Mars/Olympus" },
		"local zone":          func(p *StatusPage) { p.Timezone = "Local" },
		"unknown selection":   func(p *StatusPage) { p.Selection = "all" },
		"monitors with tag":   func(p *StatusPage) { p.TagKey, p.TagValue = "env", "prod" },
		"tag without a tag":   func(p *StatusPage) { p.Selection = StatusPageSelectTag },
		"tag without a value": func(p *StatusPage) { p.Selection, p.TagKey = StatusPageSelectTag, "env" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := ok
			mutate(&p)
			if _, err := NormaliseStatusPage(p); !errors.Is(err, ErrInvalidStatusPage) {
				t.Fatalf("NormaliseStatusPage = %v, want ErrInvalidStatusPage", err)
			}
		})
	}
	if _, err := NormaliseStatusPage(ok); err != nil {
		t.Fatalf("the valid base case is refused: %v", err)
	}
	// Exactly at the limits is allowed.
	edge := ok
	edge.Slug, edge.Title, edge.Description = strings.Repeat("a", 63), strings.Repeat("t", 120), strings.Repeat("d", 500)
	if _, err := NormaliseStatusPage(edge); err != nil {
		t.Fatalf("values at the limits are refused: %v", err)
	}
}

func TestStatusPageTagIsNormalisedLikeAMonitorTag(t *testing.T) {
	db := openTestDB(t)
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectTag, TagKey: " Customer ", TagValue: " Acme "})
	if p.TagKey != "customer" || p.TagValue != "Acme" {
		t.Errorf("tag = %q=%q, want customer=Acme", p.TagKey, p.TagValue)
	}
}

// The slug column compares without case, so a second page cannot claim the
// same URL in different capitals.
func TestStatusPageSlugIsUniqueWithoutRegardToCase(t *testing.T) {
	db := openTestDB(t)
	seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "One", Selection: StatusPageSelectMonitors})
	_, err := db.CreateStatusPage(t.Context(), StatusPage{Slug: "ACME", Title: "Two", Selection: StatusPageSelectMonitors})
	if !errors.Is(err, ErrStatusPageSlugTaken) {
		t.Fatalf("second page with the same slug: err = %v, want ErrStatusPageSlugTaken", err)
	}

	other := seedStatusPage(t, db, StatusPage{Slug: "other", Title: "Other", Selection: StatusPageSelectMonitors})
	other.Slug = "acme"
	if _, err := db.UpdateStatusPage(t.Context(), other); !errors.Is(err, ErrStatusPageSlugTaken) {
		t.Fatalf("rename onto a taken slug: err = %v, want ErrStatusPageSlugTaken", err)
	}

	got, err := db.GetStatusPageBySlug(t.Context(), "AcMe")
	if err != nil || got.Title != "One" {
		t.Fatalf("GetStatusPageBySlug(AcMe) = %+v, %v; want the page titled One", got, err)
	}
}

// The schema enforces the selection rule itself, so a write that skips the Go
// validation still cannot store a tag page without a tag.
func TestStatusPageSchemaRejectsInconsistentSelection(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Writer.ExecContext(t.Context(), `
		INSERT INTO status_pages (slug, title, selection, created_at, updated_at)
		VALUES ('x', 'X', 'tag', 0, 0)`)
	if err == nil {
		t.Fatal("a tag page without a tag was stored")
	}
	_, err = db.Writer.ExecContext(t.Context(), `
		INSERT INTO status_pages (slug, title, selection, tag_key, tag_value, created_at, updated_at)
		VALUES ('y', 'Y', 'monitors', 'env', 'prod', 0, 0)`)
	if err == nil {
		t.Fatal("a monitors page with a tag was stored")
	}
}

func TestUpdateAndDeleteStatusPage(t *testing.T) {
	db := openTestDB(t)
	p := seedStatusPage(t, db, StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors})
	p.Title, p.Enabled, p.Indexable, p.Timezone = "Service status", true, true, "Europe/Amsterdam"
	got, err := db.UpdateStatusPage(t.Context(), p)
	if err != nil {
		t.Fatalf("UpdateStatusPage: %v", err)
	}
	if got.Title != "Service status" || !got.Enabled || !got.Indexable || got.Timezone != "Europe/Amsterdam" {
		t.Errorf("update not stored: %+v", got)
	}

	if _, err := db.UpdateStatusPage(t.Context(), StatusPage{ID: 999, Slug: "nope", Title: "N", Selection: StatusPageSelectMonitors}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of a missing page: err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteStatusPage(t.Context(), p.ID); err != nil {
		t.Fatalf("DeleteStatusPage: %v", err)
	}
	if err := db.DeleteStatusPage(t.Context(), p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetStatusPageBySlug(t.Context(), "status"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted page still found by slug: %v", err)
	}
}

var statusPageKeyShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestSetStatusPageEntriesOrdersAndKeysEntries(t *testing.T) {
	db := openTestDB(t)
	a := seedTaggedMonitor(t, db, "internal-a", nil)
	b := seedTaggedMonitor(t, db, "internal-b", nil)
	p := seedStatusPage(t, db, StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors})

	got, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: b.ID, DisplayName: " Website "},
		{MonitorID: a.ID, DisplayName: "API"},
	})
	if err != nil {
		t.Fatalf("SetStatusPageEntries: %v", err)
	}
	if len(got) != 2 || got[0].MonitorID != b.ID || got[1].MonitorID != a.ID {
		t.Fatalf("entries = %+v, want b then a", got)
	}
	if got[0].DisplayName != "Website" || got[0].Position != 0 || got[1].Position != 1 {
		t.Errorf("names or positions wrong: %+v", got)
	}
	for _, e := range got {
		if !statusPageKeyShape.MatchString(e.PublicKey) {
			t.Errorf("public key %q is not 16 hex characters", e.PublicKey)
		}
	}
	if got[0].PublicKey == got[1].PublicKey {
		t.Error("two entries share a public key")
	}
}

// A monitor that stays on the page keeps its key through a rename and a
// reorder; one that is removed and added back gets a fresh key.
func TestSetStatusPageEntriesKeepsKeysOfMonitorsThatStay(t *testing.T) {
	db := openTestDB(t)
	a := seedTaggedMonitor(t, db, "a", nil)
	b := seedTaggedMonitor(t, db, "b", nil)
	p := seedStatusPage(t, db, StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors})

	first, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: a.ID, DisplayName: "A"}, {MonitorID: b.ID, DisplayName: "B"},
	})
	if err != nil {
		t.Fatalf("first set: %v", err)
	}
	keyA, keyB := first[0].PublicKey, first[1].PublicKey

	second, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: b.ID, DisplayName: "B renamed"}, {MonitorID: a.ID, DisplayName: "A"},
	})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if second[0].PublicKey != keyB || second[1].PublicKey != keyA {
		t.Errorf("keys changed on reorder/rename: before a=%s b=%s, after %+v", keyA, keyB, second)
	}

	if _, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{{MonitorID: b.ID, DisplayName: "B"}}); err != nil {
		t.Fatalf("remove a: %v", err)
	}
	third, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: b.ID, DisplayName: "B"}, {MonitorID: a.ID, DisplayName: "A"},
	})
	if err != nil {
		t.Fatalf("add a back: %v", err)
	}
	if third[1].PublicKey == keyA {
		t.Error("a monitor removed and added back reused its old public key")
	}
	if third[0].PublicKey != keyB {
		t.Error("b lost its key when a was added back")
	}
}

func TestSetStatusPageEntriesRefusesBadInputAndChangesNothing(t *testing.T) {
	db := openTestDB(t)
	a := seedTaggedMonitor(t, db, "a", nil)
	p := seedStatusPage(t, db, StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors})
	before, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: "A"}})
	if err != nil {
		t.Fatalf("seed entries: %v", err)
	}

	cases := []struct {
		name    string
		page    int64
		entries []StatusPageEntryInput
		want    error
	}{
		{"missing page", 999, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: "A"}}, ErrNotFound},
		{"unknown monitor", p.ID, []StatusPageEntryInput{{MonitorID: 999, DisplayName: "X"}}, ErrUnknownMonitor},
		// The unknown monitor comes second, after a valid row was already
		// written inside the transaction, so a partial write would show.
		{"unknown after valid", p.ID, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: "New"}, {MonitorID: 999, DisplayName: "X"}}, ErrUnknownMonitor},
		{"empty name", p.ID, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: "  "}}, ErrInvalidStatusPage},
		{"long name", p.ID, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: strings.Repeat("n", 81)}}, ErrInvalidStatusPage},
		{"listed twice", p.ID, []StatusPageEntryInput{{MonitorID: a.ID, DisplayName: "A"}, {MonitorID: a.ID, DisplayName: "B"}}, ErrInvalidStatusPage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := db.SetStatusPageEntries(t.Context(), c.page, c.entries); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			after, err := db.ListStatusPageEntries(t.Context(), p.ID)
			if err != nil {
				t.Fatalf("ListStatusPageEntries: %v", err)
			}
			if len(after) != 1 || after[0] != before[0] {
				t.Fatalf("entries changed by a refused call: before %+v, after %+v", before, after)
			}
		})
	}

	tooMany := make([]StatusPageEntryInput, MaxStatusPageEntries+1)
	if _, err := db.SetStatusPageEntries(t.Context(), p.ID, tooMany); !errors.Is(err, ErrInvalidStatusPage) {
		t.Errorf("%d entries: err = %v, want ErrInvalidStatusPage", len(tooMany), err)
	}
}

// Deleting a monitor removes it from every page; deleting a page removes its
// entries. Both are foreign-key cascades, so this is a schema test.
func TestStatusPageEntriesCascade(t *testing.T) {
	db := openTestDB(t)
	a := seedTaggedMonitor(t, db, "a", nil)
	b := seedTaggedMonitor(t, db, "b", nil)
	p := seedStatusPage(t, db, StatusPage{Slug: "status", Title: "Status", Selection: StatusPageSelectMonitors})
	if _, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: a.ID, DisplayName: "A"}, {MonitorID: b.ID, DisplayName: "B"},
	}); err != nil {
		t.Fatalf("SetStatusPageEntries: %v", err)
	}
	if err := db.DeleteMonitor(t.Context(), a.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	got, err := db.ListStatusPageEntries(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("ListStatusPageEntries: %v", err)
	}
	if len(got) != 1 || got[0].MonitorID != b.ID {
		t.Fatalf("after deleting monitor a, entries = %+v, want only b", got)
	}

	if err := db.DeleteStatusPage(t.Context(), p.ID); err != nil {
		t.Fatalf("DeleteStatusPage: %v", err)
	}
	var left int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM status_page_entries").Scan(&left); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if left != 0 {
		t.Errorf("%d entries outlived their page", left)
	}
}

// On a tag page a monitor needs both the tag and a public name to show: a
// tagged monitor without an entry would otherwise appear under its internal
// name, and an entry whose monitor lost the tag no longer belongs.
func TestTagPageShowsOnlyNamedMonitorsThatCarryTheTag(t *testing.T) {
	db := openTestDB(t)
	named := seedTaggedMonitor(t, db, "named", map[string]string{"customer": "Acme"})
	unnamed := seedTaggedMonitor(t, db, "unnamed", map[string]string{"customer": "Acme"})
	untagged := seedTaggedMonitor(t, db, "untagged", map[string]string{"customer": "Other"})
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectTag, TagKey: "customer", TagValue: "Acme"})
	if _, err := db.SetStatusPageEntries(t.Context(), p.ID, []StatusPageEntryInput{
		{MonitorID: untagged.ID, DisplayName: "Lost the tag"},
		{MonitorID: named.ID, DisplayName: "Website"},
	}); err != nil {
		t.Fatalf("SetStatusPageEntries: %v", err)
	}

	shown, err := db.ShownStatusPageEntries(t.Context(), p)
	if err != nil {
		t.Fatalf("ShownStatusPageEntries: %v", err)
	}
	if len(shown) != 1 || shown[0].MonitorID != named.ID || shown[0].DisplayName != "Website" {
		t.Fatalf("shown = %+v, want only the named, tagged monitor", shown)
	}

	waiting, err := db.UnnamedStatusPageMonitors(t.Context(), p)
	if err != nil {
		t.Fatalf("UnnamedStatusPageMonitors: %v", err)
	}
	if len(waiting) != 1 || waiting[0] != unnamed.ID {
		t.Fatalf("unnamed = %v, want [%d]", waiting, unnamed.ID)
	}

	// The same entries on a monitors page are all shown, and nothing waits.
	p.Selection, p.TagKey, p.TagValue = StatusPageSelectMonitors, "", ""
	p, err = db.UpdateStatusPage(t.Context(), p)
	if err != nil {
		t.Fatalf("UpdateStatusPage: %v", err)
	}
	shown, err = db.ShownStatusPageEntries(t.Context(), p)
	if err != nil {
		t.Fatalf("ShownStatusPageEntries: %v", err)
	}
	if len(shown) != 2 {
		t.Errorf("monitors page shows %d entries, want 2", len(shown))
	}
	if waiting, _ := db.UnnamedStatusPageMonitors(t.Context(), p); len(waiting) != 0 {
		t.Errorf("monitors page reports unnamed monitors: %v", waiting)
	}
}
