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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	tmpl    *template.Template
	css     template.CSS
	cssHash string
	csp     string
}

// FontPath is where a status page expects its two faces, relative to the
// document. Served at /status/acme, the page resolves it to
// /status/fonts/<file>. Proxied to the root of status.example.com (with the
// proxy passing / to /status/acme/), it resolves to /fonts/<file> there,
// which reaches this server as /status/acme/fonts/<file>. The public routes
// serve the faces under both, which is what "relative asset paths" in design
// §3.3 comes to, and one reason `fonts` is a reserved slug.
const FontPath = "fonts/"

// LogoPath is where a status page expects its logo, relative to the
// document, for the same reason as FontPath: /status/logos/<file> beside
// /status/acme, /status/acme/logos/<file> beside /status/acme/, and
// /logos/<file> at the root of a subdomain proxied to the page. The public
// JSON names it from the root, as LogoRoot plus LogoPath.
const LogoPath = "logos/"

// LogoRoot is the directory the HTML page is served from, which the JSON's
// logo path starts with.
const LogoRoot = "/status/"

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
		css:     template.CSS(css), //nolint:gosec // G203: trusted, embedded at build time
		cssHash: hashSource(css),
		csp:     pagePolicy(css),
	}, nil
}

// ContentSecurityPolicy is the policy that fits a rendered document without
// an accent colour: the inline stylesheet and the theme script are allowed by
// hash and nothing else is allowed to run, load or frame the page. Fonts and
// the logo come from this origin, at FontPath and LogoPath beside the page.
func (r *Renderer) ContentSecurityPolicy() string { return r.csp }

// ContentSecurityPolicyFor is the policy for the document Render writes for
// page. A page with an accent carries a second, one-rule stylesheet, and its
// hash is added to style-src; the policy stays exact rather than allowing
// inline styles in general.
func (r *Renderer) ContentSecurityPolicyFor(page Page) string {
	accent := accentStyle(page.Accent)
	if accent == "" {
		return r.csp
	}
	return strings.Replace(r.csp, "style-src "+r.cssHash, "style-src "+r.cssHash+" "+hashSource([]byte(accent)), 1)
}

// accentStyle is the stylesheet that colours the title with the page's
// accent, or "" when there is none. The accent was checked on save; it is
// checked again here because this text is written into a style element.
func accentStyle(accent string) string {
	if !accentColour.MatchString(accent) {
		return ""
	}
	return ".sp-head h1{color:" + accent + "}"
}

var accentColour = regexp.MustCompile(`^#[0-9a-f]{6}$`)

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
		// The logo, served from this origin beside the page (LogoPath).
		"img-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

