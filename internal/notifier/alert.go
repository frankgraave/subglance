// Package notifier delivers alerts to the channels a monitor is assigned to.
//
// It sits behind an outbox table rather than sending inline, for one reason
// that shapes the whole package: a check must never wait on a notification.
// A Slack webhook that takes thirty seconds to time out would, if called from
// the check path, delay every monitor behind it — turning one broken
// integration into a monitoring outage. So the runner writes rows and returns,
// and a worker here drains them.
//
// The same split is what makes retries possible at all. A delivery that fails
// is still in the table when the process restarts, which matters most in
// exactly the situation notifications exist for: the machine having a bad
// night.
package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// Alert is what a channel is asked to deliver.
//
// It is a rendered snapshot, not a set of ids to look up later: a retry an
// hour after the fact has to say what was true when it fired. "Down for 2
// minutes" that silently becomes "down for 62 minutes" on a retry is a lie,
// and a monitoring tool that lies is worse than one that stays quiet.
type Alert struct {
	MonitorID   int64  `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	MonitorType string `json:"monitor_type"`
	Target      string `json:"target"`

	Event string `json:"event"`

	IncidentID int64     `json:"incident_id,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	At         time.Time `json:"at"`

	Cause     string `json:"cause,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// ReminderCount is which reminder this is, zero for a first alert. A
	// channel can use it to say "still down" rather than repeating the
	// opening line, which is the difference between a useful nudge and the
	// nagging that teaches people to mute a monitor.
	ReminderCount int `json:"reminder_count,omitempty"`

	// GroupedNames lists every monitor in a batched alert, oldest failure
	// first. Empty for an ordinary single alert.
	//
	// When an uplink drops, twenty monitors are one problem seen twenty
	// times. Sending twenty messages is how a monitor teaches its owner to
	// mute it, so the notifier collects them and this field carries the
	// members.
	GroupedNames []string `json:"grouped_names,omitempty"`

	// GroupedCause is the failure cause when every member of a batch
	// reports the same one. Empty when they disagree: an invented common
	// cause would be worse than none.
	GroupedCause string `json:"grouped_cause,omitempty"`
}

// Grouped reports whether this alert covers more than one monitor.
func (a Alert) Grouped() bool { return len(a.GroupedNames) > 1 }

// Down reports whether this alert is bad news.
//
// Used for colour and emphasis by the channels that have any. Resolution is
// the only good news the state engine produces; everything else is a problem
// starting, being confirmed, or refusing to go away.
func (a Alert) Down() bool {
	return state.Event(a.Event) != state.EventIncidentResolved
}

// Title is the one-line summary, the part that becomes a push notification on
// a phone before anyone opens anything.
func (a Alert) Title() string {
	// A batch announces its size instead of naming one monitor, and every
	// channel inherits that by calling Title() — one place to change, five
	// senders that stay honest.
	if a.Grouped() {
		return GroupedTitle(a)
	}

	switch state.Event(a.Event) {
	case state.EventIncidentResolved:
		return fmt.Sprintf("%s is back up", a.MonitorName)
	case state.EventIncidentReminder:
		return fmt.Sprintf("%s is still down", a.MonitorName)
	case state.EventIncidentConfirmed:
		return fmt.Sprintf("%s is down", a.MonitorName)
	case state.EventIncidentOpened:
		return fmt.Sprintf("%s may be down", a.MonitorName)
	default:
		return fmt.Sprintf("%s: %s", a.MonitorName, a.Event)
	}
}

// Body is the human-readable detail under the title.
func (a Alert) Body() string {
	if a.Grouped() {
		return GroupedBody(a)
	}

	out := a.Target
	if a.Cause != "" {
		out += "\n" + a.Cause
	}
	if a.LastError != "" {
		out += "\n" + a.LastError
	}
	if !a.StartedAt.IsZero() && !a.Down() {
		out += fmt.Sprintf("\nDown for %s", durationWords(a.At.Sub(a.StartedAt)))
	} else if !a.StartedAt.IsZero() {
		out += fmt.Sprintf("\nSince %s", a.StartedAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	return out
}

// durationWords renders a gap the way a person would say it.
//
// Rounded, because the exact seconds of an outage are in the incident record
// and nobody reading an alert at 03:00 wants "2h13m47.8s".
func durationWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d days, %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// Encode renders an alert for storage in the outbox.
func (a Alert) Encode() (string, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("encode alert: %w", err)
	}
	return string(b), nil
}

// DecodeAlert parses a stored payload.
func DecodeAlert(payload string) (Alert, error) {
	var a Alert
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		return Alert{}, fmt.Errorf("decode alert: %w", err)
	}
	return a, nil
}

// Sender delivers one alert to one destination.
//
// Every channel type implements this and nothing else. Config validation lives
// with each implementation rather than in a shared switch, so adding a channel
// means adding a file instead of editing four.
type Sender interface {
	// Send delivers the alert, or returns why it could not.
	Send(ctx context.Context, cfg map[string]string, a Alert) error

	// Validate reports whether a config is usable, without sending
	// anything. The API calls this when a channel is created so a typo is
	// caught while someone is looking at the form, not at 03:00.
	Validate(cfg map[string]string) error
}

// Retryable marks an error worth trying again.
//
// The distinction matters: a 500 from Slack is a bad minute, while a 404 on a
// webhook URL is a bad URL, and retrying the second one twelve times only
// delays the moment the operator finds out it was wrong. Errors not wrapped in
// this type are treated as permanent.
type Retryable struct{ Err error }

func (r *Retryable) Error() string { return r.Err.Error() }
func (r *Retryable) Unwrap() error { return r.Err }

// retryable wraps an error as worth another attempt.
func retryable(format string, args ...any) error {
	return &Retryable{Err: fmt.Errorf(format, args...)}
}

// AlertFromStore builds an Alert from the records the runner already holds.
func AlertFromStore(m store.Monitor, inc store.Incident, event state.Event, at time.Time) Alert {
	return Alert{
		MonitorID:     m.ID,
		MonitorName:   m.Name,
		MonitorType:   m.Type,
		Target:        m.Target,
		Event:         string(event),
		IncidentID:    inc.ID,
		StartedAt:     inc.StartedAt,
		At:            at,
		Cause:         inc.Cause,
		LastError:     inc.LastError,
		ReminderCount: inc.ReminderCount,
	}
}
