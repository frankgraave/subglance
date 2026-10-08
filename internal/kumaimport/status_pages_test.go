package kumaimport

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
)

func pageRow(over row) row {
	r := row{"id": 1, "slug": "public", "title": "Public status", "description": "Service availability",
		"published": 1, "search_engine_index": 1, "show_powered_by": 1}
	for k, v := range over {
		r[k] = v
	}
	return r
}

func TestConvertPagesFromBothSchemas(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			res := convertFixture(t, fixture)
			if res.StatusPages != 2 || len(res.Document.StatusPages) != 1 || countKind(res.Skipped, "status page") != 1 {
				t.Fatalf("pages: read=%d converted=%v skipped=%v", res.StatusPages, res.Document.StatusPages, res.Skipped)
			}
			p := res.Document.StatusPages[0]
			if p.Slug != "public" || p.Title != "Public status" || deref(p.Description) != "Shop and API status" ||
				!deref(p.Enabled) || !deref(p.Indexable) || !deref(p.HideCredit) || deref(p.Selection) != "monitors" {
				t.Errorf("page = %+v", p)
			}
			want := []configfile.StatusPageMonitor{
				{Monitor: "shop-prod", Name: "Shop (prod)"}, {Monitor: "api-post", Name: "API POST"},
				{Monitor: "status-json", Name: "Status JSON"}, {Monitor: "postgres", Name: "Postgres"},
				{Monitor: "router", Name: "Router"},
			}
			if !slices.Equal(p.Monitors, want) {
				t.Errorf("monitors = %v, want %v", p.Monitors, want)
			}
			var changed []string
			for _, n := range res.Changed {
				if n.Kind == "status page" && n.Name == "Public status" {
					changed = append(changed, n.Reason)
				}
			}
			report := strings.Join(changed, "\n")
			for _, note := range []string{"flattened", "custom CSS", "footer text", "logo", "analytics", "custom domains",
				"page incidents", "maintenance announcements", "public monitor tags", "certificate expiry", "Container web", "Edge"} {
				if !strings.Contains(report, note) {
					t.Errorf("no %q in page notes: %s", note, report)
				}
			}
			if !strings.Contains(notesOf(res), `status page "API status": /status/api was not imported`) {
				t.Error("reserved slug was not reported")
			}
		})
	}
}

func TestPageOrderUsesPublicGroupWeightsThenMonitorWeights(t *testing.T) {
	src := source{
		statusPages: []row{pageRow(nil), pageRow(row{"id": 2, "slug": "empty"})},
		monitors: []row{
			httpRow(row{"id": 1, "name": "Same"}), httpRow(row{"id": 2, "name": "Same"}),
			httpRow(row{"id": 3, "name": "Third"}), httpRow(row{"id": 4, "name": "Private"}),
			httpRow(row{"id": 5, "name": "Unsupported", "type": "docker"}),
		},
		pageGroups: []row{
			{"id": 10, "status_page_id": 1, "public": 1, "weight": 2},
			{"id": 30, "status_page_id": 1, "public": 0, "weight": 0},
			{"id": 20, "status_page_id": 1, "public": 1, "weight": 1},
			{"id": 40, "status_page_id": 99, "public": 1, "weight": 0},
		},
		pageMonitors: []row{
			{"id": 1, "group_id": 20, "monitor_id": 1, "weight": 2},
			{"id": 2, "group_id": 20, "monitor_id": 2, "weight": 1},
			{"id": 3, "group_id": 30, "monitor_id": 4, "weight": 1},
			{"id": 4, "group_id": 10, "monitor_id": 3, "weight": 1},
			{"id": 5, "group_id": 10, "monitor_id": 1, "weight": 2},
			{"id": 6, "group_id": 10, "monitor_id": 5, "weight": 3},
			{"id": 7, "group_id": 10, "monitor_id": 999, "weight": 4},
			{"id": 8, "group_id": 40, "monitor_id": 4, "weight": 1},
		},
	}
	res := convert(src)
	p := res.Document.StatusPages[0]
	if !slices.Equal(p.Monitors, []configfile.StatusPageMonitor{
		{Monitor: res.Document.Monitors[1].Key, Name: "Same"},
		{Monitor: res.Document.Monitors[0].Key, Name: "Same"},
		{Monitor: "third", Name: "Third"},
	}) {
		t.Errorf("monitors = %v", p.Monitors)
	}
	if res.Document.Monitors[0].Key == res.Document.Monitors[1].Key {
		t.Fatal("test needs distinct imported keys")
	}
	if p := res.Document.StatusPages[1]; p.Monitors == nil || len(p.Monitors) != 0 {
		t.Errorf("empty page = %+v", p)
	}
	for _, note := range []string{"only its first position", "Unsupported", "id 999", "flattened"} {
		if !strings.Contains(notesOf(res), note) {
			t.Errorf("no %q in notes: %s", note, notesOf(res))
		}
	}
	// Repeated conversion is stable and does not reorder the source rows.
	again := convert(src)
	if !slices.Equal(again.Document.StatusPages[0].Monitors, p.Monitors) || src.pageGroups[0].int("id") != 10 || src.pageMonitors[0].int("id") != 1 {
		t.Error("conversion mutates the source order")
	}
}

