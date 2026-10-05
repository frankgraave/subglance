package notifier

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
)

// WebhookSender posts the alert to a URL of the operator's choosing.
//
// By default the payload is the Alert struct as-is. That is deliberate: this
// is the channel people build their own automation on, and a stable,
// documented shape is worth more to them than a prettier one. The other
// channels format for a specific product; this one formats for a script.
//
// A channel with a body template sends that instead, filled in from the
// alert (see webhook_body.go). That is what lets a service with its own
// format, such as Teams, Matrix or Pushover, take the alert without a
// translating server in between.
type WebhookSender struct{ client *http.Client }

// NewWebhookSender builds a webhook channel. The guard may be nil, which
// means outbound deliveries are not restricted.
func NewWebhookSender(guard *checker.Guard) *WebhookSender {
	return &WebhookSender{client: newHTTPClient(guard)}
}

// Validate checks the URL, the method, any custom headers and the body
// template.
func (s *WebhookSender) Validate(cfg map[string]string) error {
	if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
		return err
	}
	return ValidateWebhookConfig(cfg)
}

// ValidateWebhookConfig checks the settings a webhook has beyond its URL.
// The API calls it when a channel is saved, so the form shows the same
// sentence a delivery would fail with.
func ValidateWebhookConfig(cfg map[string]string) error {
	if err := validateWebhookURL(cfg["url"]); err != nil {
		return err
	}
	if _, err := webhookMethod(cfg); err != nil {
		return err
	}
	// A header line that does not parse would be dropped silently at send
	// time, and an operator who added an auth header would never learn why
	// their endpoint kept answering 401.
	for _, line := range splitHeaderLines(cfg["headers"]) {
		if _, _, ok := strings.Cut(line, ":"); !ok {
			return &configError{fmt.Sprintf("header %q is not in Name: value form", line)}
		}
	}
	if strings.TrimSpace(cfg["body"]) != "" {
		return validateWebhookBody(cfg["body"], isJSONType(webhookContentType(cfg)))
	}
	return nil
}

// Send delivers the alert.
func (s *WebhookSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	method, err := webhookMethod(cfg)
	if err != nil {
		return err
	}
	target := cfg["url"]
	txnID := webhookTxnID(target, a)
	target = placeholderPattern.ReplaceAllLiteralString(target, txnID)

	var req *http.Request
	if tpl := cfg["body"]; strings.TrimSpace(tpl) != "" {
		contentType := webhookContentType(cfg)
		req, err = http.NewRequestWithContext(ctx, method, target,
			bytes.NewReader(renderWebhookBody(tpl, contentType, a, txnID)))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", userAgent)
	} else {
		req, err = jsonRequest(ctx, target, a)
		if err != nil {
			return err
		}
		req.Method = method
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
