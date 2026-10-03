package statuspage

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"
)

//go:embed page.gohtml
var pageTemplate string

// themeScript picks the theme before the first paint, from the visitor's
// own preference (design §2: the page follows prefers-color-scheme and has
// no toggle). It is the page's only script. Without it the page would paint
// in the dark default and flip, because the tokens are keyed to a
// data-theme attribute rather than to the media query.
const themeScript = `(function(){var q=matchMedia("(prefers-color-scheme: light)");function s(){document.documentElement.setAttribute("data-theme",q.matches?"light":"dark")}s();q.addEventListener("change",s)})();`

// Renderer turns a public Page into the HTML document a visitor sees.
//
// It reads nothing but the Page, so the HTML can hold no more than the JSON
// answer does: the allowlist in public.go is the limit for both.
type Renderer struct {
	tmpl *template.Template
	css  template.CSS
	csp  string
}

// FontPath is where a status page expects its two faces, relative to the
// document. Served at /status/acme, the page resolves it to
// /status/fonts/<file>. Proxied to the root of status.example.com (with the
// proxy passing / to /status/acme/), it resolves to /fonts/<file> there,
// which reaches this server as /status/acme/fonts/<file>. The public routes
// serve the faces under both, which is what "relative asset paths" in design
// §3.3 comes to, and one reason `fonts` is a reserved slug.
const FontPath = "fonts/"

// NewRenderer prepares the page template with the page's stylesheet, which
// the frontend build writes to webui's status-page.css.
//
// The stylesheet is inlined rather than linked, so a page needs one request
// and one asset route (its fonts) wherever it is served. Its font URLs are
// made relative on the way in: the build writes them as /fonts/, which is
// right for the dashboard and wrong for a page behind a path prefix.
func NewRenderer(css []byte) (*Renderer, error) {
	if bytes.Contains(bytes.ToLower(css), []byte("</style")) {
		return nil, errors.New("status page stylesheet contains a closing style tag")
	}
	css = relativeFonts(css)
	tmpl, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse status page template: %w", err)
	}
	return &Renderer{
		tmpl: tmpl,
		// The build's own output, never visitor input.
		css: template.CSS(css), //nolint:gosec // G203: trusted, embedded at build time
		csp: pagePolicy(css),
	}, nil
}

// ContentSecurityPolicy is the policy that fits the rendered document: the
// inline stylesheet and the theme script are allowed by hash and nothing
// else is allowed to run, load or frame the page. Fonts come from this
// origin, at FontPath beside the page.
func (r *Renderer) ContentSecurityPolicy() string { return r.csp }

// relativeFonts rewrites the stylesheet's root-relative font URLs, in either
// quoting, to FontPath.
func relativeFonts(css []byte) []byte {
	for _, prefix := range []string{"url(", `url("`, "url('"} {
		css = bytes.ReplaceAll(css, []byte(prefix+"/fonts/"), []byte(prefix+FontPath))
	}
	return css
}