func hashSource(b []byte) string {
	sum := sha256.Sum256(b)
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// Render writes page as HTML, with times in the page's time zone and fixed
// texts in its language.
func (r *Renderer) Render(page Page) ([]byte, error) {
	loc, err := time.LoadLocation(page.Timezone)
	if err != nil {
		return nil, fmt.Errorf("status page time zone %q: %w", page.Timezone, err)
	}
	var buf bytes.Buffer
	if err := r.tmpl.Execute(&buf, newView(page, loc, r.css, textsFor(page.Language))); err != nil {
		return nil, fmt.Errorf("render status page: %w", err)
	}
	return buf.Bytes(), nil
}

// view is Page in the words and formats the template prints. Every string a
// visitor reads is decided here, where a test can read it, rather than in
// the template, and every fixed one comes from the page's texts table.
type view struct {
	Lang                         string
	Title, Description, Timezone string
	Updated, UpdatedISO          string
	UpdatedLabel                 string
	Logo                         *logoView
	Summary                      summaryView
	MaintenanceHeading           string
	Maintenance                  []maintenanceView
	ServicesHeading              string
	MaintenanceChip              string
	CertificateChip              string
	TodayAxis                    string
	Services                     []serviceView
	OutagesHeading               string
	NoOutages                    string
	Outages                      []outageView
	Footer                       string
	CSS                          template.CSS
	// AccentCSS is the one-rule stylesheet for the accent, or empty.
	AccentCSS   template.CSS
	ThemeScript template.JS
}

// logoView is the logo as the template draws it: a relative address and
// the intrinsic size, so the browser reserves its box before it loads.
type logoView struct {
	Src           string
	Width, Height int
}

type summaryView struct{ Lamp, Word, Text string }

type maintenanceView struct{ Label, When, Affects string }

type serviceView struct {
	Name, Lamp, Word, Rail string
	InMaintenance          bool
	CertificateExpiring    bool
	Days                   []string
	History                string
	Oldest, OldestPhone    string
	Uptime, UptimePhone    string
}

type outageView struct {
	Name, Text string
	Word       string
	Ongoing    bool
}

// phoneDays is how many days the history shows on a phone (design §2). The
// stylesheet hides the older bars; the axis label and the uptime figure have
// to say the same.
const phoneDays = RecentDays

// outageDays is the outage list's window in days, for its heading.
const outageDays = int(OutageWindow / (24 * time.Hour))

func newView(p Page, loc *time.Location, css template.CSS, tx *texts) view {
	now := p.GeneratedAt.In(loc)
	names := make(map[string]string, len(p.Entries))
	for _, e := range p.Entries {
		names[e.Key] = e.Name
	}
	v := view{
		Lang:               tx.Lang,
		Title:              p.Title,
		Description:        p.Description,
		Timezone:           p.Timezone,
		Updated:            tx.updated(now),
		UpdatedISO:         p.GeneratedAt.UTC().Format(time.RFC3339),
		UpdatedLabel:       tx.Updated,
		Summary:            summarise(p.Entries, tx),
		MaintenanceHeading: tx.Maintenance,
		ServicesHeading:    fmt.Sprintf(tx.ServicesHeading, len(p.Entries)),
		MaintenanceChip:    tx.Maintenance,
		CertificateChip:    tx.CertificateExpiring,
		TodayAxis:          tx.TodayAxis,
		OutagesHeading:     fmt.Sprintf(tx.PastOutages, outageDays),
		NoOutages:          fmt.Sprintf(tx.NoOutages, outageDays),
		Footer:             fmt.Sprintf(tx.TimesIn, p.Timezone),
		CSS:                css,
		// Built from a colour checked against a fixed pattern, never from
		// free visitor input.
		AccentCSS: template.CSS(accentStyle(p.Accent)), //nolint:gosec // G203: validated #rrggbb only
		// A constant of this file, never visitor input.
		ThemeScript: template.JS(themeScript), //nolint:gosec // G203: constant
	}
	if p.CreditShown {
		v.Footer += " · " + tx.Credit
	}
	if p.Logo != nil && strings.HasPrefix(p.Logo.Path, LogoRoot+LogoPath) {
		v.Logo = &logoView{Src: strings.TrimPrefix(p.Logo.Path, LogoRoot), Width: p.Logo.Width, Height: p.Logo.Height}
	}
	for _, m := range p.Maintenance {
		v.Maintenance = append(v.Maintenance, maintenanceRow(m, now, names, tx))
	}
	for _, e := range p.Entries {
		v.Services = append(v.Services, serviceRow(e, tx))
	}
	for _, o := range p.Outages {
		name, ok := names[o.Key]
		if !ok {
			continue
		}
		v.Outages = append(v.Outages, outageRow(o, name, now, tx))
	}
	return v
}

// lamp maps a public status to the lamp state and the word beside it. The
// words are design §1.3's, in the page's language; the lamp states are
// led.css's.
func (tx *texts) lamp(s Status) (state, word string) {
	switch s {
	case StatusUp:
		return "up", tx.Up
	case StatusDegraded:
		return "warn", tx.Degraded
	case StatusDown:
		return "down", tx.Down
	case StatusNotMonitored:
		return "off", tx.NotMonitored
	default:
		return "idle", tx.NoData
	}
}

// summarise states the page's state as a count, never as an adjective like
// "major outage", which is a judgement the page cannot make for the
// operator (design §2).
func summarise(entries []Entry, tx *texts) summaryView {
	total := len(entries)
	if total == 0 {
		return summaryView{Lamp: "idle", Word: tx.NoData, Text: tx.NoServices}
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
		return summaryView{"down", tx.Down, tx.ofTotal(down, total, tx.DownOne, tx.DownMany)}
	case degraded > 0:
		return summaryView{"warn", tx.Degraded, tx.ofTotal(degraded, total, tx.DegradedOne, tx.DegradedMany)}
	case up == total && total == 1:
		return summaryView{"up", tx.Up, tx.OneUp}
	case up == total:
		return summaryView{"up", tx.Up, fmt.Sprintf(tx.AllUp, total)}
	case up > 0:
		return summaryView{"up", tx.Up, tx.ofTotal(up, total, tx.UpOne, tx.UpMany) + tx.quiet(noData, off)}
	default:
		return summaryView{"idle", tx.NoData, strings.TrimPrefix(tx.quiet(noData, off), " · ")}
	}
}

// ofTotal reads "1 of 5 services is down" or "2 of 5 services are down":
// one is the sentence for a count of one, many for any other.
func (tx *texts) ofTotal(n, total int, one, many string) string {
	noun := tx.ServiceMany
	if total == 1 {
		noun = tx.ServiceOne
	}
	format := many
	if n == 1 {
		format = one
	}
	return fmt.Sprintf(format, n, total, noun)
}

// quiet names the services that say nothing about their state right now.
func (tx *texts) quiet(noData, off int) string {
	var parts []string
	if noData > 0 {
		parts = append(parts, fmt.Sprintf(tx.WithNoData, noData))
	}
	if off > 0 {
		parts = append(parts, fmt.Sprintf(tx.NotMonitoredCount, off))
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

func serviceRow(e Entry, tx *texts) serviceView {
	lamp, word := tx.lamp(e.Status)
	s := serviceView{
		Name:          e.Name,
		Lamp:          lamp,
		Word:          word,
		InMaintenance: e.InMaintenance,
		// The note, not the lamp: the service is up.
		CertificateExpiring: e.CertificateExpiring,
		Days:                make([]string, len(e.Days)),
		History:             tx.historyText(e.Days),
		Oldest:              tx.daysAgo(len(e.Days)),
		OldestPhone:         tx.daysAgo(min(phoneDays, len(e.Days))),
		Uptime:              tx.uptimeText(e.Uptime90d, HistoryDays, false),
		UptimePhone:         tx.uptimeText(e.Uptime30d, phoneDays, e.Uptime90d != nil),
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

// uptimeDecimals is how many decimals an uptime figure carries: the
// dashboard's formatUptime precision, which web/src/format/format.guard.test.ts
// holds this constant to.
const uptimeDecimals = 2

// uptimeText is an uptime figure with the period it covers, which is the
// period of the bar drawn above it: 90 days, or 30 on a phone. older says
// there is data from before that period, so an empty one is not "yet": a
// monitor paused a month ago has a 90-day figure and no 30-day one.
func (tx *texts) uptimeText(pct *float64, days int, older bool) string {
	switch {
	case pct != nil:
		figure := strings.Replace(strconv.FormatFloat(*pct, 'f', uptimeDecimals, 64), ".", tx.DecimalSeparator, 1) + "%"
		return fmt.Sprintf(tx.Uptime, figure, days)
	case older:
		return fmt.Sprintf(tx.NoUptimeOlder, days)
	default:
		return tx.NoUptimeYet
	}
}

// updated is the moment the page was built, always with its date. Every
// other time on the page is relative to it ("today", "tomorrow", "4 h so
// far"), and a page read from a cache or left open in a tab is read on a
// later day than the one it was built on. The server cannot know when it will
// be read, so the one absolute time on the page carries its day.
func (tx *texts) updated(now time.Time) string {
	return tx.date(now) + ", " + now.Format("15:04")
}

// date writes a calendar day as "Mon 2 Jan", in the page's language.
func (tx *texts) date(t time.Time) string {
	return tx.Weekdays[t.Weekday()] + " " + strconv.Itoa(t.Day()) + " " + tx.Months[t.Month()-1]
}

// historyText is the history bar as a sentence. The bars are aria-hidden, so
// this is the whole history for assistive technology: every day counted, and
// when each down and degraded day was (design §1.4).
func (tx *texts) historyText(days []Day) string {
	var up, degraded, down, none int
	var downAgo, degradedAgo []string
	for i := len(days) - 1; i >= 0; i-- {
		ago := tx.relativeDay(len(days) - 1 - i)
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
	text := fmt.Sprintf(tx.History, len(days), up, degraded, down, none)
	if len(downAgo) > 0 {
		text += fmt.Sprintf(tx.HistoryDown, strings.Join(downAgo, ", "))
	}
	if len(degradedAgo) > 0 {
		text += fmt.Sprintf(tx.HistoryDegraded, strings.Join(degradedAgo, ", "))
	}
	return text
}

// relativeDay is "today", "1 day ago" or "12 days ago".
func (tx *texts) relativeDay(n int) string {
	if n == 0 {
		return tx.Today
	}
	return tx.daysAgo(n)
}

func (tx *texts) daysAgo(n int) string {
	if n == 1 {
		return tx.DayAgo
	}
	return fmt.Sprintf(tx.DaysAgo, n)
}

func maintenanceRow(m Maintenance, now time.Time, names map[string]string, tx *texts) maintenanceView {
	loc := now.Location()
	start, end := m.StartsAt.In(loc), m.EndsAt.In(loc)
	var affects []string
	for _, k := range m.Keys {
		if n, ok := names[k]; ok {
			affects = append(affects, n)
		}
	}
	row := maintenanceView{Label: tx.Scheduled}
	if len(affects) > 0 {
		row.Affects = fmt.Sprintf(tx.Affects, strings.Join(affects, ", "))
	}
	if !now.Before(start) {
		row.Label = tx.InProgress
		row.When = fmt.Sprintf(tx.NowUntil, tx.clock(end, now))
		return row
	}
	row.When = tx.span(start, end, now)
	return row
}

func outageRow(o Outage, name string, now time.Time, tx *texts) outageView {
	loc := now.Location()
	start := o.StartedAt.In(loc)
	d := time.Duration(o.DurationS) * time.Second
	if o.ResolvedAt == nil {
		return outageView{
			Name:    name,
			Word:    tx.Down,
			Ongoing: true,
			Text:    fmt.Sprintf(tx.DownSince, tx.clock(start, now), tx.duration(d)),
		}
	}
	return outageView{
		Name: name,
		Word: tx.Recovered,
		Text: fmt.Sprintf(tx.DownFor, tx.duration(d), tx.span(start, o.ResolvedAt.In(loc), now)),
	}
}

// day names a calendar day relative to now: "today", "tomorrow",
// "yesterday", or its date.
func (tx *texts) day(t, now time.Time) string {
	ny, nm, nd := now.Date()
	switch t.Format(time.DateOnly) {
	case now.Format(time.DateOnly):
		return tx.Today
	case time.Date(ny, nm, nd+1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly):
		return tx.Tomorrow
	case time.Date(ny, nm, nd-1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly):
		return tx.Yesterday
	default:
		return tx.date(t)
	}
}

// relative reports whether day returned a word rather than a date.
func (tx *texts) relative(d string) bool {
	return d == tx.Today || d == tx.Tomorrow || d == tx.Yesterday
}

// clock is a time of day, with its day when that is not today:
// "09:41 today", "Fri 19 Sep, 09:41".
func (tx *texts) clock(t, now time.Time) string {
	d := tx.day(t, now)
	if tx.relative(d) {
		return t.Format("15:04") + " " + d
	}
	return d + ", " + t.Format("15:04")
}

// span is a stretch of time: "Tomorrow, 02:00–03:00", or with both days when
// it crosses midnight.
func (tx *texts) span(start, end, now time.Time) string {
	sd := capitalise(tx.day(start, now))
	if start.Format(time.DateOnly) == end.Format(time.DateOnly) {
		return sd + ", " + start.Format("15:04") + "–" + end.Format("15:04")
	}
	return sd + ", " + start.Format("15:04") + " – " + capitalise(tx.day(end, now)) + ", " + end.Format("15:04")
}

func capitalise(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+utf8.RuneLen(r):]
	}
	return s
}

// duration reads "23 min", "4 h 51 min" or "2 d 3 h". Under a minute is not
// rounded to zero: an outage happened, however short.
func (tx *texts) duration(d time.Duration) string {
	if d < time.Minute {
		return tx.UnderAMinute
	}
	minutes := int(math.Floor(d.Minutes()))
	days, hours, mins := minutes/(24*60), minutes/60%24, minutes%60
	unit := func(n int, u string) string { return strconv.Itoa(n) + " " + u }
	switch {
	case days > 0 && hours > 0:
		return unit(days, tx.DayUnit) + " " + unit(hours, tx.HourUnit)
	case days > 0:
		return unit(days, tx.DayUnit)
	case hours > 0 && mins > 0:
		return unit(hours, tx.HourUnit) + " " + unit(mins, tx.MinUnit)
	case hours > 0:
		return unit(hours, tx.HourUnit)
	default:
		return unit(mins, tx.MinUnit)
	}
}
