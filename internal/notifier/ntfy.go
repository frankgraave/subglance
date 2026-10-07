package notifier

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
)

// NtfySender publishes to an ntfy topic.
//
// ntfy is a push service that is as often self-hosted as used at ntfy.sh, which
// is why the server is a setting with a default rather than a constant: the
// person who runs it on their own network is exactly who this channel is for.
//
// It uses ntfy's JSON publishing (a POST of {topic, title, message, ...} to the
// server root) rather than the plain-body form with X-Title headers. Header
// values cannot carry a newline or arbitrary UTF-8 reliably, and a monitor name
// has to arrive exactly as the operator typed it.
type NtfySender struct{ client *http.Client }

// NewNtfySender builds an ntfy channel. The guard may be nil.
func NewNtfySender(guard *checker.Guard) *NtfySender {
	return &NtfySender{client: newHTTPClient(guard)}
}

// DefaultNtfyServer is used when a channel names no server of its own.
const DefaultNtfyServer = "https://ntfy.sh"

// ntfyTopicPattern is the topic shape the ntfy server accepts. Checking it here
// turns a topic with a space or a slash in it into an error on the form rather
// than a 404 at the first outage.
var ntfyTopicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// ntfy priorities: 3 is the default, 4 is "high" (a longer vibration and a
// pop-over on Android). An outage earns the pop-over; a recovery does not.
const (
	ntfyPriorityDown = 4
	ntfyPriorityUp   = 3
)

// Validate checks the topic, the optional server URL and the credentials.
func (s *NtfySender) Validate(cfg map[string]string) error {
	topic := strings.TrimSpace(cfg["topic"])
	if topic == "" {
		return &configError{"topic is required"}
	}
	if !ntfyTopicPattern.MatchString(topic) {
		return &configError{"topic may contain only letters, digits, - and _ (at most 64)"}
	}
	if server := strings.TrimSpace(cfg["url"]); server != "" {
		if err := validateHTTPSURL(server, "url"); err != nil {
			return err
		}
		// The JSON form is published to the server root and names the topic
		// in the body. A URL with a path would post to that path instead,
		// and ntfy does not support being served from a sub-path at all.
		if u, err := url.Parse(server); err != nil || !ntfyServerRoot(u) {
			return &configError{"url must name the ntfy server, not a topic or other path"}
		}
	}
	// A token of only whitespace would go out as an empty bearer token and
	// be refused at the first outage rather than here.
	if cfg["token"] != "" && strings.TrimSpace(cfg["token"]) == "" {
		return &configError{"token must not be only whitespace"}
	}
	// A token and a username/password pair are two ways to say the same
	// thing. Accepting both would mean silently ignoring one of them, and the
	// operator would debug the one that is not being sent.
	if cfg["token"] != "" && (cfg["username"] != "" || cfg["password"] != "") {
		return &configError{"set either token or username and password, not both"}
	}
	if (cfg["username"] == "") != (cfg["password"] == "") {
		return &configError{"username and password must be set together"}
	}
	return nil
}

// ntfyServerRoot reports whether u names a server root: no path beyond a
// single trailing slash, and no query or fragment to carry one either.
func ntfyServerRoot(u *url.URL) bool {
	return (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}

// Send publishes the alert.
func (s *NtfySender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	server := strings.TrimSpace(cfg["url"])
	if server == "" {
		server = DefaultNtfyServer
	}

	priority, tag := ntfyPriorityUp, "white_check_mark"
	if a.Down() {
		priority, tag = ntfyPriorityDown, "rotating_light"
	}

	payload := map[string]any{
		"topic":    strings.TrimSpace(cfg["topic"]),
		"title":    a.Title(),
		"message":  a.Body(),
		"priority": priority,
		// A tag that names an emoji is drawn in front of the title, which
		// is what makes an outage and a recovery tell apart on a lock
		// screen without reading either.
		"tags": []string{tag},
	}

	if link := primaryLink(a); link != "" {
		// Tapping the notification opens the incident, and a button
		// per link opens either page without opening the ntfy app.
		payload["click"] = link
		payload["actions"] = ntfyActions(a)
	}

	req, err := jsonRequest(ctx, server, payload)
	if err != nil {
		return err
	}
	// A pasted token often carries a stray space or newline; the header
	// keeps any whitespace after "Bearer ", so send the trimmed value.
	token := strings.TrimSpace(cfg["token"])
	switch {
	case token != "":
		req.Header.Set("Authorization", "Bearer "+token)
	case cfg["username"] != "":
		req.SetBasicAuth(cfg["username"], cfg["password"])
	}
	return httpSend(ctx, s.client, req)
}

// ntfyActions is one "view" button per link.
func ntfyActions(a Alert) []map[string]string {
	var actions []map[string]string
	if a.IncidentURL != "" {
		label := "Incident"
		if len(a.Members) > 0 {
			label = "Incidents"
		}
		actions = append(actions, map[string]string{"action": "view", "label": label, "url": a.IncidentURL})
	}
	if a.MonitorURL != "" {
		actions = append(actions, map[string]string{"action": "view", "label": "Monitor", "url": a.MonitorURL})
	}
	return actions
}
