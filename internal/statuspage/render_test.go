package statuspage

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// testCSS stands in for the built stylesheet: the unit suite must not depend
// on the frontend having been built. It carries the two font URLs the real
// build writes, in both quotings, so the rewrite is exercised.
const testCSS = `@font-face{font-family:InterVariable;src:url(/fonts/InterVariable-4.1-subset.woff2)format("woff2")}` +
	`@font-face{font-family:CommitMono;src:url("/fonts/CommitMono-1.143-400-subset.woff2")format("woff2")}` +
	`.sp-axis>span{color:red}`

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer([]byte(testCSS))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func render(t *testing.T, p Page) string {
	t.Helper()
	b, err := newTestRenderer(t).Render(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

var (
	styleElement  = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	scriptElement = regexp.MustCompile(`(?s)<script>(.*?)</script>`)
)

func previewPage(t *testing.T, name string) Page {
	t.Helper()
	at := time.Date(2026, 9, 29, 12, 34, 0, 0, time.UTC)
	p, ok := PreviewScenarios(at)[name]
	if !ok {
		t.Fatalf("no preview scenario %q", name)
	}
	return p
}

// The policy allows exactly the stylesheet and script the document carries.
// If the template or the CSS were escaped on the way in, the bytes would stop
// matching the hash and the browser would drop them: an unstyled page, with
// nothing but a console line to say why.
func TestRenderInlinesExactlyWhatThePolicyAllows(t *testing.T) {
	r := newTestRenderer(t)
	b, err := r.Render(previewPage(t, "outage"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	csp := r.ContentSecurityPolicy()

	styles := styleElement.FindAllStringSubmatch(html, -1)
	if len(styles) != 1 {
		t.Fatalf("want one <style>, got %d", len(styles))
	}
	if !strings.Contains(csp, "style-src "+hashOf(styles[0][1])) {
		t.Errorf("the inline stylesheet's hash is not in the policy %q", csp)
	}
	scripts := scriptElement.FindAllStringSubmatch(html, -1)
	if len(scripts) != 1 || scripts[0][1] != themeScript {
		t.Fatalf("want the theme script and nothing else, got %q", scripts)
	}
	if !strings.Contains(csp, "script-src "+hashOf(scripts[0][1])) {
		t.Errorf("the theme script's hash is not in the policy %q", csp)
	}
	if strings.Contains(html, " style=") {
		t.Error("a style attribute is not covered by a hash and would be blocked")
	}
	for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "font-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("policy %q allows any inline code", csp)
	}
}

func TestFontURLsAreRelativeToThePage(t *testing.T) {
	css := styleElement.FindStringSubmatch(render(t, previewPage(t, "allup")))[1]
	for _, want := range []string{"url(fonts/InterVariable", `url("fonts/CommitMono`} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %s", want)
		}
	}
	if strings.Contains(css, "/fonts/") {
		t.Error("a root-relative font URL breaks a page served behind a path prefix")
	}
}

func TestNewRendererRefusesAStylesheetThatClosesItsElement(t *testing.T) {
	if _, err := NewRenderer([]byte(`a{b:c}</STYLE><script>x()</script>`)); err == nil {
		t.Fatal("want an error")
	}
}

// What the operator types is text, wherever it lands.
func TestRenderEscapesOperatorText(t *testing.T) {
	p := previewPage(t, "outage")
	p.Title = `Acme <script>alert(1)</script>`
	p.Description = `"><img src=x onerror=alert(2)>`
	p.Entries[0].Name = `</style><b>bold</b>`
	html := render(t, p)
	for _, bad := range []string{"<script>alert", "<img", "<b>bold"} {
		if strings.Contains(html, bad) {
			t.Errorf("operator text reached the page as markup: %q", bad)
		}
	}
	if !strings.Contains(html, "Acme &lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the title is missing or double-escaped")
	}
}

