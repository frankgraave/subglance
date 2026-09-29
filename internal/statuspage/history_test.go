package statuspage

import (
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

var amsterdam = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		panic(err)
	}
	return loc
}()

// now is mid-afternoon on 29 September in Amsterdam (UTC+2).
var now = time.Date(2026, 9, 29, 13, 30, 0, 0, time.UTC)

func hour(day, h int) time.Time { return time.Date(2026, 9, day, h, 0, 0, 0, time.UTC) }

func TestDaysAreNinetyInThePageZoneEndingToday(t *testing.T) {
	days := Days(History{}, now, amsterdam)
	if len(days) != HistoryDays {
		t.Fatalf("len = %d, want %d", len(days), HistoryDays)
	}
	if days[len(days)-1].Date != "2026-09-29" || days[0].Date != "2026-07-02" {
		t.Errorf("range = %s .. %s, want 2026-07-02 .. 2026-09-29", days[0].Date, days[len(days)-1].Date)
	}
	for _, d := range days {
		if d.State != DayNoData || d.DownMinutes != 0 {
			t.Fatalf("empty history day %+v, want no_data with 0 minutes", d)
		}
	}
}

// 22:30 UTC on the 28th is 00:30 on the 29th in Amsterdam: the check belongs
// to the page's today, not the server's yesterday.
func TestDaysFollowThePageZone(t *testing.T) {
	h := History{Hours: []store.StatusHistoryHour{{Hour: hour(28, 22), Warning: 1}}}
	days := Days(h, now, amsterdam)
	if got := days[89].State; got != DayDegraded {
		t.Errorf("29th = %s, want degraded", got)
	}
	if got := days[88].State; got != DayNoData {
		t.Errorf("28th = %s, want no_data", got)
	}
}

func TestDayStatePrecedence(t *testing.T) {
	h := History{Hours: []store.StatusHistoryHour{
		{Hour: hour(27, 8), Up: 5},
		{Hour: hour(28, 8), Up: 5}, {Hour: hour(28, 9), Warning: 1},
		{Hour: hour(29, 8), Up: 5}, {Hour: hour(29, 9), Warning: 1}, {Hour: hour(29, 10), Down: 1},
		// Only maintenance on the 26th: nothing counted, so no data.
		{Hour: hour(26, 8), Maintenance: 12},
		// Legacy checks prove the monitor was watched.
		{Hour: hour(25, 8), Unassessed: 3},
	}}
	days := Days(h, now, amsterdam)
	want := map[int]DayState{89: DayDown, 88: DayDegraded, 87: DayUp, 86: DayNoData, 85: DayUp}
	for i, s := range want {
		if days[i].State != s {
			t.Errorf("%s = %s, want %s", days[i].Date, days[i].State, s)
		}
	}
}

func TestDownMinutesRoundUpAndSplitAtLocalMidnight(t *testing.T) {
	h := History{Incidents: []store.IncidentSpan{
		// 90 seconds on the 27th: rounds up to 2 minutes.
		{Start: hour(27, 8), End: hour(27, 8).Add(90 * time.Second)},
		// 21:30 to 22:30 UTC on the 28th crosses Amsterdam midnight.
		{Start: hour(28, 21).Add(30 * time.Minute), End: hour(28, 22).Add(30 * time.Minute)},
	}}
	days := Days(h, now, amsterdam)
	if got := days[87].DownMinutes; got != 2 {
		t.Errorf("27th down minutes = %d, want 2", got)
	}
	if days[88].DownMinutes != 30 || days[89].DownMinutes != 30 {
		t.Errorf("28th, 29th = %d, %d minutes; want 30 and 30", days[88].DownMinutes, days[89].DownMinutes)
	}
	for _, i := range []int{87, 88, 89} {
		if days[i].State != DayDown {
			t.Errorf("%s = %s, want down", days[i].Date, days[i].State)
		}
	}
}

func TestOpenIncidentRunsUntilNow(t *testing.T) {
	h := History{Incidents: []store.IncidentSpan{{Start: now.Add(-10 * time.Minute)}}}
	if got := Days(h, now, amsterdam)[89].DownMinutes; got != 10 {
		t.Errorf("down minutes = %d, want 10", got)
	}
}

func TestIncidentTimeInMaintenanceIsNotDowntime(t *testing.T) {
	h := History{
		Incidents: []store.IncidentSpan{{Start: hour(27, 8), End: hour(27, 10)}},
		// The middle hour was announced maintenance.
		Maintenance: []Span{{Start: hour(27, 8).Add(30 * time.Minute), End: hour(27, 9).Add(30 * time.Minute)}},
	}
	if got := Days(h, now, amsterdam)[87].DownMinutes; got != 60 {
		t.Errorf("down minutes = %d, want 60 (two hours less one of maintenance)", got)
	}

	h.Maintenance = []Span{{Start: hour(27, 7), End: hour(27, 11)}}
	d := Days(h, now, amsterdam)[87]
	if d.DownMinutes != 0 || d.State == DayDown {
		t.Errorf("fully covered incident = %+v, want no downtime", d)
	}
}

func TestUptimeCountsConfirmedChecksOnly(t *testing.T) {
	if Uptime(nil) != nil {
		t.Error("no checks: want nil")
	}
	if Uptime([]store.StatusHistoryHour{{Warning: 3, Unassessed: 4, Maintenance: 5}}) != nil {
		t.Error("only uncounted checks: want nil")
	}
	got := Uptime([]store.StatusHistoryHour{{Up: 2, Down: 1, Warning: 50, Maintenance: 50}})
	if got == nil || *got != 66.67 {
		t.Errorf("uptime = %v, want 66.67", got)
	}
}

func TestOutagesKeepTheLastFourteenDaysNewestFirst(t *testing.T) {
	old := store.IncidentSpan{Start: now.Add(-20 * 24 * time.Hour), End: now.Add(-15 * 24 * time.Hour)}
	straddling := store.IncidentSpan{Start: now.Add(-15 * 24 * time.Hour), End: now.Add(-13 * 24 * time.Hour)}
	open := store.IncidentSpan{Start: now.Add(-5 * time.Minute)}

	got := Outages("abc", []store.IncidentSpan{old, straddling, open}, now)
	if len(got) != 2 {
		t.Fatalf("got %d outages, want 2: %+v", len(got), got)
	}
	if got[0].ResolvedAt != nil || got[0].DurationS != 300 || got[0].Key != "abc" {
		t.Errorf("open outage = %+v, want unresolved, 300s, key abc", got[0])
	}
	if got[1].ResolvedAt == nil || got[1].DurationS != 2*24*3600 {
		t.Errorf("resolved outage = %+v, want resolved, two days", got[1])
	}
}
