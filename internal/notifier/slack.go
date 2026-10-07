package notifier

import (
	"context"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
)

// SlackSender posts a Block Kit message to a Slack incoming webhook.
//
// The text field is set as well as the blocks, because Slack uses it for the
// mobile push notification and the desktop preview. A message with blocks but
// no text arrives on a phone as "SubGlance sent a message", which is exactly
// the moment the content mattered most.
type SlackSender struct{ client *http.Client }

// NewSlackSender builds a Slack channel. The guard may be nil.
func NewSlackSender(guard *checker.Guard) *SlackSender {
	return &SlackSender{client: newHTTPClient(guard)}
}

// Validate checks the webhook URL.
func (s *SlackSender) Validate(cfg map[string]string) error {
	return validateHTTPSURL(cfg["url"], "url")
}

// Send posts the message.
func (s *SlackSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	marker := ":large_green_circle:"
	if a.Down() {
		marker = ":red_circle:"
	}

	payload := map[string]any{
		"text": a.Title(),
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": marker + " *" + a.Title() + "*",
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": a.Body(),
				},
			},
		},
	}

	if links := slackLinks(a); links != "" {
		payload["blocks"] = append(payload["blocks"].([]map[string]any), map[string]any{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": links},
		})
	}

	req, err := jsonRequest(ctx, cfg["url"], payload)
	if err != nil {
		return err
	}
	return httpSend(ctx, s.client, req)
}

// slackLinks is the links as Slack mrkdwn, "" when the alert has none. A
// named link rather than the bare address: a Slack message is read on a
// phone, where a long URL wraps over three lines and says nothing.
func slackLinks(a Alert) string {
	var parts []string
	if a.IncidentURL != "" {
		label := "Open the incident"
		if len(a.Members) > 0 {
			label = "Open the incidents"
		}
		parts = append(parts, "<"+a.IncidentURL+"|"+label+">")
	}
	if a.MonitorURL != "" {
		parts = append(parts, "<"+a.MonitorURL+"|Open the monitor>")
	}
	return strings.Join(parts, " · ")
}
