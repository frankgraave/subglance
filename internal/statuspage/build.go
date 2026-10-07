package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// MaintenanceHorizon is how far ahead the maintenance list announces a
// window (design §1.1: running now, or starting within 7 days).
const MaintenanceHorizon = 7 * 24 * time.Hour

// RecoverySource knows whether a monitor's confirmed incident is seeing
// passing checks without having met its recovery threshold yet. That streak
// lives in the running state engine only, not in the database, so the
// builder asks the engine. The monitor runner satisfies it.
type RecoverySource interface {
	Recovery(monitorID int64) (passes, threshold int, ok bool)
}

// Builder reads one status page's facts from the database and turns them
// into its public answer.
//
// It is the only place where store rows meet the public types, and it copies
// across nothing but what those types name: a monitor's internal name,
// target, tags, check results and incident causes are read to decide a state,
// then left behind.
type Builder struct {
	DB *store.DB
	// Recovery may be nil, for instance when the process runs without a
	// checker. A confirmed incident then shows as down until it closes.
	Recovery RecoverySource
}

// Build returns the public page for p as of now.
//
// The caller decides whether p may be shown at all (it exists and is
// enabled); Build does not look at Enabled, so a preview for the operator
// could use it too.
func (b Builder) Build(ctx context.Context, p store.StatusPage, now time.Time) (Page, error) {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return Page{}, fmt.Errorf("status page %q: time zone %q: %w", p.Slug, p.Timezone, err)
	}
	shown, err := b.DB.ShownStatusPageEntries(ctx, p)
	if err != nil {
		return Page{}, err
	}
	windows, err := b.DB.ListMaintenance(ctx)
	if err != nil {
		return Page{}, fmt.Errorf("list maintenance windows: %w", err)
	}

	since := Since(now, loc)
	// Each window's occurrences are expanded once for the whole page, over
	// the history range and the announcement horizon together, rather than
	// once per entry.
	spans := make([][]store.MaintenanceSpan, len(windows))
	for i, w := range windows {
		spans[i] = w.Occurrences(since, now.Add(MaintenanceHorizon))
	}

	page := Page{
		Title:       p.Title,
		Description: p.Description,
		GeneratedAt: now.UTC(),
		Timezone:    p.Timezone,
		Language:    p.Language,
		Accent:      p.Accent,
		CreditShown: !p.HideCredit,
		Entries:     []Entry{},
		Maintenance: []Maintenance{},
		Outages:     []Outage{},
	}
	if p.Logo != nil {
		page.Logo = &Logo{Path: LogoRoot + LogoPath + p.Logo.FileName(), Width: p.Logo.Width, Height: p.Logo.Height}
	}
	announced := map[spanKey][]string{}
	for _, se := range shown {
		m, err := b.DB.GetMonitor(ctx, se.MonitorID)
		if errors.Is(err, sql.ErrNoRows) {
			// Deleted between the two reads; its entry is gone with it.
			continue
		}
		if err != nil {
			return Page{}, fmt.Errorf("read monitor %d: %w", se.MonitorID, err)
		}

		entry, incidents, err := b.entry(ctx, m, se, since, now, loc, windows, spans, announced)
		if err != nil {
			return Page{}, err
		}
		page.Entries = append(page.Entries, entry)
		page.Outages = append(page.Outages, Outages(se.PublicKey, incidents, now)...)
	}

	page.Summary = Summarise(page.Entries)
	sort.SliceStable(page.Outages, func(i, j int) bool {
		return page.Outages[i].StartedAt.After(page.Outages[j].StartedAt)
	})
	page.Maintenance = maintenanceList(announced)
	return page, nil
}

