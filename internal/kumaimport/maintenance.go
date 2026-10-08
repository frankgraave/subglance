package kumaimport

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Kuma's zones must resolve in a minimal container too.
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/configfile"
)

// The bounds SubGlance puts on a maintenance window, restated like the
// monitor bounds in convert.go so that a converted window passes the dry run.
const (
	// maxWindows is the most windows one instance keeps, so no file can
	// bring more.
	maxWindows = 200
	// maxWindowName is in bytes, as the store counts it.
	maxWindowName = 120
	// maxWeeklyMinutes is the longest a weekly window lasts.
	maxWeeklyMinutes = 24 * 60
	// maxOneOff is the longest a one-off window lasts.
	maxOneOff = 366 * 24 * time.Hour
)

// kumaServerZone is the time zone value with which a Kuma window follows the
// zone of Kuma's server, set under Settings, General as serverTimezone.
const kumaServerZone = "SAME_AS_SERVER"

// windowDate is how a date in the report is written: the wall-clock time in
// the window's own zone, as Kuma's form showed it.
const windowDate = "2 Jan 2006 15:04"

// convertWindows turns Kuma's maintenance windows into SubGlance windows,
// after the monitors, whose keys they refer to.
//
// Kuma schedules a window once, every day, on weekdays, every few days, on
// days of the month, by a cron expression or by hand, and attaches it to any
// number of monitors, groups among them. A SubGlance window is one-off or
// weekly and covers one monitor or one tag pair. So a Kuma window becomes one
// window per monitor it covers; a group becomes the group tag its monitors
// were given, for the group and every group inside it, because Kuma holds a
// monitor in maintenance when any group above it is; and a schedule that has
// no one-off or weekly equivalent is left out with the reason.
//
// Kuma runs a window only while it is switched on and inside its date range.
// One that can never run again is left out, rather than imported as a window
// that does something Kuma no longer did.
func convertWindows(src source, monitorKeys map[int64]string, res *Result) {
	names := map[int64]string{}
	groups := map[int64]string{}
	children := map[int64][]int64{}
	sameName := map[string]int{}
	for _, m := range src.monitors {
		id := int64(m.int("id"))
		names[id] = strings.TrimSpace(m.str("name"))
		if names[id] == "" {
			names[id] = "Kuma monitor " + m.str("id")
		}
		if m.str("type") == "group" {
			groups[id] = strings.TrimSpace(m.str("name"))
			children[int64(m.int("parent"))] = append(children[int64(m.int("parent"))], id)
			if groups[id] != "" {
				sameName[tagValue(groups[id])]++
			}
		}
	}
	targets := map[int64][]int64{}
	for _, l := range src.monitorMaintenance {
		wid := int64(l.int("maintenance_id"))
		targets[wid] = append(targets[wid], int64(l.int("monitor_id")))
	}
	server := serverZone(src.settings)

	for _, w := range src.maintenance {
		name := strings.TrimSpace(w.str("title"))
		if name == "" {
			name = "Kuma maintenance " + w.str("id")
		}
		typ := w.str("strategy")
		skip := func(reason string) {
			res.Skipped = append(res.Skipped, Note{Kind: "maintenance", Name: name, Type: typ, Reason: reason})
		}
		// Notes wait until the window is written: a window that is left
		// out has one reason, not a list of changes it did not get.
		var notes []string

		if !w.bool("active") {
			skip("it is paused in Kuma, and a SubGlance window cannot be paused")
			continue
		}
		zone, loc, zoneNote, reason := windowZone(w, server)
		if reason != "" {
			skip(reason)
			continue
		}
		if zoneNote != "" {
			notes = append(notes, zoneNote)
		}
		window, more, reason := windowSchedule(w, typ, loc, src.now)
		if reason != "" {
			skip(reason)
			continue
		}
		notes = append(notes, more...)
		if window.StartsAt == nil {
			window.Timezone = zone
		}

		if len(name) > maxWindowName {
			cut := name[:maxWindowName]
			for !utf8.ValidString(cut) {
				cut = cut[:len(cut)-1]
			}
			name = strings.TrimSpace(cut)
			notes = append(notes, "the name was shortened to "+strconv.Itoa(maxWindowName)+" bytes")
		}
		window.Name = name

		var out []configfile.Maintenance
		var missing []string
		seen := map[string]bool{}
		add := func(m configfile.Maintenance, id string) {
			if !seen[id] {
				seen[id] = true
				out = append(out, m)
			}
		}
		for _, mid := range targets[int64(w.int("id"))] {
			if _, isGroup := groups[mid]; isGroup {
				for _, gid := range groupTree(mid, children) {
					if groups[gid] == "" {
						continue // its monitors were given no group tag
					}
					m := window
					m.TagKey = "group"
					m.TagValue = tagValue(groups[gid])
					if sameName[m.TagValue] > 1 && !seen["tag:"+m.TagValue] {
						notes = append(notes, strconv.Itoa(sameName[m.TagValue])+" groups in Kuma are called "+
							strconv.Quote(m.TagValue)+", so the window on the tag group: "+m.TagValue+" covers the monitors of each")
					}
					add(m, "tag:"+m.TagValue)
				}
				continue
			}
			if key, ok := monitorKeys[mid]; ok {
				m := window
				m.Monitor = key
				add(m, "monitor:"+key)
				continue
			}
			label := names[mid]
			if label == "" {
				label = "Kuma monitor " + strconv.FormatInt(mid, 10)
			}
			missing = append(missing, strconv.Quote(label))
		}

		switch {
		case len(out) == 0 && len(missing) == 0:
			skip("it covers no monitors")
			continue
		case len(out) == 0:
			skip("none of the monitors it covers is imported: " + strings.Join(missing, ", "))
			continue
		case len(missing) > 0:
			notes = append(notes, "it also covers "+strings.Join(missing, ", ")+", which "+
				plural(len(missing), "is", "are")+" not imported")
		}
		if room := maxWindows - len(res.Document.Maintenance); len(out) > room {
			if room == 0 {
				skip("SubGlance keeps at most " + strconv.Itoa(maxWindows) + " maintenance windows")
				continue
			}
			notes = append(notes, strconv.Itoa(len(out)-room)+" of the "+strconv.Itoa(len(out))+
				" windows it became were left out: SubGlance keeps at most "+strconv.Itoa(maxWindows))
			out = out[:room]
		}
		if d := strings.TrimSpace(w.str("description")); d != "" {
			notes = append(notes, "its description is not carried over")
		}
		res.Document.Maintenance = append(res.Document.Maintenance, out...)
		res.WindowsConverted++
		for _, n := range notes {
			changed(res, "maintenance", name, typ, n)
		}
	}
}