func TestPageWeightsBreakTiesByRowID(t *testing.T) {
	rows := []row{{"id": 3, "owner": 1, "weight": 1}, {"id": 2, "owner": 1, "weight": 1}, {"id": 1, "owner": 1, "weight": 0}}
	got := weightedPageRows(rows, "owner", false)[1]
	for i, r := range got {
		if r.int("id") != i+1 {
			t.Errorf("order = %v", got)
			break
		}
	}
}

func TestPageVisibilityAndCreditAreExplicit(t *testing.T) {
	for _, published := range []int{0, 1} {
		for _, indexed := range []int{0, 1} {
			for _, credit := range []int{0, 1} {
				res := convert(source{statusPages: []row{pageRow(row{"published": published, "search_engine_index": indexed, "show_powered_by": credit})}})
				p := res.Document.StatusPages[0]
				if p.Enabled == nil || p.Indexable == nil || p.HideCredit == nil ||
					*p.Enabled != (published == 1) || *p.Indexable != (indexed == 1) || *p.HideCredit != (credit == 0) ||
					p.Selection == nil || *p.Selection != "monitors" || p.Monitors == nil {
					t.Errorf("flags %d/%d/%d = %+v", published, indexed, credit, p)
				}
			}
		}
	}
}

func TestPasswordPageIsDisabledAndSecretNeverRendered(t *testing.T) {
	for _, secret := range []string{"kuma-secret-page", " "} {
		r := pageRow(row{"password": secret})
		res := convert(source{statusPages: []row{r}})
		if len(res.Document.StatusPages) != 1 || res.Document.StatusPages[0].Enabled == nil || *res.Document.StatusPages[0].Enabled {
			t.Fatalf("password page is not explicitly disabled: %+v", res.Document.StatusPages)
		}
		file, err := Render(res)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(file), "imported disabled, not public") ||
			(secret != " " && strings.Contains(string(file), secret)) {
			t.Errorf("unsafe file: %s", file)
		}
	}
}

func TestPageValidationSkipsWithoutInventingAnotherURL(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields row
		reason string
	}{
		{"reserved", row{"slug": "assets"}, "reserved"},
		{"invalid slug", row{"slug": "service_status"}, "slug must"},
		{"long slug", row{"slug": strings.Repeat("a", 64)}, "slug must"},
		{"empty title", row{"title": ""}, "title must"},
		{"long title", row{"title": strings.Repeat("a", 121)}, "title must"},
		{"multiline title", row{"title": "Hello\nworld"}, "line breaks"},
		{"long description", row{"description": strings.Repeat("a", 501)}, "description must"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(source{statusPages: []row{pageRow(tc.fields)}})
			if len(res.Document.StatusPages) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, tc.reason) {
				t.Errorf("result = %+v", res)
			}
		})
	}
	res := convert(source{statusPages: []row{pageRow(row{"slug": "Public"}), pageRow(row{"id": 2, "slug": "public"})}})
	if len(res.Document.StatusPages) != 1 || res.Document.StatusPages[0].Slug != "public" ||
		!strings.Contains(notesOf(res), "normalised to /status/public") || !strings.Contains(notesOf(res), "already used") {
		t.Errorf("case and collision = %+v", res)
	}
}

