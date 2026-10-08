package kumaimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

// Kuma's public page reads only public groups, ordered by group.weight, then
// monitor_group.weight. A monitor's check group (type=group) is unrelated to
// these page sections and must not expand into monitors that were not public.
func convertStatusPages(src source, keys map[int64]string, res *Result) {
	groups := weightedPageRows(src.pageGroups, "status_page_id", true)
	links := weightedPageRows(src.pageMonitors, "group_id", false)
	names := map[string]string{}
	for _, m := range res.Document.Monitors {
		names[m.Key] = m.Name
	}
	originalNames := map[int]string{}
	for _, m := range src.monitors {
		originalNames[m.int("id")] = m.str("name")
	}
	slugs := map[string]bool{}
	for _, p := range src.statusPages {
		changed := func(reason string) {
			res.Changed = append(res.Changed, Note{Kind: "status page", Name: p.str("title"), Reason: reason})
		}
		// Use the store's actual rules, not a second copy of its limits or
		// reserved slugs. Refuse a page rather than quietly change its URL.
		normal, err := store.NormaliseStatusPage(store.StatusPage{
			Slug: p.str("slug"), Title: p.str("title"), Description: p.str("description"),
			Selection: store.StatusPageSelectMonitors,
		})
		reason := ""
		switch {
		case err != nil:
			reason = err.Error()
		case slugs[normal.Slug]:
			reason = "slug is already used by another imported page"
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, Note{Kind: "status page", Name: p.str("title"),
				Reason: fmt.Sprintf("/status/%s was not imported: %s", p.str("slug"), reason)})
			continue
		}
		slugs[normal.Slug] = true
		page := configfile.StatusPage{
			Slug: normal.Slug, Title: normal.Title, Description: ptr(normal.Description),
			Enabled: ptr(p.bool("published")), Indexable: ptr(p.bool("search_engine_index")),
			Selection: ptr(store.StatusPageSelectMonitors), Monitors: []configfile.StatusPageMonitor{},
		}
		// Old schemas without show_powered_by keep the credit, rather than
		// silently hiding it because an absent column reads as false.
		_, creditKnown := p["show_powered_by"]
		page.HideCredit = ptr(creditKnown && !p.bool("show_powered_by"))
		if p.str("slug") != normal.Slug {
			changed("slug was normalised to /status/" + normal.Slug)
		}
		if p.str("password") != "" {
			// There is no password gate on a SubGlance status page. An
			// explicit false also switches off an existing page on import.
			page.Enabled = ptr(false)
			changed("password protection is not supported; imported disabled, not public; review access before enabling the page")
		}
		pageGroups := groups[p.int("id")]
		if len(pageGroups) > 1 {
			changed("public groups are flattened into one monitor list in group order; section headings are not carried over")
		} else if len(pageGroups) == 1 {
			changed("the public group heading is not carried over")
		}
		seen := map[string]bool{}
		for _, g := range pageGroups {
			for _, l := range links[g.int("id")] {
				id := l.int("monitor_id")
				key, ok := keys[int64(id)]
				label := originalNames[id]
				if label == "" {
					label = fmt.Sprintf("id %d", id)
				}
				switch {
				case !ok:
					changed(fmt.Sprintf("monitor %q was left off this page because it was not imported", label))
					continue
				case seen[key]:
					changed(fmt.Sprintf("monitor %q appeared more than once; only its first position is kept", label))
					continue
				}
				seen[key] = true
				name, problem := store.NormaliseStatusPageDisplayName(names[key])
				if problem != "" {
					changed(fmt.Sprintf("monitor %q was left off this page: its public name %s", label, problem))
					continue
				}
				if len(page.Monitors) >= store.MaxStatusPageEntries {
					changed(fmt.Sprintf("monitor %q was left off this page: at most %d monitors per page", label, store.MaxStatusPageEntries))
					continue
				}
				page.Monitors = append(page.Monitors, configfile.StatusPageMonitor{Monitor: key, Name: name})
				if l.bool("send_url") || l.str("custom_url") != "" {
					changed(fmt.Sprintf("monitor %q: public monitor links are not carried over", label))
				}
			}
		}
		reportPageSettings(p, src, changed)
		res.Document.StatusPages = append(res.Document.StatusPages, page)
	}
}

// weightedPageRows leaves source slices untouched and breaks equal weights
// by row id, making the result repeatable even after a database vacuum.
func weightedPageRows(rows []row, owner string, publicOnly bool) map[int][]row {
	out := map[int][]row{}
	for _, r := range rows {
		if !publicOnly || r.bool("public") {
			out[r.int(owner)] = append(out[r.int(owner)], r)
		}
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].int("weight") != list[j].int("weight") {
				return list[i].int("weight") < list[j].int("weight")
			}
			return list[i].int("id") < list[j].int("id")
		})
	}
	return out
}

// Report the setting's presence, never its value: analytics URLs, CSS and
// footer text can contain credentials, just like a page's password can.
func reportPageSettings(p row, src source, changed func(string)) {
	for _, f := range []struct{ key, label string }{
		{"custom_css", "custom CSS"}, {"footer_text", "footer text"}, {"rss_title", "custom RSS title"},
	} {
		if strings.TrimSpace(p.str(f.key)) != "" {
			changed(f.label + " is not carried over")
		}
	}
	if icon := p.str("icon"); icon != "" && icon != "/icon.svg" {
		changed("logo is not carried over; upload it under Settings, Status pages after importing")
	}
	for _, key := range []string{"google_analytics_tag_id", "analytics_id", "analytics_script_url", "analytics_type"} {
		if p.str(key) != "" {
			changed("analytics settings are not carried over")
			break
		}
	}
	if theme := p.str("theme"); theme != "" && theme != "auto" {
		changed("fixed page theme is not carried over")
	}
	for _, f := range []struct{ key, label string }{
		{"show_tags", "public monitor tags"}, {"show_certificate_expiry", "certificate expiry display"},
		{"show_only_last_heartbeat", "last-heartbeat-only display"},
	} {
		if p.bool(f.key) {
			changed(f.label + " is not carried over")
		}
	}
	if interval := p.int("auto_refresh_interval"); interval != 0 && interval != 300 {
		changed("custom page refresh interval is not carried over")
	}
	for _, relation := range []struct {
		rows   []row
		reason string
	}{
		{src.pageDomains, "custom domains are not carried over; configure the new page's address at the reverse proxy"},
		{src.pageIncidents, "page incidents are not carried over; history stays in Kuma"},
		{src.pageWindows, "page maintenance announcements are not carried over; converted monitor maintenance windows are separate"},
	} {
		for _, r := range relation.rows {
			if r.int("status_page_id") == p.int("id") {
				changed(relation.reason)
				break
			}
		}
	}
}
