package kumaimport

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"
)

// windowNow is the instant the window tests judge "ended" against.
var windowNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func windowRow(over row) row {
	r := row{"id": int64(1), "title": "Deploy", "description": "", "active": int64(1), "strategy": "recurring-weekday",
		"start_date": nil, "end_date": nil, "start_time": "02:00", "end_time": "03:00", "weekdays": "[1,2,3,4,5]",
		"days_of_month": "[]", "interval_day": int64(1), "cron": "0 2 * * 1,2,3,4,5", "timezone": "Europe/Amsterdam",
		"duration": int64(3600)}
	for k, v := range over {
		r[k] = v
	}
	return r
}

// windowSource is one imported monitor, "Shop" (id 1), one that is not
// imported, "Docker" (id 2), and a group "Edge" (id 3) holding a group
// "Edge EU" (id 4), with the given windows on the given monitor ids.
func windowSource(windows []row, links map[int64][]int64, settings ...row) (source, map[int64]string) {
	src := source{now: windowNow, settings: settings, maintenance: windows, monitors: []row{
		{"id": int64(1), "name": "Shop", "type": "http"},
		{"id": int64(2), "name": "Docker", "type": "docker"},
		{"id": int64(3), "name": "Edge", "type": "group"},
		{"id": int64(4), "name": "Edge EU", "type": "group", "parent": int64(3)},
		{"id": int64(5), "name": "CDN", "type": "http", "parent": int64(4)},
	}}
	link := int64(0)
	for _, w := range windows {
		wid := int64(w.int("id"))
		for _, mid := range links[wid] {
			link++
			src.monitorMaintenance = append(src.monitorMaintenance,
				row{"id": link, "maintenance_id": wid, "monitor_id": mid})
		}
	}
	return src, map[int64]string{1: "shop", 5: "cdn"}
}

func convertOne(w row, targets ...int64) Result {
	if len(targets) == 0 {
		targets = []int64{1}
	}
	src, keys := windowSource([]row{w}, map[int64][]int64{int64(w.int("id")): targets})
	var res Result
	convertWindows(src, keys, &res)
	return res
}

