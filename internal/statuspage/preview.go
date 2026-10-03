package statuspage

import "time"

// PreviewScenarios returns the public pages the design prototype draws
// (docs/mockups/pages/status.html: an outage, all up, maintenance), plus a
// page with no services, as of now. They feed the preview command and the
// browser test that holds the rendered page to the prototype's layout claims.
//
// Fixed data, never read from a database: a scenario is a picture of a state,
// not a sample of one.
func PreviewScenarios(now time.Time) map[string]Page {
	const zone = "Europe/Amsterdam"
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	now = now.Truncate(time.Minute)
	pct := func(v float64) *float64 { return &v }
	base := func() Page {
		return Page{
			Title:       "Example Co status",
			Description: "Live status of the services Example Co runs for its customers.",
			GeneratedAt: now.UTC(),
			Timezone:    zone,
			Entries:     []Entry{},
			Maintenance: []Maintenance{},
			Outages:     []Outage{},
		}
	}
	// The two figures are the history's 90 days and the 30 a phone draws;
	// a scenario states both, as Build would compute them.
	entry := func(key, name string, status Status, uptime, recent float64, hist []Day) Entry {
		return Entry{Key: key, Name: name, Status: status, Uptime90d: pct(uptime), Uptime30d: pct(recent), Days: hist}
	}
	days := func(down, degraded []int, noDataFrom int) []Day {
		return previewDays(now, loc, down, degraded, noDataFrom)
	}
	resolved := func(key string, ago, length time.Duration) Outage {
		start := now.Add(-ago)
		end := start.Add(length)
		return Outage{Key: key, StartedAt: start.UTC(), ResolvedAt: &end, DurationS: int64(length / time.Second)}
	}

	outage := base()
	outage.Entries = []Entry{
		entry("a1", "Website", StatusUp, 99.98, 100, days([]int{41}, []int{12}, 0)),
		entry("a2", "Web app", StatusUp, 99.95, 100, days([]int{41, 63}, nil, 0)),
		entry("a3", "Public API", StatusDown, 99.71, 99.17, days([]int{0, 9, 41}, []int{22}, 0)),
		entry("a4", "Email delivery", StatusDegraded, 99.90, 100, days(nil, []int{0, 1, 30}, 0)),
		entry("a5", "Background jobs", StatusUp, 100, 100, days(nil, nil, 0)),
	}
	tomorrow := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day()+1, 2, 0, 0, 0, loc)
	outage.Maintenance = []Maintenance{{StartsAt: tomorrow.UTC(), EndsAt: tomorrow.Add(time.Hour).UTC(), Keys: []string{"a2"}}}
	ongoing := now.Add(-(4*time.Hour + 51*time.Minute))
	outage.Outages = []Outage{
		{Key: "a3", StartedAt: ongoing.UTC(), DurationS: int64((4*time.Hour + 51*time.Minute) / time.Second)},
		resolved("a3", 9*24*time.Hour+3*time.Hour, 23*time.Minute),
	}
	outage.Summary = Summarise(outage.Entries)

	allUp := base()
	allUp.Entries = []Entry{
		entry("a1", "Website", StatusUp, 99.98, 100, days([]int{41}, []int{12}, 0)),
		entry("a2", "Web app", StatusUp, 99.95, 100, days([]int{41, 63}, nil, 0)),
		entry("a3", "Public API", StatusUp, 99.93, 99.94, days([]int{9, 41}, []int{22}, 0)),
		entry("a4", "Email delivery", StatusUp, 100, 100, days(nil, []int{30}, 0)),
		entry("a5", "Background jobs", StatusUp, 100, 100, days(nil, nil, 0)),
	}
	allUp.Outages = []Outage{resolved("a3", 9*24*time.Hour+3*time.Hour, 23*time.Minute)}
	allUp.Summary = Summarise(allUp.Entries)

	maintenance := base()
	maintenance.Entries = []Entry{
		entry("a1", "Website", StatusUp, 99.98, 100, days([]int{41}, []int{12}, 0)),
		entry("a2", "Web app", StatusUp, 99.95, 100, days([]int{41, 63}, nil, 0)),
		entry("a3", "Public API", StatusUp, 99.93, 99.94, days([]int{9, 41}, []int{22}, 0)),
		entry("a4", "Email delivery", StatusUp, 100, 100, days(nil, []int{30}, 0)),
		entry("a5", "Background jobs", StatusNotMonitored, 100, 100, days(nil, nil, 20)),
	}
	maintenance.Entries[1].InMaintenance = true
	maintenance.Maintenance = []Maintenance{{StartsAt: now.Add(-14 * time.Minute).UTC(), EndsAt: now.Add(46 * time.Minute).UTC(), Keys: []string{"a2"}}}
	maintenance.Summary = Summarise(maintenance.Entries)

	return map[string]Page{
		"outage":      outage,
		"allup":       allUp,
		"maintenance": maintenance,
		"empty":       base(),
	}
}

// previewDays draws HistoryDays days ending today: the days listed as down
// or degraded (in days ago), no data from noDataFrom days ago on (0 = never),
// and up otherwise.
func previewDays(now time.Time, loc *time.Location, down, degraded []int, noDataFrom int) []Day {
	in := func(list []int, n int) bool {
		for _, v := range list {
			if v == n {
				return true
			}
		}
		return false
	}
	start := Since(now, loc)
	out := make([]Day, HistoryDays)
	for i := range out {
		ago := HistoryDays - 1 - i
		d := Day{Date: time.Date(start.Year(), start.Month(), start.Day()+i, 0, 0, 0, 0, loc).Format(time.DateOnly), State: DayUp}
		switch {
		case noDataFrom > 0 && ago >= noDataFrom:
			d.State = DayNoData
		case in(down, ago):
			d.State, d.DownMinutes = DayDown, 23
		case in(degraded, ago):
			d.State = DayDegraded
		}
		out[i] = d
	}
	return out
}
