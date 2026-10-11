package notifier

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// DefaultGroupWindow is how long an alert waits for company.
//
// The number is a trade between two costs that pull opposite ways. Too short
// and a shared outage still arrives as a burst, because twenty monitors on a
// 60-second schedule do not fail in the same second — they fail across the
// next minute as each one's turn comes round. Too long and a single monitor
// going down sits in a queue while someone could have been fixing it.
//
// Ninety seconds covers a full 60-second check cycle plus the confirmation
// delay that follows it, which is what actually decides whether two failures
// belong to the same event. It is the default rather than a fixed rule: the
// right window follows the check schedule, so someone running 10-second checks
// on a handful of services has a legitimate reason to shorten it, and someone
// with a fleet on five-minute checks has one to lengthen it.
const DefaultGroupWindow = 90 * time.Second

// GroupingDisabled is the window value that turns batching off entirely, so
// every alert is written to the outbox the moment it happens.
//
// It is a negative duration rather than zero because zero in Options means
// "use the default" — the same convention the other Options fields follow.
// Callers that take a window from a user should translate whatever they spell
// "off" into this value rather than passing a raw negative number around.
const GroupingDisabled time.Duration = -1

// Grouper collects alerts that arrive close together and sends one message
// about all of them.
//
// This is the difference between a monitor you keep and one you mute. When an
// uplink drops, twenty monitors do not represent twenty problems: they are one
// problem seen twenty times, and a tool that says so twenty times has told you
// nothing except that it is loud. The state engine already suppresses
// flapping and waits for confirmation; this is the same idea applied across
// monitors rather than across time.
//
// What it does not do is delay a recovery. A grouped outage ends with one
// summary, but the summary is assembled the moment the last member resolves,
// not after another window — nobody needs their good news rate-limited. The
// one exception is a recovery whose own alert or reminder is still waiting in
// an open batch: it waits for that batch to close, so it cannot arrive first
// (see due).
type Grouper struct {
	window time.Duration
	now    func() time.Time
}

// newGrouper builds a Grouper. Window zero means DefaultGroupWindow; a
// negative window means grouping is off and every alert is written
// immediately.
func newGrouper(window time.Duration, now func() time.Time) *Grouper {
	if window == 0 {
		window = DefaultGroupWindow
	}
	if now == nil {
		now = time.Now
	}
	return &Grouper{window: window, now: now}
}

// enabled reports whether alerts are batched at all.
func (g *Grouper) enabled() bool { return g.window > 0 }

// batchKind is the kind of news a batch carries. One message tells one kind,
// because the fields at its top level (the event, the title, an SMS's status
// word) can only say one thing about everything in it.
type batchKind int

// The kinds, in the order a channel hears them: a batch waits for a batch of
// an earlier kind about the same incident, and a shutdown writes them in this
// order (see due and sortBatches).
const (
	// kindAlert is a first alert: an outage nobody has been told about.
	kindAlert batchKind = iota
	// kindReminder repeats an alert about an outage the channel already
	// heard of.
	kindReminder
	// kindRecovery is good news.
	kindRecovery
)

// String names the kind in a batch key and in the log.
func (k batchKind) String() string {
	switch k {
	case kindAlert:
		return "down"
	case kindReminder:
		return "reminder"
	default:
		return "up"
	}
}

// kindOf sorts an alert into the batch kind it travels in.
//
// A reminder is down news, but it is not the same news as a first alert: one
// says something new has broken, the other that something already known still
// is. In one batch, whichever came first decided the message's event, so a new
// outage could arrive as a "still down" repeat (and an SMS reading STILL DOWN),
// or a repeat as a fresh "is down".
func kindOf(a Alert) batchKind {
	switch {
	case !a.Down():
		return kindRecovery
	case state.Event(a.Event) == state.EventIncidentReminder:
		return kindReminder
	default:
		return kindAlert
	}
}

