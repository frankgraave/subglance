package notifier

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
)

// GotifySender posts to a Gotify server's message endpoint.
//
// Gotify only exists self-hosted, so it is typically on a private address; that
// case is the --allow-private-targets opt-in, and the error a refused delivery
// returns says so (see httpSend).
//
// The application token travels in the X-Gotify-Key header, never in the
// ?token= query parameter Gotify also accepts. A URL ends up in error
// messages, proxy logs and the outbox's last_error column; a header does not.
type GotifySender struct{ client *http.Client }

// NewGotifySender builds a Gotify channel. The guard may be nil.
func NewGotifySender(guard *checker.Guard) *GotifySender {
	return &GotifySender{client: newHTTPClient(guard)}
}

// Gotify priorities run 0 to 10. The Android client makes a sound from 4 and
// a pop-up from 8, so the defaults are "wake me" for an outage and "tell me
// quietly" for a recovery.
const (
	gotifyPriorityDown = 8
	gotifyPriorityUp   = 4
	gotifyPriorityMax  = 10
)

// Validate checks the server URL, the token and both priorities.
func (s *GotifySender) Validate(cfg map[string]string) error {
	if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg["token"]) == "" {
		return &configError{"token is required (an application token from Gotify's Apps page)"}
	}
	for _, key := range []string{"priority_down", "priority_up"} {
		if _, err := gotifyPriority(cfg[key], 0); err != nil {
			return &configError{key + " " + err.Error()}
		}
	}
	return nil
}

// gotifyPriority parses a configured priority, falling back when unset.
func gotifyPriority(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > gotifyPriorityMax {
		return 0, fmt.Errorf("must be a whole number from 0 to %d", gotifyPriorityMax)
	}
	return n, nil
}

// gotifyEndpoint appends /message to the server URL, keeping any path prefix:
// a Gotify behind a reverse proxy under a sub-path is common, and dropping
// the prefix would post to the proxy's own root.
func gotifyEndpoint(server string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil {
		return "", fmt.Errorf("url is not a valid URL: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/message"
	u.RawPath = ""
	return u.String(), nil
}

// Send posts the message.
func (s *GotifySender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	priority, err := gotifyPriority(cfg["priority_up"], gotifyPriorityUp)
	if a.Down() {
		priority, err = gotifyPriority(cfg["priority_down"], gotifyPriorityDown)
	}
	if err != nil {
		return &configError{err.Error()}
	}

	endpoint, err := gotifyEndpoint(cfg["url"])
	if err != nil {
		return &configError{err.Error()}
	}

	payload := map[string]any{
		"title":    a.Title(),
		"message":  a.Body(),
		"priority": priority,
	}
	if link := primaryLink(a); link != "" {
		// The Gotify apps open the click URL when the notification is
		// tapped; the links in the text are for the web client, which
		// does not.
		payload["message"] = a.Body() + "\n\n" + linkLines(a)
		payload["extras"] = map[string]any{
			"client::notification": map[string]any{"click": map[string]string{"url": link}},
		}
	}

	req, err := jsonRequest(ctx, endpoint, payload)
	if err != nil {
		return err
	}
	req.Header.Set("X-Gotify-Key", strings.TrimSpace(cfg["token"]))
	return httpSend(ctx, s.client, req)
}