func at(s string, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic(err)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// Each of Kuma's schedules that has a one-off or weekly counterpart becomes
// that window, with the same times in the same zone.
func TestWindowSchedulesComeOver(t *testing.T) {
	weekly := func(days []int, clock string, minutes int) configfile.Maintenance {
		return configfile.Maintenance{Name: "Deploy", Monitor: "shop", Timezone: "Europe/Amsterdam",
			Weekdays: days, LocalTime: clock, DurationMinutes: minutes}
	}
	all := []int{0, 1, 2, 3, 4, 5, 6}
	cases := []struct {
		name string
		row  row
		want configfile.Maintenance
		note string
	}{
		{"on weekdays", windowRow(nil), weekly([]int{1, 2, 3, 4, 5}, "02:00", 60), ""},
		{"weekdays out of order, Sunday as 7", windowRow(row{"weekdays": "[6,7,1]"}), weekly([]int{0, 1, 6}, "02:00", 60), ""},
		{"past midnight", windowRow(row{"weekdays": "[6,0]", "start_time": "23:30", "end_time": "01:00"}),
			weekly([]int{0, 6}, "23:30", 90), ""},
		{"times with seconds", windowRow(row{"start_time": "02:00:00", "end_time": "02:45:30"}),
			weekly([]int{1, 2, 3, 4, 5}, "02:00", 45), ""},
		{"every day", windowRow(row{"strategy": "recurring-interval", "interval_day": int64(1), "weekdays": "[]"}),
			weekly(all, "02:00", 60), ""},
		{"cron, Kuma's default", windowRow(row{"strategy": "cron", "cron": "30 3 * * *", "duration": int64(3600),
			"start_time": nil, "end_time": nil}), weekly(all, "03:30", 60), ""},
		{"cron on a weekday range", windowRow(row{"strategy": "cron", "cron": " 15 4 * * 1-5 ", "duration": int64(1800)}),
			weekly([]int{1, 2, 3, 4, 5}, "04:15", 30), ""},
		{"cron with day names and seconds", windowRow(row{"strategy": "cron", "cron": "0 0 22 ? * SUN,sat", "duration": int64(5400)}),
			weekly([]int{0, 6}, "22:00", 90), ""},
		{"cron on Sunday as 7, a part minute rounded up", windowRow(row{"strategy": "cron", "cron": "0 1 * * 7", "duration": int64(61)}),
			weekly([]int{0}, "01:00", 2), ""},
		{"date range not started", windowRow(row{"start_date": "2027-01-01T00:00"}),
			weekly([]int{1, 2, 3, 4, 5}, "02:00", 60), "Kuma ran it only from 1 Jan 2027 00:00; a weekly window has no date range"},
		{"date range not ended", windowRow(row{"start_date": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			"end_date": time.Date(2027, 6, 30, 18, 0, 0, 0, time.UTC)}),
			weekly([]int{1, 2, 3, 4, 5}, "02:00", 60), "Kuma ran it only until 30 Jun 2027 18:00; a weekly window"},
		{"description", windowRow(row{"description": "Rolling deploy"}),
			weekly([]int{1, 2, 3, 4, 5}, "02:00", 60), "its description is not carried over"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convertOne(tc.row)
			if len(res.Skipped) > 0 || len(res.Document.Maintenance) != 1 {
				t.Fatalf("windows = %+v, notes = %s", res.Document.Maintenance, notesOf(res))
			}
			got := res.Document.Maintenance[0]
			if got.Name != tc.want.Name || got.Monitor != tc.want.Monitor || got.Timezone != tc.want.Timezone ||
				!slices.Equal(got.Weekdays, tc.want.Weekdays) || got.LocalTime != tc.want.LocalTime ||
				got.DurationMinutes != tc.want.DurationMinutes || got.StartsAt != nil || got.EndsAt != nil {
				t.Errorf("window = %+v, want %+v", got, tc.want)
			}
			notes := notesOf(res)
			if tc.note == "" && notes != "" || !strings.Contains(notes, tc.note) {
				t.Errorf("notes = %q, want %q", notes, tc.note)
			}
			if res.WindowsConverted != 1 {
				t.Errorf("WindowsConverted = %d, want 1", res.WindowsConverted)
			}
		})
	}
}

// A single window is a one-off window. Kuma stores what was typed into a
// date-and-time field and means it in the window's zone, so 22:00 in Berlin
// in winter is 21:00 UTC, and in summer 20:00.
func TestOneOffWindows(t *testing.T) {
	single := func(start, end any, zone string) row {
		return windowRow(row{"strategy": "single", "start_date": start, "end_date": end, "timezone": zone,
			"start_time": nil, "end_time": nil, "cron": nil, "duration": nil})
	}
	cases := []struct {
		name       string
		row        row
		start, end time.Time
	}{
		{"winter, as text", single("2027-03-01T22:00", "2027-03-02T04:00", "Europe/Berlin"),
			at("2027-03-01 22:00", "Europe/Berlin"), at("2027-03-02 04:00", "Europe/Berlin")},
		{"summer, as the driver's time", single(time.Date(2027, 7, 1, 22, 0, 0, 0, time.UTC),
			time.Date(2027, 7, 2, 1, 30, 0, 0, time.UTC), "Europe/Berlin"),
			time.Date(2027, 7, 1, 20, 0, 0, 0, time.UTC), time.Date(2027, 7, 1, 23, 30, 0, 0, time.UTC)},
		{"with seconds and a space", single("2027-01-05 08:00:00", "2027-01-05 09:00:00", "UTC"),
			time.Date(2027, 1, 5, 8, 0, 0, 0, time.UTC), time.Date(2027, 1, 5, 9, 0, 0, 0, time.UTC)},
		{"running now", single("2026-10-08T10:00", "2026-10-09T10:00", "UTC"),
			time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convertOne(tc.row)
			if len(res.Document.Maintenance) != 1 {
				t.Fatalf("not converted: %s", notesOf(res))
			}
			got := res.Document.Maintenance[0]
			if got.StartsAt == nil || got.EndsAt == nil || !got.StartsAt.Equal(tc.start) || !got.EndsAt.Equal(tc.end) ||
				got.Timezone != "" || len(got.Weekdays) != 0 || got.LocalTime != "" || got.DurationMinutes != 0 {
				t.Errorf("window = %+v (%v to %v), want %v to %v", got, got.StartsAt, got.EndsAt, tc.start, tc.end)
			}
			if got.StartsAt.Location() != time.UTC {
				t.Errorf("start is in %v, want UTC", got.StartsAt.Location())
			}
		})
	}
}