// groupTree returns a group and every group inside it, at any depth.
func groupTree(id int64, children map[int64][]int64) []int64 {
	out := []int64{}
	seen := map[int64]bool{}
	var walk func(int64)
	walk = func(g int64) {
		if seen[g] {
			return // a cycle Kuma should not allow, but would loop here
		}
		seen[g] = true
		out = append(out, g)
		for _, c := range children[g] {
			walk(c)
		}
	}
	walk(id)
	return out
}

// serverZone is the zone set under Kuma's Settings, General, or "" when
// none was. Kuma stores every setting as JSON.
func serverZone(settings []row) string {
	for _, s := range settings {
		if s.str("key") != "serverTimezone" {
			continue
		}
		var zone string
		if err := json.Unmarshal([]byte(s.str("value")), &zone); err != nil {
			zone = s.str("value")
		}
		return strings.TrimSpace(zone)
	}
	return ""
}

// windowZone returns the IANA zone a Kuma window runs in, with a note when it
// had to be assumed, or the reason it cannot be used.
func windowZone(w row, server string) (zone string, loc *time.Location, note, reason string) {
	zone = strings.TrimSpace(w.str("timezone"))
	if zone == "" || zone == kumaServerZone {
		zone = server
		if zone == "" {
			zone = "UTC"
			note = "it follows Kuma's server time zone, which was never set in Kuma's settings, so UTC is assumed: " +
				"check the times if Kuma ran in another zone"
		}
	}
	if zone == "Local" {
		return "", nil, "", "its time zone is the host's own, which has no name"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", nil, "", "its time zone " + strconv.Quote(zone) + " is not an IANA time zone name"
	}
	return zone, loc, note, ""
}

