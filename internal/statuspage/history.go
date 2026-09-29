package statuspage

import (
	"math"
	"sort"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// HistoryDays is how many days an entry's history covers.
const HistoryDays = 90

// OutageWindow is how far back the outage list reaches (design §1.1).
const OutageWindow = 14 * 24 * time.Hour

// Span is a stretch of time. End is zero while it is still running.
type Span struct {
	Start time.Time
	End   time.Time
}

// History is what the caller has read about one monitor's past.
type History struct {
	// Hours are the monitor's checks per hour, from store.StatusHistoryHours.
	Hours []store.StatusHistoryHour
	// Incidents are its confirmed incidents, from
	// store.ConfirmedIncidentSpans.
	Incidents []store.IncidentSpan
	// Maintenance are the stretches the monitor spent in maintenance.
	// Incident time inside them is not downtime, exactly as checks inside
	// them are not counted in uptime.
	Maintenance []Span
}

// Since is where a history read for Days has to start: the first instant of
// the oldest day shown, in the page's zone.
func Since(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()-(HistoryDays-1), 0, 0, 0, 0, loc)
}

// Days returns HistoryDays days of history, oldest first, the last one being
// today in loc (design §1.4).
//
//   - down: the day holds confirmed incident time outside maintenance;
//   - degraded: no downtime, but failed checks that were never confirmed;
//   - up: only passing checks;
//   - no_data: no counted check at all.
//
// An hour belongs to the day its first minute falls in. Where a zone's offset
// is not a whole number of hours, that moves up to half an hour of checks to
// a neighbouring day; the rollup keeps no finer timestamps to split on.
//
// down_minutes is the confirmed incident time that day outside maintenance,
// rounded up to whole minutes, so a two-minute blip reads differently from
// a lost afternoon. An open incident runs until now.
func Days(h History, now time.Time, loc *time.Location) []Day {
	start := Since(now, loc)
	days := make([]Day, HistoryDays)
	bounds := make([]time.Time, HistoryDays+1)
	for i := range bounds {
		// time.Date normalises the day, so this steps across months and
		// through daylight-saving changes without assuming 24 hours.
		bounds[i] = time.Date(start.Year(), start.Month(), start.Day()+i, 0, 0, 0, 0, loc)
	}
	for i := range days {
		days[i] = Day{Date: bounds[i].Format(time.DateOnly), State: DayNoData}
	}
	dayOf := func(t time.Time) int {
		// The first bound after t, minus one.
		return sort.Search(len(bounds), func(i int) bool { return bounds[i].After(t) }) - 1
	}

	var up, warn, down [HistoryDays]int
	for _, hr := range h.Hours {
		i := dayOf(hr.Hour)
		if i < 0 || i >= HistoryDays {
			continue
		}
		up[i] += hr.Up + hr.Unassessed
		warn[i] += hr.Warning
		down[i] += hr.Down
	}

	var downTime [HistoryDays]time.Duration
	for _, inc := range h.Incidents {
		end := inc.End
		if end.IsZero() || end.After(now) {
			end = now
		}
		for _, piece := range subtract(Span{Start: inc.Start, End: end}, h.Maintenance, now) {
			for i := range days {
				downTime[i] += overlap(piece, bounds[i], bounds[i+1])
			}
		}
	}

	for i := range days {
		days[i].DownMinutes = int(math.Ceil(downTime[i].Minutes()))
		switch {
		// A check assessed down outside maintenance is incident time outside
		// maintenance, even when the incident row itself has been pruned.
		case days[i].DownMinutes > 0 || down[i] > 0:
			days[i].State = DayDown
		case warn[i] > 0:
			days[i].State = DayDegraded
		case up[i] > 0:
			days[i].State = DayUp
		}
	}
	return days
}

// Uptime returns the share of passing checks outside maintenance, rounded
// to two decimals, or nil when there were none to count. It counts exactly
// what the dashboard's uptime counts: confirmed-down checks against passing
// ones, with unconfirmed failures and unassessed history left out.
func Uptime(hours []store.StatusHistoryHour) *float64 {
	var up, down int
	for _, h := range hours {
		up += h.Up
		down += h.Down
	}
	if up+down == 0 {
		return nil
	}
	pct := math.Round(float64(up)/float64(up+down)*100*100) / 100
	return &pct
}

// Outages lists the confirmed incidents that were running inside
// OutageWindow before now, newest first, under the entry's public key.
func Outages(key string, incidents []store.IncidentSpan, now time.Time) []Outage {
	from := now.Add(-OutageWindow)
	out := []Outage{}
	for _, inc := range incidents {
		if !inc.End.IsZero() && inc.End.Before(from) {
			continue
		}
		// A span stamped after now (a push report or a clock step can do
		// that) is not published as a fact yet: an outage that has not
		// started is left out, and an end that has not happened leaves the
		// outage open.
		if inc.Start.After(now) {
			continue
		}
		o := Outage{Key: key, StartedAt: inc.Start.UTC()}
		end := now
		if !inc.End.IsZero() && !inc.End.After(now) {
			resolved := inc.End.UTC()
			o.ResolvedAt = &resolved
			end = inc.End
		}
		if d := end.Sub(inc.Start); d > 0 {
			o.DurationS = int64(d / time.Second)
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// subtract returns the parts of s that no span in cut covers. A cut span
// with a zero End runs until now.
func subtract(s Span, cut []Span, now time.Time) []Span {
	pieces := []Span{s}
	for _, c := range cut {
		cEnd := c.End
		if cEnd.IsZero() {
			cEnd = now
		}
		next := pieces[:0:0]
		for _, p := range pieces {
			if !c.Start.Before(p.End) || !cEnd.After(p.Start) {
				next = append(next, p)
				continue
			}
			if c.Start.After(p.Start) {
				next = append(next, Span{Start: p.Start, End: c.Start})
			}
			if cEnd.Before(p.End) {
				next = append(next, Span{Start: cEnd, End: p.End})
			}
		}
		pieces = next
	}
	return pieces
}

// overlap is how much of s falls in [from, to).
func overlap(s Span, from, to time.Time) time.Duration {
	start, end := s.Start, s.End
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}
