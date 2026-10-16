package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The words of a status page are what strangers read: the title is the
// heading and the browser tab, a public name is the only name a visitor sees
// for a service. Neither may hold a control character, and the description,
// a paragraph, may hold only line breaks and tabs. Each is refused with a
// 400 that names the field, and nothing is stored.

const (
	noControlTitle       = "title must not contain line breaks, tabs or other control characters"
	noControlDescription = "description must not contain control characters other than line breaks and tabs"
)

// paragraphControls are the controls a description keeps as text.
var paragraphControls = map[string]bool{"line feed": true, "carriage return": true, "tab": true}

func TestStatusPageTextRefusesControlCharacters(t *testing.T) {
	ctx := context.Background()
	for label, text := range controlNames {
		t.Run(label, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com"})

			rec := doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
				`{"slug":"a","title":"`+text+`","selection":"monitors"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "title", noControlTitle)

			mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
				`{"slug":"acme","title":"Acme","selection":"monitors"}`), http.StatusCreated, "seed page")
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
				`{"slug":"acme","title":"`+text+`","selection":"monitors"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "title", noControlTitle)

			rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
				`{"entries":[{"monitor_id":`+itoa(m.ID)+`,"display_name":"`+text+`"}]}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "entries", store.NoControlCharacters)

			rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
				`{"slug":"acme","title":"Acme","description":"`+text+`","selection":"monitors"}`)
			if paragraphControls[label] {
				mustStatus(t, rec, http.StatusOK, "a description with a "+label)
			} else {
				wantStatusProblem(t, rec.Code, rec.Body.String(), "description", noControlDescription)
			}

			pages, err := db.ListStatusPages(ctx)
			must(t, err)
			if len(pages) != 1 || pages[0].Title != "Acme" {
				t.Fatalf("pages = %+v, want only the seeded one, titled Acme", pages)
			}
			if want := ""; !paragraphControls[label] && pages[0].Description != want {
				t.Errorf("a refused description stored %q", pages[0].Description)
			}
			if entries, _ := db.ListStatusPageEntries(ctx, pages[0].ID); len(entries) != 0 {
				t.Errorf("a refused entry list stored %+v", entries)
			}
		})
	}
}

// A description is a paragraph: its line breaks and tabs are kept as typed.
func TestStatusPageDescriptionKeepsLineBreaksAndTabs(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"acme","title":"Acme","description":"Line one\r\nLine two\twith a tab","selection":"monitors"}`)
	mustStatus(t, rec, http.StatusCreated, "create")
	if got := decodeStatusPageBody(t, rec).Description; got != "Line one\r\nLine two\twith a tab" {
		t.Errorf("description = %q, want it kept as sent", got)
	}
}

// Format characters are not control characters: a zero-width joiner holds an
// emoji sequence together and a direction mark keeps right-to-left text
// readable.
func TestStatusPageTextKeepsFormatCharacters(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com"})
	const text = "Ops \u200d team \u05d0\u05d1\u200f"
	body, err := json.Marshal(map[string]string{"slug": "acme", "title": text, "description": text, "selection": "monitors"})
	must(t, err)
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages", string(body)), http.StatusCreated, "create")
	body, err = json.Marshal(map[string]any{"entries": []map[string]any{{"monitor_id": m.ID, "display_name": text}}})
	must(t, err)
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries", string(body))
	mustStatus(t, rec, http.StatusOK, "set entries")
	page := decodeStatusPageBody(t, rec)
	if page.Title != text || page.Description != text || len(page.Entries) != 1 || page.Entries[0].DisplayName != text {
		t.Errorf("page = %+v, want the format characters kept", page)
	}
}

// A page saved before the rule keeps its title, description and public names
// through every other edit and through an import of the instance's own
// export, which sends them back unchanged. A new or changed one is held to
// the rule.
func TestStatusPageTextSavedBeforeTheRuleIsKept(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)
	a := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://a.example.com"})
	b := seedMonitor(t, db, store.Monitor{Name: "web", Type: "http", Target: "https://b.example.com"})
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"acme","title":"Acme","selection":"monitors"}`), http.StatusCreated, "seed page")
	mustStatus(t, doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
		`{"entries":[{"monitor_id":`+itoa(a.ID)+`,"display_name":"API"},{"monitor_id":`+itoa(b.ID)+`,"display_name":"Web"}]}`),
		http.StatusOK, "seed entries")

	// Written past the store's checks, as a page saved before the rule was.
	const oldTitle, oldDescription, oldName = "Acme\x1bstatus", "Read\x00me", "API\x1beu"
	_, err := db.Writer.ExecContext(ctx, `UPDATE status_pages SET title = ?, description = ? WHERE slug = 'acme'`,
		oldTitle, oldDescription)
	must(t, err)
	_, err = db.Writer.ExecContext(ctx, `UPDATE status_page_entries SET display_name = ? WHERE monitor_id = ?`, oldName, a.ID)
	must(t, err)

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme\u001bstatus","description":"Read\u0000me","selection":"monitors","enabled":true}`)
	mustStatus(t, rec, http.StatusOK, "switch the page on and send its words back")
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
		`{"entries":[{"monitor_id":`+itoa(b.ID)+`,"display_name":"Website"},{"monitor_id":`+itoa(a.ID)+`,"display_name":"API\u001beu"}]}`)
	mustStatus(t, rec, http.StatusOK, "reorder, rename the other entry and send the old name back")

	code, rep, body := importYAML(t, srv, exportYAML(t, srv), false)
	if code != http.StatusOK {
		t.Fatalf("import of the instance's own export = %d: %s", code, body)
	}
	if len(rep.StatusPages) != 1 || rep.StatusPages[0].Action != actionUnchanged {
		t.Errorf("status page report = %+v, want the page unchanged", rep.StatusPages)
	}

	page, err := db.GetStatusPageBySlug(ctx, "acme")
	must(t, err)
	if page.Title != oldTitle || page.Description != oldDescription || !page.Enabled {
		t.Errorf("page = %q %q enabled %v, want the old words kept and the page on", page.Title, page.Description, page.Enabled)
	}
	entries, err := db.ListStatusPageEntries(ctx, page.ID)
	must(t, err)
	if len(entries) != 2 || entries[1].DisplayName != oldName || entries[0].DisplayName != "Website" {
		t.Errorf("entries = %+v, want Website, then the old name", entries)
	}

	// The exemption belongs to the words the page has: another invalid
	// title, description or name is refused, and so is the old name
	// given to a monitor that does not have it.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme\u001bother","description":"Read\u0000me","selection":"monitors"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "title", noControlTitle)
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme",
		`{"slug":"acme","title":"Acme\u001bstatus","description":"Read\u0000you","selection":"monitors"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "description", noControlDescription)
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/status-pages/acme/entries",
		`{"entries":[{"monitor_id":`+itoa(b.ID)+`,"display_name":"API\u001beu"},{"monitor_id":`+itoa(a.ID)+`,"display_name":"API"}]}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "entries", store.NoControlCharacters)
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/status-pages",
		`{"slug":"copy","title":"Acme\u001bstatus","selection":"monitors"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "title", noControlTitle)
}

// An import refuses such a title, description or public name at the place in
// the file that holds it, and writes nothing.
func TestImportRefusesAControlCharacterOnAStatusPageAtItsPath(t *testing.T) {
	head := "version: 1\nmonitors:\n  - key: shop\n    name: Shop\n    type: tcp\n    target: shop.example.com:443\nstatus_pages:\n"
	cases := map[string]struct{ page, field, want string }{
		"title":       {"  - slug: a\n    title: \"Acme\\e\"\n", "status_pages[0].title", noControlTitle},
		"description": {"  - slug: a\n    title: A\n    description: \"Read\\0me\"\n", "status_pages[0].description", noControlDescription},
		"public name": {"  - slug: a\n    title: A\n    monitors:\n      - monitor: shop\n        name: \"Shop\\nfront\"\n",
			"status_pages[0].monitors[0].name", store.NoControlCharacters},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, head+tc.page, false)
			wantStatusProblem(t, code, body, tc.field, tc.want)
			ctx := context.Background()
			pages, _ := db.ListStatusPages(ctx)
			mons, _ := db.ListMonitors(ctx)
			if len(pages)+len(mons) != 0 {
				t.Errorf("a refused file wrote %d pages and %d monitors", len(pages), len(mons))
			}
		})
	}
}