// windowSchedule returns the schedule of a SubGlance window for a Kuma
// window, with notes on what changed, or the reason it has none. A one-off
// window comes back with StartsAt set, a weekly one without.
func windowSchedule(w row, strategy string, loc *time.Location, now time.Time) (configfile.Maintenance, []string, string) {
	var m configfile.Maintenance
	if strategy == "single" {
		start, okStart := w.wallClock("start_date", loc)
		end, okEnd := w.wallClock("end_date", loc)
		switch {
		case !okStart || !okEnd:
			return m, nil, "it has no start and end date that can be read"
		case !end.After(start):
			return m, nil, "its end is not after its start"
		case !end.After(now):
			return m, nil, "it ended on " + end.Format(windowDate)
		case end.Sub(start) > maxOneOff:
			return m, nil, "it lasts longer than 366 days, the longest one-off window SubGlance keeps"
		}
		start, end = start.UTC(), end.UTC()
		m.StartsAt, m.EndsAt = &start, &end
		return m, nil, ""
	}

	var days []int
	var clock string
	var minutes int
	switch strategy {
	case "recurring-weekday", "recurring-interval":
		if strategy == "recurring-weekday" {
			var ok bool
			if days, ok = kumaWeekdays(w.str("weekdays")); !ok {
				return m, nil, "its weekdays cannot be read"
			}
			if len(days) == 0 {
				return m, nil, "it has no weekdays, so Kuma never ran it"
			}
		} else {
			switch n := w.int("interval_day"); {
			case n == 1:
				days = []int{0, 1, 2, 3, 4, 5, 6}
			case n > 1:
				return m, nil, "it repeats every " + strconv.Itoa(n) + " days, and a SubGlance window repeats weekly"
			default:
				return m, nil, "its interval in days cannot be read"
			}
		}
		// Kuma reads both times as HH:mm and counts the window past
		// midnight when the end is earlier than the start.
		sh, sm, ok1 := kumaClock(w.str("start_time"))
		eh, em, ok2 := kumaClock(w.str("end_time"))
		if !ok1 || !ok2 {
			return m, nil, "its start or end time cannot be read"
		}
		clock = twoDigits(sh) + ":" + twoDigits(sm)
		minutes = (eh*60 + em) - (sh*60 + sm)
		if minutes < 0 {
			minutes += 24 * 60
		}
		if minutes == 0 {
			return m, nil, "it starts and ends at the same time, so Kuma never held it open"
		}
	case "cron":
		expr := strings.TrimSpace(w.str("cron"))
		d, hour, minute, ok := weeklyCron(expr)
		if !ok {
			return m, nil, "its cron expression " + strconv.Quote(expr) +
				" does not start at one time of day on chosen weekdays, which is what a SubGlance window repeats on"
		}
		days, clock = d, twoDigits(hour)+":"+twoDigits(minute)
		seconds := w.int("duration")
		if seconds <= 0 {
			return m, nil, "it has no duration"
		}
		minutes = (seconds + 59) / 60
		if minutes > maxWeeklyMinutes {
			return m, nil, "it lasts " + strconv.Itoa(minutes) + " minutes, and a weekly window lasts at most 24 hours"
		}
	case "manual":
		return m, nil, "it is switched on and off by hand, and a SubGlance window runs on a schedule"
	case "recurring-day-of-month":
		return m, nil, "it repeats on days of the month, and a SubGlance window repeats on days of the week"
	default:
		return m, nil, "Kuma's schedule " + strconv.Quote(strategy) + " has no counterpart"
	}

	// A recurring Kuma window runs only inside its date range. A weekly
	// window has none, so a range that is over leaves nothing to import,
	// and one that is not is reported.
	var notes []string
	var span []string
	if w.present("start_date") {
		start, ok := w.wallClock("start_date", loc)
		if !ok {
			return m, nil, "its date range cannot be read"
		}
		if start.After(now) {
			span = append(span, "from "+start.Format(windowDate))
		}
	}
	if w.present("end_date") {
		end, ok := w.wallClock("end_date", loc)
		if !ok {
			return m, nil, "its date range cannot be read"
		}
		if !end.After(now) {
			return m, nil, "its date range ended on " + end.Format(windowDate)
		}
		span = append(span, "until "+end.Format(windowDate))
	}
	if len(span) > 0 {
		notes = append(notes, "Kuma ran it only "+strings.Join(span, " ")+
			"; a weekly window has no date range, so it applies from the import on, without an end")
	}
	m.Weekdays, m.LocalTime, m.DurationMinutes = days, clock, minutes
	return m, notes, ""
}

