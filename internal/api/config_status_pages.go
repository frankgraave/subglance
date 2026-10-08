package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// Status pages in a configuration file.
//
// A page is matched by its slug, the one name it has that a person does not
// change casually and that the instance already keeps unique. Its monitors
// are written as monitor keys, so a file moved to another instance puts the
// same monitors on the page there.
//
// Status pages are administered by administrators only, through the API and
// therefore through the file as well: an editor's export leaves them out, and
// an editor's import of a file that carries them is refused whole.

// pageStep is one status page an import creates or changes.
type pageStep struct {
	id   int64 // 0 when created
	page store.StatusPage
	// write is set when the page's settings change.
	write bool
	// monitors lists the page's entries by monitor key, in page order; nil
	// leaves the entries alone.
	monitors []configfile.StatusPageMonitor
}

// exportStatusPages writes every page with every setting, its monitors in
// page order under their public names.
func (s *Server) exportStatusPages(ctx context.Context, monitorKeys map[int64]string) ([]configfile.StatusPage, error) {
	pages, err := s.db.ListStatusPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list status pages: %w", err)
	}
	var out []configfile.StatusPage
	for _, p := range pages {
		entries, err := s.db.ListStatusPageEntries(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("list status page entries: %w", err)
		}
		sp := configfile.StatusPage{
			Slug: p.Slug, Title: p.Title, Description: ptr(p.Description),
			Timezone: ptr(p.Timezone), Language: ptr(p.Language), Accent: ptr(p.Accent),
			HideCredit: ptr(p.HideCredit), Indexable: ptr(p.Indexable), Enabled: ptr(p.Enabled),
			Selection: ptr(p.Selection), TagKey: p.TagKey, TagValue: p.TagValue,
			Monitors: []configfile.StatusPageMonitor{},
		}
		for _, e := range entries {
			sp.Monitors = append(sp.Monitors, configfile.StatusPageMonitor{
				Monitor: monitorKeys[e.MonitorID], Name: e.DisplayName,
			})
		}
		out = append(out, sp)
	}
	return out, nil
}