// entry builds one monitor's row, and records the upcoming maintenance that
// covers it in announced.
func (b Builder) entry(ctx context.Context, m store.Monitor, se store.StatusPageEntry,
	since, now time.Time, loc *time.Location, windows []store.MaintenanceWindow,
	spans [][]store.MaintenanceSpan, announced map[spanKey][]string,
) (Entry, []store.IncidentSpan, error) {
	live, err := b.live(ctx, m)
	if err != nil {
		return Entry{}, nil, err
	}
	hours, err := b.DB.StatusHistoryHours(ctx, m.ID, since)
	if err != nil {
		return Entry{}, nil, err
	}
	incidents, err := b.DB.ConfirmedIncidentSpans(ctx, m.ID, since)
	if err != nil {
		return Entry{}, nil, err
	}

	var past []Span
	inMaintenance := false
	for i, w := range windows {
		if !covers(w, m) {
			continue
		}
		for _, s := range spans[i] {
			// The spans cover a range that contains now, so being inside one
			// is the answer w.Active(now) gives, without resolving the
			// recurrence and its time zone again for every entry.
			if !now.Before(s.Start) && now.Before(s.End) {
				inMaintenance = true
			}
			if s.Start.Before(now) {
				past = append(past, Span{Start: s.Start, End: s.End})
			}
			if s.End.After(now) {
				k := spanKey{start: s.Start.Unix(), end: s.End.Unix()}
				announced[k] = append(announced[k], se.PublicKey)
			}
		}
	}

	return Entry{
		Key:           se.PublicKey,
		Name:          se.DisplayName,
		Status:        PublicStatus(live),
		InMaintenance: inMaintenance,
		// Only beside up: a monitor that is down or not watched has a
		// louder thing to say, and "expires soon" next to it would read
		// as the reason.
		CertificateExpiring: live.Notice && PublicStatus(live) == StatusUp,
		Uptime90d:           Uptime(hours),
		Uptime30d:           UptimeFrom(hours, RecentSince(now, loc)),
		Days:                Days(History{Hours: hours, Incidents: incidents, Maintenance: past}, now, loc),
	}, incidents, nil
}

// live reads the facts PublicStatus decides on. Whether the last check
// passed is not among them: see Live.Checked.
func (b Builder) live(ctx context.Context, m store.Monitor) (Live, error) {
	l := Live{Enabled: m.Enabled}

	_, err := b.DB.LatestHeartbeat(ctx, m.ID)
	switch {
	case err == nil:
		l.Checked = true
	case !errors.Is(err, sql.ErrNoRows):
		return Live{}, fmt.Errorf("latest heartbeat for monitor %d: %w", m.ID, err)
	}

	inc, err := b.DB.OpenIncidentFor(ctx, m.ID)
	switch {
	case err == nil:
		l.Notice = inc.Notice
		l.Confirmed = inc.Confirmed() && !inc.Notice
	case !errors.Is(err, store.ErrNoOpenIncident):
		return Live{}, fmt.Errorf("open incident for monitor %d: %w", m.ID, err)
	}
	if l.Confirmed && b.Recovery != nil {
		_, _, l.Recovering = b.Recovery.Recovery(m.ID)
	}
	return l, nil
}

// covers reports whether a maintenance window applies to a monitor: it names
// the monitor, or the monitor carries its tag now. A tag window is judged by
// the monitor's current tags for past occurrences too, because tag history is
// not kept.
func covers(w store.MaintenanceWindow, m store.Monitor) bool {
	if w.MonitorID != 0 {
		return w.MonitorID == m.ID
	}
	if w.TagKey == "" {
		return false
	}
	v, ok := m.Tags[w.TagKey]
	return ok && v == w.TagValue
}

// spanKey identifies an announced stretch by its instants. time.Time is not
// used as the key because two equal instants in different zones compare
// unequal as map keys, and would be listed twice.
type spanKey struct{ start, end int64 }

// maintenanceList turns the announced spans into the page's list, soonest
// first. Two windows with the same start and end are one item: a visitor
// learns when, and for which entries, not how the operator filed it.
func maintenanceList(announced map[spanKey][]string) []Maintenance {
	out := make([]Maintenance, 0, len(announced))
	for s, keys := range announced {
		out = append(out, Maintenance{
			StartsAt: time.Unix(s.start, 0).UTC(),
			EndsAt:   time.Unix(s.end, 0).UTC(),
			Keys:     dedupe(keys),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		return out[i].EndsAt.Before(out[j].EndsAt)
	})
	return out
}

// dedupe drops repeated keys, keeping the first of each and so the page
// order the keys were added in.
func dedupe(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := keys[:0:0]
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
