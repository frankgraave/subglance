package notifier

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Links back to SubGlance.
//
// Whoever is woken by an alert at 03:00 wants the page about it, not a login
// screen and a search. A message can only link there if SubGlance knows the
// address it is reached at, and it cannot work that out for itself: behind a
// reverse proxy the host, the scheme and often a path prefix are the
// proxy's. So the address is a start-up setting, --base-url, and without it
// every message stays exactly as it was.

// ParseBaseURL checks the address SubGlance is reached at and returns it in
// the one form links are built from: scheme, host and any path prefix, with
// no slash at the end. An empty value means links are off and is returned
// as it is.
//
// The refusals are about what ends up in a message. Credentials would be
// sent to every chat service an alert goes through. A query or a fragment
// would sit in the middle of every link, since a link is the base with a
// path appended.
func ParseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%q is not a URL: %w", raw, errors.Unwrap(err))
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme != "http" && scheme != "https":
		return "", fmt.Errorf("%q must be an absolute http:// or https:// address, such as https://status.example.com", raw)
	case u.Host == "" || u.Opaque != "":
		return "", fmt.Errorf("%q has no host; write it as https://status.example.com", raw)
	case u.User != nil:
		return "", fmt.Errorf("%q contains a user name or password, which every alert would carry to its channel", raw)
	case u.RawQuery != "" || u.ForceQuery:
		return "", fmt.Errorf("%q has a query string; give the address only, with any path prefix", raw)
	case u.Fragment != "" || strings.Contains(raw, "#"):
		return "", fmt.Errorf("%q has a #fragment; give the address only, with any path prefix", raw)
	}
	return scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// withLinks returns the alert with IncidentURL and MonitorURL filled in from
// base, a value ParseBaseURL returned. An empty base returns it unchanged.
//
// An alert about one monitor links to its incident and to the monitor. One
// about several, a grouped alert or a quiet-hours summary, links to the
// incidents screen, where all of them are, and each member links to its own.
// A message about SubGlance itself (a failed backup, a failing channel, the
// test button) names no monitor and gets no links.
func withLinks(a Alert, base string) Alert {
	if base == "" {
		return a
	}
	if len(a.Members) > 0 {
		members := make([]Alert, len(a.Members))
		for i, m := range a.Members {
			members[i] = withLinks(m, base)
		}
		a.Members = members
		a.IncidentURL = base + "/incidents"
		return a
	}
	if a.MonitorID == 0 {
		return a
	}
	a.MonitorURL = fmt.Sprintf("%s/monitors/%d", base, a.MonitorID)
	if a.IncidentID != 0 {
		a.IncidentURL = fmt.Sprintf("%s/incidents#incident-%d", base, a.IncidentID)
	}
	return a
}

// linkLines is the links as text for a channel that sends plain text, one
// per line, or "" when there are none.
func linkLines(a Alert) string {
	var lines []string
	if a.IncidentURL != "" {
		lines = append(lines, "Incident: "+a.IncidentURL)
	}
	if a.MonitorURL != "" {
		lines = append(lines, "Monitor: "+a.MonitorURL)
	}
	return strings.Join(lines, "\n")
}

// primaryLink is the one link a message with room for only one carries, the
// incident before the monitor: it is the page about what just happened.
func primaryLink(a Alert) string {
	if a.IncidentURL != "" {
		return a.IncidentURL
	}
	return a.MonitorURL
}
