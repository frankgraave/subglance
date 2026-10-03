// Package statuspage turns a monitor's state and history into what a public
// status page may show, and nothing more.
//
// Everything a visitor can see is built from the types in this file. They are
// deliberately not store.Monitor or store.Incident, nor anything the
// authenticated API serialises: a field added to a monitor cannot reach a
// public page by accident, because it has to be added here by name, in a diff
// a reviewer reads. docs/design/status-page.md §1 lists the fields and says
// why each is safe; public_test.go fails when the two drift apart.
//
// The package is pure. It takes facts the caller has read and returns the
// public shape, so every rule about what a visitor may learn is testable
// without a database or an HTTP server.
package statuspage

import "time"

// Status is a monitor's state in public words (design §1.3).
type Status string

const (
	StatusUp           Status = "up"
	StatusDegraded     Status = "degraded"
	StatusDown         Status = "down"
	StatusNoData       Status = "no_data"
	StatusNotMonitored Status = "not_monitored"
)

// DayState is one day of the history bar (design §1.4).
type DayState string

const (
	DayUp       DayState = "up"
	DayDegraded DayState = "degraded"
	DayDown     DayState = "down"
	DayNoData   DayState = "no_data"
)

// Page is the whole public answer for one status page.
type Page struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	GeneratedAt time.Time     `json:"generated_at"`
	Timezone    string        `json:"timezone"`
	Summary     Summary       `json:"summary"`
	Entries     []Entry       `json:"entries"`
	Maintenance []Maintenance `json:"maintenance"`
	Outages     []Outage      `json:"outages"`
}

// Summary counts the entries by public state. no_data and not_monitored
// both count as unmonitored: neither says anything about the service.
type Summary struct {
	Up          int `json:"up"`
	Degraded    int `json:"degraded"`
	Down        int `json:"down"`
	Unmonitored int `json:"unmonitored"`
}

// Entry is one monitor on the page, under its public name only.
type Entry struct {
	// Key is the entry's random public key, never the monitor id.
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	Status        Status   `json:"status"`
	InMaintenance bool     `json:"in_maintenance"`
	Uptime90d     *float64 `json:"uptime_90d"`
	// Uptime30d is the same figure over the last RecentDays days, the
	// period a phone's shorter history bar draws.
	Uptime30d *float64 `json:"uptime_30d"`
	Days      []Day    `json:"days"`
}

// Day is one day of an entry's history, in the page's time zone.
type Day struct {
	Date        string   `json:"date"`
	State       DayState `json:"state"`
	DownMinutes int      `json:"down_minutes"`
}

// Maintenance is an announced window, without its name, schedule or
// channels: only when, and which entries it covers.
type Maintenance struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Keys     []string  `json:"keys"`
}

// Outage is one confirmed incident, without its cause or error.
type Outage struct {
	Key        string     `json:"key"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	DurationS  int64      `json:"duration_s"`
}

// Summarise counts entries by public state.
func Summarise(entries []Entry) Summary {
	var s Summary
	for _, e := range entries {
		switch e.Status {
		case StatusUp:
			s.Up++
		case StatusDegraded:
			s.Degraded++
		case StatusDown:
			s.Down++
		default:
			s.Unmonitored++
		}
	}
	return s
}