// The document holds the page's own words and nothing it was not given: the
// only strings in it are the template's, the view's and the Page's.
func TestRenderPrintsThePageFields(t *testing.T) {
	html := render(t, previewPage(t, "outage"))
	for _, want := range []string{
		"<title>Example Co status</title>",
		`<h2 id="sp-summary">1 of 5 services is down</h2>`,
		"Services (5)",
		`data-status="down"`,
		`<span class="led-label">Degraded</span>`,
		"99.71% uptime, 90 days",
		"Scheduled", "Tomorrow, 02:00–03:00", "Affects Web app",
		"4 h 51 min so far",
		"Down 23 min",
		"Times in Europe/Amsterdam",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if got := strings.Count(html, "<i data-s="); got != 5*HistoryDays {
		t.Errorf("history bars = %d, want %d", got, 5*HistoryDays)
	}
}

// TestRenderNeverPublishesAPerfectUptimeItDidNotMeasure follows a near-perfect
// count from the history to the printed page: one confirmed-down check in
// 20,001 must read 99.99%, because "100.00% uptime" beside a history bar that
// shows the outage is the one number on a public page a reader trusts most.
func TestRenderNeverPublishesAPerfectUptimeItDidNotMeasure(t *testing.T) {
	p := previewPage(t, "allup")
	p.Entries = p.Entries[:1]
	near := []store.StatusHistoryHour{{Up: 20000, Down: 1}}
	p.Entries[0].Uptime90d = Uptime(near)
	p.Entries[0].Uptime30d = Uptime(near)
	html := render(t, p)
	if !strings.Contains(html, "99.99% uptime, 90 days") || !strings.Contains(html, "99.99% uptime, 30 days") ||
		strings.Contains(html, "100.00%") {
		t.Errorf("one down check in 20,001 is not printed as 99.99%%")
	}
}

func TestRenderUsesThePageTimeZone(t *testing.T) {
	p := previewPage(t, "allup")
	if html := render(t, p); !strings.Contains(html, `datetime="2026-09-29T12:34:00Z">Tue 29 Sep, 14:34</time>`) {
		t.Error("the update time is not in Europe/Amsterdam")
	}
	p.Timezone = "UTC"
	if html := render(t, p); !strings.Contains(html, `>Tue 29 Sep, 12:34</time>`) {
		t.Error("the update time does not follow the page's zone")
	}
	// 23:30 UTC is already the next day in Amsterdam: the date is the
	// page's, not the server's.
	p.Timezone = "Europe/Amsterdam"
	p.GeneratedAt = time.Date(2026, 9, 29, 23, 30, 0, 0, time.UTC)
	if html := render(t, p); !strings.Contains(html, `>Wed 30 Sep, 01:30</time>`) {
		t.Error("the update date is not the page zone's date")
	}
	p.Timezone = "Not/AZone"
	if _, err := newTestRenderer(t).Render(p); err == nil {
		t.Error("an unknown zone must fail, not fall back to the server's")
	}
}

// The page is read later than it is built: from a proxy's cache, or a tab
// left open overnight. A bare "Updated 14:34" then claims today's 14:34.
func TestUpdatedAlwaysCarriesItsDate(t *testing.T) {
	html := render(t, previewPage(t, "outage"))
	got := regexp.MustCompile(`Updated <time[^>]*>([^<]*)</time>`).FindStringSubmatch(html)
	if got == nil || !regexp.MustCompile(`^\w{3} \d{1,2} \w{3}, \d{2}:\d{2}$`).MatchString(got[1]) {
		t.Errorf("updated = %q, want a weekday, date and time", got)
	}
}

// A phone draws 30 days of history, so the figure under it is the 30-day
// one; the 90-day figure stays with the 90-day bar. The stylesheet shows one
// of the two spans, so each must name its own period.
func TestUptimeFollowsTheDaysTheBarDraws(t *testing.T) {
	html := render(t, previewPage(t, "outage"))
	for _, want := range []string{
		`<span class="sp-uptime sp-mono sp-axis-desk">99.71% uptime, 90 days</span>`,
		`<span class="sp-uptime sp-mono sp-axis-phone">99.17% uptime, 30 days</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, c := range []struct {
		e        Entry
		desk, ph string
	}{
		{Entry{}, "No uptime data yet", "No uptime data yet"},
		{Entry{Uptime90d: ptr(99.5)}, "99.50% uptime, 90 days", "No uptime data, 30 days"},
		{Entry{Uptime90d: ptr(99.5), Uptime30d: ptr(100)}, "99.50% uptime, 90 days", "100.00% uptime, 30 days"},
	} {
		row := serviceRow(c.e, &english)
		if row.Uptime != c.desk || row.UptimePhone != c.ph {
			t.Errorf("%+v: got %q / %q, want %q / %q", c.e, row.Uptime, row.UptimePhone, c.desk, c.ph)
		}
	}
}

func ptr(v float64) *float64 { return &v }

func TestAnEmptyPageSaysSoAndDrawsNoLists(t *testing.T) {
	html := render(t, previewPage(t, "empty"))
	if !strings.Contains(html, "No services on this page yet.") {
		t.Error("an empty page must say it has no services")
	}
	for _, absent := range []string{"sp-services", "sp-outages", "sp-maintenance"} {
		if strings.Contains(html, absent) {
			t.Errorf("an empty page draws %s", absent)
		}
	}
}

func TestMaintenanceCardOnlyWhenThereIsMaintenance(t *testing.T) {
	if strings.Contains(render(t, previewPage(t, "allup")), "sp-maintenance") {
		t.Error("no maintenance, no card (design §2)")
	}
	html := render(t, previewPage(t, "maintenance"))
	if !strings.Contains(html, "In progress") || !strings.Contains(html, "Now, until ") {
		t.Error("a running window reads as in progress")
	}
	if !strings.Contains(html, `<span class="chip chip--state">Maintenance</span>`) {
		t.Error("the entry in maintenance carries the chip")
	}
}

// A window whose services are none of this page's still shows its time, but
// names nothing: "Affects" followed by nothing reads as a broken page.
func TestMaintenanceWithNoServiceOnThePageNamesNone(t *testing.T) {
	p := previewPage(t, "outage")
	p.Maintenance[0].Keys = []string{"not-on-this-page"}
	html := render(t, p)
	if !strings.Contains(html, "Tomorrow, 02:00–03:00") {
		t.Error("the window itself must still be listed")
	}
	if strings.Contains(html, "Affects") {
		t.Error(`a window that affects no listed service must not print "Affects"`)
	}
}

func TestSummaryStatesACount(t *testing.T) {
	e := func(s ...Status) []Entry {
		out := make([]Entry, len(s))
		for i, st := range s {
			out[i] = Entry{Status: st}
		}
		return out
	}
	for _, tc := range []struct {
		entries []Entry
		lamp    string
		text    string
	}{
		{e(StatusUp, StatusDown, StatusUp), "down", "1 of 3 services is down"},
		{e(StatusDown, StatusDown, StatusDegraded), "down", "2 of 3 services are down"},
		{e(StatusUp, StatusDegraded), "warn", "1 of 2 services is degraded"},
		{e(StatusUp, StatusUp), "up", "All 2 services are up"},
		{e(StatusUp), "up", "The service is up"},
		{e(StatusDown), "down", "1 of 1 service is down"},
		{e(StatusUp, StatusUp, StatusNotMonitored), "up", "2 of 3 services are up · 1 not monitored"},
		{e(StatusUp, StatusNoData), "up", "1 of 2 services is up · 1 with no data yet"},
		{e(StatusNoData, StatusNotMonitored), "idle", "1 with no data yet · 1 not monitored"},
		{nil, "idle", "No services on this page yet."},
	} {
		got := summarise(tc.entries, &english)
		if got.Lamp != tc.lamp || got.Text != tc.text {
			t.Errorf("%v: got %q/%q, want %q/%q", tc.entries, got.Lamp, got.Text, tc.lamp, tc.text)
		}
	}
}

func TestEveryStatusHasALampAndAWord(t *testing.T) {
	for _, st := range []Status{StatusUp, StatusDegraded, StatusDown, StatusNoData, StatusNotMonitored} {
		row := serviceRow(Entry{Status: st}, &english)
		if row.Lamp == "" || row.Word == "" {
			t.Errorf("%s: lamp %q, word %q", st, row.Lamp, row.Word)
		}
	}
	if row := serviceRow(Entry{Status: StatusDown}, &english); row.Rail != "down" {
		t.Errorf("a down row carries the down rail, got %q", row.Rail)
	}
	if row := serviceRow(Entry{Status: StatusUp}, &english); row.Rail != "" {
		t.Errorf("an up row carries no rail, got %q", row.Rail)
	}
}

// The bars are hidden from assistive technology, so the sentence has to
// account for every day they draw.
func TestHistoryTextAccountsForEveryDay(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	days := previewDays(now, time.UTC, []int{0, 9}, []int{1}, 80)
	got := english.historyText(days)
	want := "Last 90 days: 77 up, 1 degraded, 2 down, 10 no data. Down today, 9 days ago. Degraded 1 day ago."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestOutageWording(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 9, 29, 14, 32, 0, 0, loc)
	start := time.Date(2026, 9, 29, 9, 41, 0, 0, loc)
	ongoing := outageRow(Outage{StartedAt: start.UTC(), DurationS: int64(now.Sub(start) / time.Second)}, "API", now, &english)
	if !ongoing.Ongoing || ongoing.Text != "Down since 09:41 today · 4 h 51 min so far" {
		t.Errorf("ongoing: %+v", ongoing)
	}
	s := time.Date(2026, 9, 19, 14, 2, 0, 0, loc)
	e := s.Add(23 * time.Minute)
	done := outageRow(Outage{StartedAt: s.UTC(), ResolvedAt: &e, DurationS: 23 * 60}, "API", now, &english)
	if done.Ongoing || done.Text != "Down 23 min · Sat 19 Sep, 14:02–14:25" {
		t.Errorf("resolved: %+v", done)
	}
	s = time.Date(2026, 9, 28, 23, 50, 0, 0, loc)
	e = s.Add(20 * time.Minute)
	overnight := outageRow(Outage{StartedAt: s.UTC(), ResolvedAt: &e, DurationS: 20 * 60}, "API", now, &english)
	if overnight.Text != "Down 20 min · Yesterday, 23:50 – Today, 00:10" {
		t.Errorf("across midnight: %q", overnight.Text)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		20 * time.Second:                "under 1 min",
		time.Minute:                     "1 min",
		59*time.Minute + 59*time.Second: "59 min",
		time.Hour:                       "1 h",
		4*time.Hour + 51*time.Minute:    "4 h 51 min",
		24 * time.Hour:                  "1 d",
		51*time.Hour + 30*time.Minute:   "2 d 3 h",
	} {
		if got := english.duration(d); got != want {
			t.Errorf("duration(%s) = %q, want %q", d, got, want)
		}
	}
}

// An outage whose entry is no longer on the page names nobody, so it is left
// out rather than published under an empty name.
func TestOutageWithoutItsEntryIsLeftOut(t *testing.T) {
	p := previewPage(t, "outage")
	p.Outages = append(p.Outages, Outage{Key: "gone", StartedAt: p.GeneratedAt.Add(-time.Hour)})
	v := newView(p, time.UTC, "", &english)
	if len(v.Outages) != 2 {
		t.Errorf("outages = %d, want the 2 with a named entry", len(v.Outages))
	}
}
