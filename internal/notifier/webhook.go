package notifier

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
)

// WebhookSender posts the alert as JSON to a URL of the operator's choosing.
//
// The payload is the Alert struct as-is. That is deliberate: this is the
// channel people build their own automation on, and a stable, documented shape
// is worth more to them than a prettier one. The other channels format for a
// specific product; this one formats for a script.
type WebhookSender struct{ client *http.Client }

// NewWebhookSender builds a webhook channel. The guard may be nil, which
// means outbound deliveries are not restricted.
func NewWebhookSender(guard *checker.Guard) *WebhookSender {
	return &WebhookSender{client: newHTTPClient(guard)}
}

// Validate checks the URL and any custom headers.
func (s *WebhookSender) Validate(cfg map[string]string) error {
	if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
		return err
	}
	// A header line that does not parse would be dropped silently at send
	// time, and an operator who added an auth header would never learn why
	// their endpoint kept answering 401.
	for _, line := range splitHeaderLines(cfg["headers"]) {
		if _, _, ok := strings.Cut(line, ":"); !ok {
			return fmt.Errorf("header %q is not in Name: value form", line)
		}
	}
	return nil
}

// Send posts the alert.
func (s *WebhookSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	req, err := jsonRequest(ctx, cfg["url"], a)
	if err != nil {
		return err
	}

	for _, line := range splitHeaderLines(cfg["headers"]) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
	}

	return httpSend(ctx, s.client, req)
}

// splitHeaderLines parses the newline-separated header config.
//
// Newline-separated rather than JSON because this value is typed into a
// textarea by a person, and "Authorization: Bearer xyz" on its own line is
// what they already know from every other tool.
func splitHeaderLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