func TestPageEntriesThatCannotFitAreReported(t *testing.T) {
	src := source{statusPages: []row{pageRow(nil)}, pageGroups: []row{{"id": 1, "status_page_id": 1, "public": 1}}}
	res := Result{}
	keys := map[int64]string{}
	for i := 1; i <= store.MaxStatusPageEntries+3; i++ {
		name := fmt.Sprintf("Monitor %d", i)
		if i == 1 {
			name = strings.Repeat("a", 81)
		}
		if i == 2 {
			name = "Line\nbreak"
		}
		key := fmt.Sprintf("monitor-%d", i)
		keys[int64(i)] = key
		res.Document.Monitors = append(res.Document.Monitors, configfile.Monitor{Key: key, Name: name})
		src.monitors = append(src.monitors, row{"id": i, "name": name})
		src.pageMonitors = append(src.pageMonitors, row{"id": i, "monitor_id": i, "group_id": 1, "weight": i})
	}
	convertStatusPages(src, keys, &res)
	p := res.Document.StatusPages[0]
	if len(p.Monitors) != store.MaxStatusPageEntries || p.Monitors[0].Monitor != "monitor-3" {
		t.Errorf("entries = %v", p.Monitors)
	}
	for _, note := range []string{"1-80 characters", "line breaks", "at most 200"} {
		if !strings.Contains(notesOf(res), note) {
			t.Errorf("no %q in %s", note, notesOf(res))
		}
	}
}

func TestOptionalPageSettingsAreReportedWithoutTheirValues(t *testing.T) {
	src := source{statusPages: []row{pageRow(row{
		"custom_css": "kuma-secret-css", "footer_text": "kuma-secret-footer", "rss_title": "kuma-secret-rss",
		"icon": "kuma-secret-logo", "analytics_script_url": "kuma-secret-analytics", "theme": "dark",
		"show_tags": 1, "show_certificate_expiry": 1, "show_only_last_heartbeat": 1, "auto_refresh_interval": 60,
	})}, monitors: []row{httpRow(nil)}, pageGroups: []row{{"id": 1, "status_page_id": 1, "public": 1}},
		pageMonitors:  []row{{"id": 1, "group_id": 1, "monitor_id": 1, "custom_url": "kuma-secret-link"}},
		pageDomains:   []row{{"status_page_id": 1, "domain": "kuma-secret-domain"}},
		pageIncidents: []row{{"status_page_id": 1, "content": "kuma-secret-incident"}},
		pageWindows:   []row{{"status_page_id": 1}},
	}
	res := convert(src)
	file, err := Render(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "kuma-secret") {
		t.Errorf("setting value leaked: %s", file)
	}
	for _, note := range []string{"custom CSS", "footer text", "custom RSS title", "logo", "analytics", "fixed page theme",
		"public monitor tags", "certificate expiry", "last-heartbeat-only", "refresh interval", "custom domains",
		"page incidents", "maintenance announcements", "public monitor links"} {
		if !strings.Contains(notesOf(res), note) {
			t.Errorf("no %q in %s", note, notesOf(res))
		}
	}
}

func TestPageRelationsNeverReportAnotherPagesSettings(t *testing.T) {
	src := source{statusPages: []row{pageRow(nil)}, pageDomains: []row{{"status_page_id": 99}},
		pageIncidents: []row{{"status_page_id": 99}}, pageWindows: []row{{"status_page_id": 99}}}
	res := convert(src)
	if len(res.Changed) != 0 || len(res.Skipped) != 0 {
		t.Errorf("unrelated notes: %s", notesOf(res))
	}
}