// What has no counterpart, or can never run again, is left out with the
// reason, and nothing of it reaches the file.
func TestWindowsThatCannotComeOver(t *testing.T) {
	cases := []struct {
		name   string
		row    row
		reason string
	}{
		{"paused", windowRow(row{"active": int64(0)}), "paused in Kuma"},
		{"manual", windowRow(row{"strategy": "manual"}), "switched on and off by hand"},
		{"day of month", windowRow(row{"strategy": "recurring-day-of-month", "days_of_month": "[1]"}), "days of the month"},
		{"every third day", windowRow(row{"strategy": "recurring-interval", "interval_day": int64(3)}), "every 3 days"},
		{"no interval", windowRow(row{"strategy": "recurring-interval", "interval_day": nil}), "interval in days cannot be read"},
		{"unknown strategy", windowRow(row{"strategy": "lunar"}), `Kuma's schedule "lunar" has no counterpart`},
		{"no weekdays", windowRow(row{"weekdays": "[]"}), "no weekdays"},
		{"unreadable weekdays", windowRow(row{"weekdays": "[1,9]"}), "weekdays cannot be read"},
		{"weekday not a number", windowRow(row{"weekdays": `["monday"]`}), "weekdays cannot be read"},
		{"no start time", windowRow(row{"start_time": nil}), "start or end time cannot be read"},
		{"start is end", windowRow(row{"end_time": "02:00"}), "starts and ends at the same time"},
		{"date range over", windowRow(row{"start_date": "2025-06-01T00:00", "end_date": "2025-09-01T00:00"}),
			"its date range ended on 1 Sep 2025 00:00"},
		{"unreadable date range", windowRow(row{"end_date": "soon"}), "date range cannot be read"},
		{"cron with a step", windowRow(row{"strategy": "cron", "cron": "0 */6 * * *"}), `"0 */6 * * *" does not start at one time`},
		{"cron with two hours", windowRow(row{"strategy": "cron", "cron": "0 2,14 * * *"}), "does not start at one time"},
		{"cron on a day of the month", windowRow(row{"strategy": "cron", "cron": "0 2 1 * *"}), "does not start at one time"},
		{"cron in one month", windowRow(row{"strategy": "cron", "cron": "0 2 * 12 *"}), "does not start at one time"},
		{"cron with seconds", windowRow(row{"strategy": "cron", "cron": "30 0 2 * * *"}), "does not start at one time"},
		{"cron with a backwards range", windowRow(row{"strategy": "cron", "cron": "0 2 * * 5-1"}), "does not start at one time"},
		{"cron hour out of range", windowRow(row{"strategy": "cron", "cron": "0 24 * * *"}), "does not start at one time"},
		{"cron without a duration", windowRow(row{"strategy": "cron", "cron": "0 2 * * *", "duration": int64(0)}), "no duration"},
		{"cron longer than a day", windowRow(row{"strategy": "cron", "cron": "0 2 * * *", "duration": int64(86401)}),
			"lasts 1441 minutes, and a weekly window lasts at most 24 hours"},
		{"single, ended", windowRow(row{"strategy": "single", "start_date": "2025-01-10T20:00", "end_date": "2025-01-10T22:00"}),
			"it ended on 10 Jan 2025 22:00"},
		{"single, backwards", windowRow(row{"strategy": "single", "start_date": "2027-01-10T20:00", "end_date": "2027-01-10T19:00"}),
			"its end is not after its start"},
		{"single, no dates", windowRow(row{"strategy": "single"}), "no start and end date"},
		{"single, too long", windowRow(row{"strategy": "single", "start_date": "2027-01-01T00:00", "end_date": "2028-01-03T00:00"}),
			"longer than 366 days"},
		{"zone unknown", windowRow(row{"timezone": "Mars/Olympus"}), `time zone "Mars/Olympus" is not an IANA`},
		{"zone Local", windowRow(row{"timezone": "Local"}), "the host's own"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convertOne(tc.row)
			if len(res.Document.Maintenance) != 0 || res.WindowsConverted != 0 {
				t.Errorf("converted: %+v", res.Document.Maintenance)
			}
			if len(res.Skipped) != 1 || res.Skipped[0].Kind != "maintenance" || res.Skipped[0].Name != "Deploy" ||
				!strings.Contains(res.Skipped[0].Reason, tc.reason) {
				t.Errorf("skipped = %v, want one reason with %q", res.Skipped, tc.reason)
			}
			if len(res.Changed) != 0 {
				t.Errorf("a window left out also has changes listed: %v", res.Changed)
			}
		})
	}
}

