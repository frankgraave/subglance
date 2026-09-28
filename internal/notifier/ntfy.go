package notifier

import (
	"context"
	"net/http"
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
	if strings.TrimSpace(cfg["url"]) != "" {
		if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
			return err
		}
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

	req, err := jsonRequest(ctx, server, payload)
	if err != nil {
		return err
	}
	switch {
	case cfg["token"] != "":
		req.Header.Set("Authorization", "Bearer "+cfg["token"])
	case cfg["username"] != "":
		req.SetBasicAuth(cfg["username"], cfg["password"])
	}
	return httpSend(ctx, s.client, req)
}