// groupKey identifies the batch an alert belongs to.
//
// Keyed on the channel and the kind of news. Three things deliberately stay
// apart: deliveries to different channels, because one may be a phone and the
// other a log; down-alerts from recoveries, because "three things broke and
// one came back" in a single message is a sentence nobody can act on at 03:00;
// and first alerts from reminders, because "is down" and "is still down" ask
// for different things of the reader, and one message can only say one of them.
func groupKey(channelID int64, kind batchKind) string {
	return fmt.Sprintf("%d:%s", channelID, kind)
}

// Summarise renders one message for a batch of alerts.
//
// A single alert is rendered exactly as it would have been without grouping.
// That matters more than it looks: the common case is one monitor failing on
// its own, and wrapping that in "1 monitor is down:" would make every ordinary
// alert read like a report.
func Summarise(alerts []Alert) Alert {
	if len(alerts) == 1 {
		return alerts[0]
	}

	// Oldest first, so the message reads in the order things happened and
	// the first line names what most likely caused the rest.
	sorted := make([]Alert, len(alerts))
	copy(sorted, alerts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	first := sorted[0]

	var names []string
	for _, a := range sorted {
		names = append(names, a.MonitorName)
	}

	out := Alert{
		Members:    sorted,
		Event:      first.Event,
		At:         first.At,
		StartedAt:  first.StartedAt,
		IncidentID: first.IncidentID,

		// GroupedNames drives the rendering; the singular fields stay
		// set from the first alert so a channel that only knows about
		// MonitorName still says something true.
		MonitorName:  first.MonitorName,
		MonitorType:  first.MonitorType,
		Target:       first.Target,
		GroupedNames: names,
		GroupedCause: sharedCause(sorted),
	}
	// A batch of recoveries that each replace their alert keeps saying so
	// when it is summarised again, as maintenance filtering does on every
	// attempt.
	out.ReplacesAlert = true
	for _, a := range sorted {
		out.ReplacesAlert = out.ReplacesAlert && a.ReplacesAlert
	}
	return out
}

// sharedCause returns the failure cause when every member agrees on it.
//
// Agreement is evidence: twenty monitors reporting "no such host" is a DNS
// failure, and naming it turns a list of symptoms into a diagnosis. When they
// disagree the honest answer is nothing at all, because an invented common
// cause is worse than none.
func sharedCause(alerts []Alert) string {
	cause := ""
	for _, a := range alerts {
		if a.Cause == "" {
			return ""
		}
		if cause == "" {
			cause = a.Cause
			continue
		}
		if a.Cause != cause {
			return ""
		}
	}
	return cause
}

// pending is one accumulating batch.
type pending struct {
	key      string
	channel  int64
	alerts   []Alert
	deadline time.Time

	monitorID  int64
	incidentID int64

	// kind is the kind of news, the other half of the key.
	kind batchKind
}

// Add records an alert against its batch and reports whether the batch is now
// due to be sent.
func (g *Grouper) add(batches map[string]*pending, channelID, monitorID int64, a Alert) *pending {
	kind := kindOf(a)
	key := groupKey(channelID, kind)

	b, ok := batches[key]
	if !ok {
		b = &pending{
			key:       key,
			channel:   channelID,
			deadline:  g.now().Add(g.window),
			monitorID: monitorID,
			kind:      kind,
		}
		batches[key] = b
	}
	b.alerts = append(b.alerts, a)
	if a.IncidentID != 0 && b.incidentID == 0 {
		b.incidentID = a.IncidentID
	}
	return b
}

// due reports the batches whose window has closed.
//
// A batch also waits for a batch of an earlier kind on its channel that
// carries news about one of the same incidents and has not gone out yet: a
// recovery for the alert or the reminder it closes, a reminder for the first
// alert it repeats. Without that, a recovery that joined a batch opened before
// its own alert was sent first; with it, the two close together and go out in
// order. Every batch waited for is open (or waiting itself on one that is), and
// an open batch closes within one window, so the wait is at most one window.
func (g *Grouper) due(batches map[string]*pending) []*pending {
	now := g.now()
	var out []*pending
	// Earlier kinds first: a batch released on this pass is gone from the
	// map by the time a later kind asks whether to wait for it, so whatever
	// a batch finds there is still being held.
	for _, kind := range []batchKind{kindAlert, kindReminder, kindRecovery} {
		for key, b := range batches {
			if b.kind != kind || now.Before(b.deadline) || b.waits(batches) {
				continue
			}
			out = append(out, b)
			delete(batches, key)
		}
	}
	sortBatches(out)
	return out
}

// waits reports whether b would go out before a batch of an earlier kind on its
// channel, still held, that shares one of its incidents.
func (b *pending) waits(batches map[string]*pending) bool {
	for kind := kindAlert; kind < b.kind; kind++ {
		if b.sharesIncident(batches[groupKey(b.channel, kind)]) {
			return true
		}
	}
	return false
}

// sharesIncident reports whether b and other carry news about one incident.
func (b *pending) sharesIncident(other *pending) bool {
	if other == nil {
		return false
	}
	held := map[int64]bool{}
	for _, a := range other.alerts {
		if a.IncidentID != 0 {
			held[a.IncidentID] = true
		}
	}
	for _, a := range b.alerts {
		if held[a.IncidentID] {
			return true
		}
	}
	return false
}

// sortBatches orders each channel's batches by kind: alerts, then reminders,
// then recoveries. The outbox keeps a channel's messages in the order they are
// written, so this order is the one the channel hears them in. Channels are
// ordered by id so a test can rely on the order, and a log reads the same way
// twice.
func sortBatches(batches []*pending) {
	sort.Slice(batches, func(i, j int) bool {
		if batches[i].channel != batches[j].channel {
			return batches[i].channel < batches[j].channel
		}
		return batches[i].kind < batches[j].kind
	})
}

// GroupedTitle renders the one-line summary for a batch.
//
// Only called for a real batch: Alert.Title() checks Grouped() first, so the
// n<=1 case here would be infinite recursion rather than a fallback.
func GroupedTitle(a Alert) string {
	verb := "are down"
	switch {
	case groupedReminder(a):
		verb = "are still down"
	case a.Down():
	case a.ReplacesAlert:
		verb = "were down and are back up"
	default:
		verb = "are back up"
	}
	return fmt.Sprintf("%d monitors %s", len(a.GroupedNames), verb)
}

// groupedReminder reports whether every alert in a batch is a reminder. Only
// then does its title say "still": a batch is one kind since reminders were
// kept apart from first alerts, but a row queued by an older version can still
// mix the two, and calling a new outage a repeat would hide that it is new.
func groupedReminder(a Alert) bool {
	if len(a.Members) == 0 {
		return false
	}
	for _, m := range a.Members {
		if state.Event(m.Event) != state.EventIncidentReminder {
			return false
		}
	}
	return true
}

// GroupedBody lists the members, with the shared cause when there is one.
//
// Only called for a real batch, for the same reason as GroupedTitle.
//
// The list is capped. A message naming sixty monitors is a wall of text that
// gets skimmed, and the count in the title already carries the scale; the
// dashboard is where the full list belongs.
func GroupedBody(a Alert) string {
	const maxNamed = 10

	var b strings.Builder
	if a.GroupedCause != "" {
		b.WriteString(a.GroupedCause + "\n\n")
	}

	shown := a.GroupedNames
	if len(shown) > maxNamed {
		shown = shown[:maxNamed]
	}
	for _, name := range shown {
		b.WriteString("• " + name + "\n")
	}
	if rest := len(a.GroupedNames) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "… and %d more\n", rest)
	}

	if !a.Down() && !a.StartedAt.IsZero() {
		fmt.Fprintf(&b, "\nDown for %s", durationWords(a.At.Sub(a.StartedAt)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// alertForStore rebuilds the store-facing pieces of a grouped delivery.
func (b *pending) delivery(payload string, event state.Event, due time.Time) store.Delivery {
	return store.Delivery{
		ChannelID:     b.channel,
		MonitorID:     b.monitorID,
		IncidentID:    b.incidentID,
		Event:         string(event),
		Payload:       payload,
		NextAttemptAt: due,
	}
}