// Kuma's default zone is its server's, kept in its settings as JSON. When
// that was never set, Kuma used the host's zone, which the database does
// not record: UTC is assumed and said.
func TestWindowZones(t *testing.T) {
	src, keys := windowSource([]row{windowRow(row{"timezone": "SAME_AS_SERVER"}), windowRow(row{"id": int64(2), "timezone": nil})},
		map[int64][]int64{1: {1}, 2: {1}}, row{"key": "serverTimezone", "value": `"Asia/Tokyo"`})
	var res Result
	convertWindows(src, keys, &res)
	if len(res.Document.Maintenance) != 2 || res.Document.Maintenance[0].Timezone != "Asia/Tokyo" ||
		res.Document.Maintenance[1].Timezone != "Asia/Tokyo" || len(res.Changed) != 0 {
		t.Errorf("windows = %+v, notes = %s", res.Document.Maintenance, notesOf(res))
	}

	res = convertOne(windowRow(row{"timezone": "SAME_AS_SERVER"}))
	if len(res.Document.Maintenance) != 1 || res.Document.Maintenance[0].Timezone != "UTC" ||
		!strings.Contains(notesOf(res), "so UTC is assumed") {
		t.Errorf("windows = %+v, notes = %s", res.Document.Maintenance, notesOf(res))
	}

	// A one-off window in the server's zone is placed in that zone.
	src, keys = windowSource([]row{windowRow(row{"strategy": "single", "timezone": "SAME_AS_SERVER",
		"start_date": "2027-01-10T09:00", "end_date": "2027-01-10T10:00"})}, map[int64][]int64{1: {1}},
		row{"key": "serverTimezone", "value": `"Asia/Tokyo"`})
	res = Result{}
	convertWindows(src, keys, &res)
	if len(res.Document.Maintenance) != 1 || !res.Document.Maintenance[0].StartsAt.Equal(at("2027-01-10 09:00", "Asia/Tokyo")) {
		t.Errorf("windows = %+v, notes = %s", res.Document.Maintenance, notesOf(res))
	}
}