// kumaWeekdays reads Kuma's weekdays column, a JSON list of numbers from
// Sunday = 0, the numbering SubGlance uses. 7 is Sunday too, as in cron.
func kumaWeekdays(s string) ([]int, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, true
	}
	var raw []any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, false
	}
	set := map[int]bool{}
	for _, v := range raw {
		var d int
		switch x := v.(type) {
		case float64:
			d = int(x)
			if float64(d) != x {
				return nil, false
			}
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(x))
			if err != nil {
				return nil, false
			}
			d = n
		default:
			return nil, false
		}
		if d == 7 {
			d = 0
		}
		if d < 0 || d > 6 {
			return nil, false
		}
		set[d] = true
	}
	return sortedDays(set), true
}

// cronDays are the day names cron accepts, from Sunday = 0.
var cronDays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// weeklyCron reads a cron expression that starts at one time of day on
// chosen weekdays: minute and hour each one number, any day of the month,
// any month, and days of the week as *, numbers, names, ranges or a list of
// them. That is Kuma's default, "30 3 * * *", and every schedule Kuma builds
// from its weekday form. A six-field expression is accepted when its seconds
// are 0. Anything else, a step or a list of hours, repeats in a way a weekly
// window cannot.
func weeklyCron(expr string) (days []int, hour, minute int, ok bool) {
	f := strings.Fields(expr)
	if len(f) == 6 {
		if f[0] != "0" {
			return nil, 0, 0, false
		}
		f = f[1:]
	}
	if len(f) != 5 {
		return nil, 0, 0, false
	}
	minute, okMin := cronNumber(f[0], 59)
	hour, okHour := cronNumber(f[1], 23)
	if !okMin || !okHour || !anyField(f[2]) || !anyField(f[3]) {
		return nil, 0, 0, false
	}
	if anyField(f[4]) {
		return []int{0, 1, 2, 3, 4, 5, 6}, hour, minute, true
	}
	set := map[int]bool{}
	for _, item := range strings.Split(f[4], ",") {
		lo, hi, isRange := strings.Cut(item, "-")
		from, okFrom := cronDay(lo)
		to := from
		okTo := true
		if isRange {
			to, okTo = cronDay(hi)
		}
		if !okFrom || !okTo || to < from {
			return nil, 0, 0, false
		}
		for d := from; d <= to; d++ {
			set[d%7] = true
		}
	}
	return sortedDays(set), hour, minute, true
}

func anyField(s string) bool { return s == "*" || s == "?" }

// cronNumber reads a field that is one plain number from 0 to max.
func cronNumber(s string, max int) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n <= max
}

// cronDay reads one day of the week, 0 to 7 or a name; 7 is Sunday again.
func cronDay(s string) (int, bool) {
	if d, ok := cronDays[strings.ToLower(s)]; ok {
		return d, true
	}
	return cronNumber(s, 7)
}

func sortedDays(set map[int]bool) []int {
	days := make([]int, 0, len(set))
	for d := range set {
		days = append(days, d)
	}
	sort.Ints(days)
	return days
}

// kumaClock reads one of Kuma's times of day, "HH:mm" with optional seconds.
// Kuma schedules on hours and minutes, so the seconds are ignored as Kuma
// ignores them.
func kumaClock(s string) (hour, minute int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, false
	}
	h, okH := cronNumber(parts[0], 23)
	m, okM := cronNumber(parts[1], 59)
	return h, m, okH && okM
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// present reports whether a column holds a value other than NULL or "".
func (r row) present(k string) bool {
	return r[k] != nil && strings.TrimSpace(r.str(k)) != ""
}

// wallClock reads one of Kuma's dates as the wall-clock time it was typed as
// and places it in loc. Kuma stores the value of a datetime-local field,
// "2026-03-01T22:00", and means it in the window's zone. The driver hands a
// DATETIME column over as a time.Time in UTC with those same figures when it
// recognises the layout, so both forms are read by their figures.
func (r row) wallClock(k string, loc *time.Location) (time.Time, bool) {
	var t time.Time
	switch v := r[k].(type) {
	case time.Time:
		t = v
	case string, []byte:
		s := strings.TrimSpace(r.str(k))
		ok := false
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04",
			"2006-01-02 15:04:05", "2006-01-02"} {
			if p, err := time.Parse(layout, s); err == nil {
				t, ok = p, true
				break
			}
		}
		if !ok {
			return time.Time{}, false
		}
	default:
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), true
}