func pagePolicy(css []byte) string {
	return strings.Join([]string{
		"default-src 'none'",
		"style-src " + hashSource(css),
		"script-src " + hashSource([]byte(themeScript)),
		"font-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

func hashSource(b []byte) string {
	sum := sha256.Sum256(b)
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// Render writes page as HTML, with times in the page's time zone.
func (r *Renderer) Render(page Page) ([]byte, error) {
	loc, err := time.LoadLocation(page.Timezone)
	if err != nil {
		return nil, fmt.Errorf("status page time zone %q: %w", page.Timezone, err)
	}
	var buf bytes.Buffer
	if err := r.tmpl.Execute(&buf, newView(page, loc, r.css)); err != nil {
		return nil, fmt.Errorf("render status page: %w", err)
	}
	return buf.Bytes(), nil
}

// view is Page in the words and formats the template prints. Every string a
// visitor reads is decided here, where a test can read it, rather than in
// the template.
type view struct {
	Title, Description, Timezone string
	Updated, UpdatedISO          string
	Summary                      summaryView
	Maintenance                  []maintenanceView
	Services                     []serviceView
	Outages                      []outageView
	CSS                          template.CSS
	ThemeScript                  template.JS
}

type summaryView struct{ Lamp, Word, Text string }

type maintenanceView struct{ Label, When, Affects string }

type serviceView struct {
	Name, Lamp, Word, Rail string
	InMaintenance          bool
	Days                   []string
	History                string
	Oldest, OldestPhone    string
	Uptime, UptimePhone    string
}

type outageView struct {
	Name, Text string
	Ongoing    bool
}

// phoneDays is how many days the history shows on a phone (design §2). The
// stylesheet hides the older bars; the axis label and the uptime figure have
// to say the same.
const phoneDays = RecentDays

func newView(p Page, loc *time.Location, css template.CSS) view {
	now := p.GeneratedAt.In(loc)
	names := make(map[string]string, len(p.Entries))
	for _, e := range p.Entries {
		names[e.Key] = e.Name
	}
	v := view{
		Title:       p.Title,
		Description: p.Description,
		Timezone:    p.Timezone,
		Updated:     updated(now),
		UpdatedISO:  p.GeneratedAt.UTC().Format(time.RFC3339),
		Summary:     summarise(p.Entries),
		CSS:         css,
		// A constant of this file, never visitor input.
		ThemeScript: template.JS(themeScript), //nolint:gosec // G203: constant
	}
	for _, m := range p.Maintenance {
		v.Maintenance = append(v.Maintenance, maintenanceRow(m, now, names))
	}
	for _, e := range p.Entries {
		v.Services = append(v.Services, serviceRow(e))
	}
	for _, o := range p.Outages {
		name, ok := names[o.Key]
		if !ok {
			continue
		}
		v.Outages = append(v.Outages, outageRow(o, name, now))
	}
	return v
}

// lamps maps a public status to the lamp state and the word beside it. The
// words are design §1.3's; the lamp states are led.css's.
var lamps = map[Status][2]string{
	StatusUp:           {"up", "Up"},
	StatusDegraded:     {"warn", "Degraded"},
	StatusDown:         {"down", "Down"},
	StatusNoData:       {"idle", "No data yet"},
	StatusNotMonitored: {"off", "Not monitored"},
}

// summarise states the page's state as a count, never as an adjective like
// "major outage", which is a judgement the page cannot make for the
// operator (design §2).
func summarise(entries []Entry) summaryView {
	total := len(entries)
	if total == 0 {
		return summaryView{Lamp: "idle", Word: "No data yet", Text: "No services on this page yet."}
	}
	var up, degraded, down, noData, off int
	for _, e := range entries {
		switch e.Status {
		case StatusUp:
			up++
		case StatusDegraded:
			degraded++
		case StatusDown:
			down++
		case StatusNoData:
			noData++
		default:
			off++
		}
	}
	switch {
	case down > 0:
		return summaryView{"down", "Down", ofTotal(down, total, "down")}
	case degraded > 0:
		return summaryView{"warn", "Degraded", ofTotal(degraded, total, "degraded")}
	case up == total && total == 1:
		return summaryView{"up", "Up", "The service is up"}
	case up == total:
		return summaryView{"up", "Up", fmt.Sprintf("All %d services are up", total)}
	case up > 0:
		return summaryView{"up", "Up", ofTotal(up, total, "up") + quiet(noData, off)}
	default:
		return summaryView{"idle", "No data yet", strings.TrimPrefix(quiet(noData, off), " · ")}
	}
}

// ofTotal reads "1 of 5 services is down" or "2 of 5 services are down".
func ofTotal(n, total int, state string) string {
	noun := "services"
	if total == 1 {
		noun = "service"
	}
	verb := "are"
	if n == 1 {
		verb = "is"
	}
	return fmt.Sprintf("%d of %d %s %s %s", n, total, noun, verb, state)
}

// quiet names the services that say nothing about their state right now.
func quiet(noData, off int) string {
	var parts []string
	if noData > 0 {
		parts = append(parts, fmt.Sprintf("%d with no data yet", noData))
	}
	if off > 0 {
		parts = append(parts, fmt.Sprintf("%d not monitored", off))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// dayMarks maps a day's state to the bar's data-s value in the stylesheet.
var dayMarks = map[DayState]string{
	DayUp:       "up",
	DayDegraded: "warn",
	DayDown:     "down",
	DayNoData:   "none",
}

func serviceRow(e Entry) serviceView {
	lamp := lamps[e.Status]
	if lamp[0] == "" {
		lamp = lamps[StatusNoData]
	}
	s := serviceView{
		Name:          e.Name,
		Lamp:          lamp[0],
		Word:          lamp[1],
		InMaintenance: e.InMaintenance,
		Days:          make([]string, len(e.Days)),
		History:       historyText(e.Days),
		Oldest:        fmt.Sprintf("%d days ago", len(e.Days)),
		OldestPhone:   fmt.Sprintf("%d days ago", min(phoneDays, len(e.Days))),
		Uptime:        uptimeText(e.Uptime90d, HistoryDays, false),
		UptimePhone:   uptimeText(e.Uptime30d, phoneDays, e.Uptime90d != nil),
	}
	switch e.Status {
	case StatusDown:
		s.Rail = "down"
	case StatusDegraded:
		s.Rail = "warn"
	}
	for i, d := range e.Days {
		s.Days[i] = dayMarks[d.State]
		if s.Days[i] == "" {
			s.Days[i] = "none"
		}
	}
	return s
}

// uptimeText is an uptime figure with the period it covers, which is the
// period of the bar drawn above it: 90 days, or 30 on a phone. older says
// there is data from before that period, so an empty one is not "yet": a
// monitor paused a month ago has a 90-day figure and no 30-day one.
func uptimeText(pct *float64, days int, older bool) string {
	switch {
	case pct != nil:
		return fmt.Sprintf("%.2f%% uptime, %d days", *pct, days)
	case older:
		return fmt.Sprintf("No uptime data, %d days", days)
	default:
		return "No uptime data yet"
	}
}

// updated is the moment the page was built, always with its date. Every
// other time on the page is relative to it ("today", "tomorrow", "4 h so
// far"), and a page read from a cache or left open in a tab is read on a
// later day than the one it was built on. The server cannot know when it will
// be read, so the one absolute time on the page carries its day.
func updated(now time.Time) string {
	return now.Format("Mon 2 Jan, 15:04")
}

// historyText is the history bar as a sentence. The bars are aria-hidden, so
// this is the whole history for assistive technology: every day counted, and
// when each down and degraded day was (design §1.4).
func historyText(days []Day) string {
	var up, degraded, down, none int
	var downAgo, degradedAgo []string
	for i := len(days) - 1; i >= 0; i-- {
		ago := daysAgo(len(days) - 1 - i)
		switch days[i].State {
		case DayUp:
			up++
		case DayDegraded:
			degraded++
			degradedAgo = append(degradedAgo, ago)
		case DayDown:
			down++
			downAgo = append(downAgo, ago)
		default:
			none++
		}
	}
	text := fmt.Sprintf("Last %d days: %d up, %d degraded, %d down, %d no data.",
		len(days), up, degraded, down, none)
	if len(downAgo) > 0 {
		text += " Down " + strings.Join(downAgo, ", ") + "."
	}
	if len(degradedAgo) > 0 {
		text += " Degraded " + strings.Join(degradedAgo, ", ") + "."
	}
	return text
}

func daysAgo(n int) string {
	switch n {
	case 0:
		return "today"
	case 1:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", n)
	}
}

func maintenanceRow(m Maintenance, now time.Time, names map[string]string) maintenanceView {
	loc := now.Location()
	start, end := m.StartsAt.In(loc), m.EndsAt.In(loc)
	var affects []string
	for _, k := range m.Keys {
		if n, ok := names[k]; ok {
			affects = append(affects, n)
		}
	}
	row := maintenanceView{Label: "Scheduled", Affects: strings.Join(affects, ", ")}
	if !now.Before(start) {
		row.Label = "In progress"
		row.When = "Now, until " + clock(end, now)
		return row
	}
	row.When = span(start, end, now)
	return row
}

func outageRow(o Outage, name string, now time.Time) outageView {
	loc := now.Location()
	start := o.StartedAt.In(loc)
	d := time.Duration(o.DurationS) * time.Second
	if o.ResolvedAt == nil {
		return outageView{
			Name:    name,
			Ongoing: true,
			Text:    "Down since " + clock(start, now) + " · " + duration(d) + " so far",
		}
	}
	return outageView{
		Name: name,
		Text: "Down " + duration(d) + " · " + span(start, o.ResolvedAt.In(loc), now),
	}
}

// day names a calendar day relative to now: "today", "tomorrow", or its date.
func day(t, now time.Time) string {
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return "today"
	case time.Date(ny, nm, nd+1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly) == t.Format(time.DateOnly):
		return "tomorrow"
	case time.Date(ny, nm, nd-1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly) == t.Format(time.DateOnly):
		return "yesterday"
	default:
		return t.Format("Mon 2 Jan")
	}
}

// clock is a time of day, with its day when that is not today:
// "09:41 today", "Fri 19 Sep, 09:41".
func clock(t, now time.Time) string {
	d := day(t, now)
	if d == "today" || d == "tomorrow" || d == "yesterday" {
		return t.Format("15:04") + " " + d
	}
	return d + ", " + t.Format("15:04")
}

// span is a stretch of time: "Tomorrow, 02:00–03:00", or with both days when
// it crosses midnight.
func span(start, end, now time.Time) string {
	sd := day(start, now)
	if start.Format(time.DateOnly) == end.Format(time.DateOnly) {
		return capitalise(sd) + ", " + start.Format("15:04") + "–" + end.Format("15:04")
	}
	return capitalise(sd) + ", " + start.Format("15:04") + " – " + capitalise(day(end, now)) + ", " + end.Format("15:04")
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// duration reads "23 min", "4 h 51 min" or "2 d 3 h". Under a minute is not
// rounded to zero: an outage happened, however short.
func duration(d time.Duration) string {
	if d < time.Minute {
		return "under 1 min"
	}
	minutes := int(math.Floor(d.Minutes()))
	days, hours, mins := minutes/(24*60), minutes/60%24, minutes%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%d d %d h", days, hours)
	case days > 0:
		return fmt.Sprintf("%d d", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%d h %d min", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%d h", hours)
	default:
		return fmt.Sprintf("%d min", mins)
	}
}