// A SubGlance window covers one monitor or one tag pair, so a Kuma window
// becomes one per monitor, and one per group tag for a group and every
// group inside it. What it covered that did not come over is named.
func TestWindowTargets(t *testing.T) {
	res := convertOne(windowRow(nil), 1, 2, 3, 1)
	var got []string
	for _, m := range res.Document.Maintenance {
		got = append(got, m.Monitor+"|"+m.TagKey+"="+m.TagValue)
	}
	if want := []string{"shop|=", "|group=Edge", "|group=Edge EU"}; !slices.Equal(got, want) {
		t.Errorf("windows = %v, want %v", got, want)
	}
	if res.WindowsConverted != 1 || !strings.Contains(notesOf(res), `it also covers "Docker", which is not imported`) {
		t.Errorf("converted %d, notes = %s", res.WindowsConverted, notesOf(res))
	}

	res = convertOne(windowRow(nil), 2)
	if len(res.Document.Maintenance) != 0 || len(res.Skipped) != 1 ||
		!strings.Contains(res.Skipped[0].Reason, `none of the monitors it covers is imported: "Docker"`) {
		t.Errorf("windows = %v, skipped = %v", res.Document.Maintenance, res.Skipped)
	}
	src, keys := windowSource([]row{windowRow(nil)}, nil)
	res = Result{}
	convertWindows(src, keys, &res)
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "covers no monitors") {
		t.Errorf("skipped = %v", res.Skipped)
	}

	// Two groups with one name share one tag, so one window covers both.
	src, keys = windowSource([]row{windowRow(nil)}, map[int64][]int64{1: {3}})
	src.monitors = append(src.monitors, row{"id": int64(6), "name": " Edge ", "type": "group"})
	src.monitorMaintenance = append(src.monitorMaintenance, row{"id": int64(9), "maintenance_id": int64(1), "monitor_id": int64(6)})
	res = Result{}
	convertWindows(src, keys, &res)
	if len(res.Document.Maintenance) != 2 || !strings.Contains(notesOf(res), `2 groups in Kuma are called "Edge"`) {
		t.Errorf("windows = %+v, notes = %s", res.Document.Maintenance, notesOf(res))
	}

	// A group whose name is longer than a tag value gets the value its
	// monitors got.
	long := strings.Repeat("g", 70) + " x"
	src, keys = windowSource([]row{windowRow(nil)}, map[int64][]int64{1: {3}})
	src.monitors[2]["name"] = long
	res = Result{}
	convertWindows(src, keys, &res)
	if v := convertTags("CDN", nil, long, &Result{})["group"]; len(res.Document.Maintenance) < 1 ||
		res.Document.Maintenance[0].TagValue != v || len(v) != 64 {
		t.Errorf("window tag %q, monitor tag %q", res.Document.Maintenance[0].TagValue, v)
	}
}

// No file brings more windows than an instance keeps; the rest are named.
func TestWindowCap(t *testing.T) {
	var windows []row
	links := map[int64][]int64{}
	for i := 1; i <= 102; i++ {
		windows = append(windows, windowRow(row{"id": int64(i), "title": "W" + strconv.Itoa(i)}))
		links[int64(i)] = []int64{1, 5}
	}
	links[1] = []int64{1} // 1 + 99*2 = 199, so W101 has room for one of its two
	src, keys := windowSource(windows, links)
	var res Result
	convertWindows(src, keys, &res)
	if len(res.Document.Maintenance) != maxWindows {
		t.Errorf("%d windows written, want %d", len(res.Document.Maintenance), maxWindows)
	}
	notes := notesOf(res)
	if !strings.Contains(notes, `maintenance "W101" (recurring-weekday): 1 of the 2 windows it became were left out`) ||
		!strings.Contains(notes, `maintenance "W102" (recurring-weekday): SubGlance keeps at most 200`) {
		t.Errorf("notes = %s", notes)
	}
}

// A name longer than SubGlance keeps is cut on a character boundary.
func TestWindowNameIsShortened(t *testing.T) {
	res := convertOne(windowRow(row{"title": strings.Repeat("é", 70)}))
	if len(res.Document.Maintenance) != 1 {
		t.Fatalf("not converted: %s", notesOf(res))
	}
	if name := res.Document.Maintenance[0].Name; len(name) != 120 || name != strings.Repeat("é", 60) {
		t.Errorf("name = %q (%d bytes)", name, len(name))
	}
	if !strings.Contains(notesOf(res), "shortened to 120 bytes") {
		t.Errorf("notes = %s", notesOf(res))
	}
}