// loadPages adds the instance's status pages and their entries to ex.
func (s *Server) loadPages(ctx context.Context, ex *existingConfig) error {
	pages, err := s.db.ListStatusPages(ctx)
	if err != nil {
		return err
	}
	ex.pages = make(map[string]store.StatusPage, len(pages))
	ex.entries = make(map[int64][]store.StatusPageEntry, len(pages))
	for _, p := range pages {
		ex.pages[p.Slug] = p
		if ex.entries[p.ID], err = s.db.ListStatusPageEntries(ctx, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// planPage validates one page against the rules the status page API applies
// and decides what to write.
func planPage(p *importPlan, ex existingConfig, path string, sp configfile.StatusPage, monitorKnown map[string]bool) error {
	slug := strings.ToLower(strings.TrimSpace(sp.Slug))
	existing, found := ex.pages[slug]
	next := store.StatusPage{Selection: store.StatusPageSelectMonitors}
	if found {
		next = existing
	}
	next.Slug = slug
	if strings.TrimSpace(sp.Title) != "" || !found {
		next.Title = sp.Title
	}
	setIf(&next.Description, sp.Description)
	setIf(&next.Timezone, sp.Timezone)
	setIf(&next.Language, sp.Language)
	setIf(&next.Accent, sp.Accent)
	setIf(&next.HideCredit, sp.HideCredit)
	setIf(&next.Indexable, sp.Indexable)
	setIf(&next.Enabled, sp.Enabled)
	switch {
	case sp.Selection != nil:
		next.Selection, next.TagKey, next.TagValue = *sp.Selection, sp.TagKey, sp.TagValue
	case sp.TagKey != "" || sp.TagValue != "":
		// A tag pair without a selection would either be ignored or turn a
		// page of hand-picked monitors into a tag page without saying so.
		return problemAt(sub(path, "selection"), "is required when tag_key or tag_value is given")
	}

	normalised, err := store.NormaliseStatusPage(next)
	if err != nil {
		var invalid *store.StatusPageError
		if errors.As(err, &invalid) {
			return problemAt(sub(path, invalid.Field), "%s", invalid.Msg)
		}
		return err
	}
	next = normalised

	if err := checkPageMonitors(sub(path, "monitors"), sp.Monitors, monitorKnown); err != nil {
		return err
	}

	item := importItem{Key: slug, Name: next.Title, Action: actionCreate}
	step := pageStep{page: next, monitors: sp.Monitors, write: !found}
	if found {
		step.id = existing.ID
		item.Changes = pageChanges(existing, next)
		step.write = len(item.Changes) > 0
		if sp.Monitors != nil {
			if samePageMonitors(ex, ex.entries[existing.ID], sp.Monitors) {
				step.monitors = nil
			} else {
				item.Changes = append(item.Changes, "monitors")
			}
		}
		item.Action = actionUnchanged
		if len(item.Changes) > 0 {
			item.Action = actionUpdate
		}
	}
	p.report.StatusPages = append(p.report.StatusPages, item)
	if step.write || step.monitors != nil {
		p.pages = append(p.pages, step)
	}
	return nil
}

// setIf overwrites *dst with *src when the file gives a value.
func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// checkPageMonitors applies the entry rules of the status page API to a
// file's list, naming the place in the file instead of a monitor id.
func checkPageMonitors(path string, list []configfile.StatusPageMonitor, monitorKnown map[string]bool) error {
	if len(list) > store.MaxStatusPageEntries {
		return problemAt(path, "at most %d monitors per page", store.MaxStatusPageEntries)
	}
	seen := make(map[string]bool, len(list))
	for i, m := range list {
		at := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case !monitorKnown[m.Monitor]:
			return problemAt(sub(at, "monitor"), "names monitor %q, which is neither in the file nor on this instance", m.Monitor)
		case seen[m.Monitor]:
			return problemAt(sub(at, "monitor"), "monitor %q is listed twice", m.Monitor)
		}
		seen[m.Monitor] = true
		if _, msg := store.NormaliseStatusPageDisplayName(m.Name); msg != "" {
			return problemAt(sub(at, "name"), "%s", msg)
		}
	}
	return nil
}

// pageChanges lists the settings that differ, by their name in the file.
func pageChanges(a, b store.StatusPage) []string {
	var out []string
	for _, f := range []struct {
		name string
		diff bool
	}{
		{"title", a.Title != b.Title},
		{"description", a.Description != b.Description},
		{"timezone", a.Timezone != b.Timezone},
		{"language", a.Language != b.Language},
		{"accent", a.Accent != b.Accent},
		{"hide_credit", a.HideCredit != b.HideCredit},
		{"indexable", a.Indexable != b.Indexable},
		{"enabled", a.Enabled != b.Enabled},
		{"selection", a.Selection != b.Selection},
		{"tag_key", a.TagKey != b.TagKey},
		{"tag_value", a.TagValue != b.TagValue},
	} {
		if f.diff {
			out = append(out, f.name)
		}
	}
	return out
}

// samePageMonitors reports whether a page already lists exactly these
// monitors, in this order, under these names. A key that is not on the
// instance yet belongs to a monitor this import creates, so it is a change.
func samePageMonitors(ex existingConfig, have []store.StatusPageEntry, want []configfile.StatusPageMonitor) bool {
	if len(have) != len(want) {
		return false
	}
	ids := ex.monitorIDs()
	for i, m := range want {
		id, ok := ids[m.Monitor]
		name, _ := store.NormaliseStatusPageDisplayName(m.Name)
		if !ok || have[i].MonitorID != id || have[i].DisplayName != name {
			return false
		}
	}
	return true
}

// applyPage writes one planned page.
//
// A page created switched on is created switched off, given its monitors and
// only then switched on, so a visitor never sees it half built, and a failure
// in between leaves a draft rather than a published empty page. Running the
// file again finds the draft by its slug and finishes it. An existing page
// being disabled is switched off before its entries are replaced: a converted
// password-protected page must never briefly expose its new monitor list.
func (s *Server) applyPage(ctx context.Context, st pageStep, monitorIDs map[string]int64) error {
	publish := st.page.Enabled
	if st.id == 0 {
		draft := st.page
		draft.Enabled = false
		created, err := s.db.CreateStatusPage(ctx, draft)
		if err != nil {
			return err
		}
		st.id = created.ID
		st.write = publish
	} else if st.write && !publish {
		st.page.ID = st.id
		if _, err := s.db.UpdateStatusPage(ctx, st.page); err != nil {
			return err
		}
		st.write = false
	}
	if st.monitors != nil {
		in := make([]store.StatusPageEntryInput, len(st.monitors))
		for i, m := range st.monitors {
			in[i] = store.StatusPageEntryInput{MonitorID: monitorIDs[m.Monitor], DisplayName: m.Name}
		}
		if _, err := s.db.SetStatusPageEntries(ctx, st.id, in); err != nil {
			return err
		}
	}
	if st.write {
		st.page.ID = st.id
		if _, err := s.db.UpdateStatusPage(ctx, st.page); err != nil {
			return err
		}
	}
	return nil
}
