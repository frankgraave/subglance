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
	return validateWebhook(cfg, true)
}

// validateWebhook checks every webhook setting. With headerRules false a
// header line is only held to the Name: value form, the one rule headers
// were saved under before the rest (webhookHeadersProblem) existed.
func validateWebhook(cfg map[string]string, headerRules bool) error {
	if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
		return err
	}
	return webhookConfigProblem(cfg, headerRules)
}

// webhookConfigProblem checks the settings a webhook has beyond the scheme
// and host of its URL. The API asks for it through ValidateConfig when a
// channel is saved, so the form shows the same sentence a delivery would
// fail with.
func webhookConfigProblem(cfg map[string]string, headerRules bool) error {
	if err := validateWebhookURL(cfg["url"]); err != nil {
		return onKey("url", err)
	}
	if _, err := webhookMethod(cfg); err != nil {
		return onKey("method", err)
	}
	check := webhookHeadersProblem
	if !headerRules {
		check = webhookHeaderLinesProblem
	}
	if err := check(cfg["headers"]); err != nil {
		return onKey("headers", err)
	}
	if strings.TrimSpace(cfg["body"]) != "" {
		return onKey("body", validateWebhookBody(cfg["body"], isJSONType(webhookContentType(cfg))))
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
	txnID := webhookTxnID(ctx, target, a)
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

// webhookHeaderLinesProblem is the Name: value rule alone, the one header
// lines were saved under before webhookHeadersProblem.
func webhookHeaderLinesProblem(raw string) error {
	for i, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) != "" && !strings.Contains(line, ":") {
			return &configError{fmt.Sprintf("headers line %d is not in Name: value form", i+1)}
		}
	}
	return nil
}

// webhookHeadersProblem refuses the header lines a delivery could not send
// as they are written. Delivery does not validate, so each of these used to
// be saved and then cost every alert routed to the channel:
//
//   - a line without a colon is skipped at send time, and an operator who
//     added an auth header would never learn why the endpoint kept
//     answering 401;
//   - a name that is not an HTTP token, or a value holding a control
//     character other than a tab, fails the request in net/http before a
//     connection is made, and the failure is retried as if the endpoint
//     were down;
//   - two lines naming one header (names ignore case) send only the last,
//     while the body is escaped for the first Content-Type line, so the
//     body can go out escaped for one type and labelled as another.
//
// The name and value rules are the ones an http monitor's request headers
// are held to (checker.RequestHeaderProblem), which a test holds to what the
// transport does. A refusal names the line, and the header when its name is
// one, never the value or the whole line: the setting is masked as a
// credential, and a refusal is also the error a channel test logs.
func webhookHeadersProblem(raw string) error {
	seen := map[string]int{}
	for i, line := range strings.Split(raw, "\n") {
		n := i + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return &configError{fmt.Sprintf("headers line %d is not in Name: value form", n)}
		}
		name = strings.TrimSpace(name)
		if !checker.ValidHeaderName(name) {
			// Not quoted: a name that is no header name is often a
			// credential typed on the wrong side of the colon.
			return &configError{fmt.Sprintf("headers line %d has no valid header name before its colon: a name cannot be empty or contain spaces or other separators", n)}
		}
		if !checker.ValidHeaderValue(strings.TrimSpace(value)) {
			return &configError{fmt.Sprintf("headers line %d has a control character in the value of %s", n, name)}
		}
		folded := strings.ToLower(name)
		if first, dup := seen[folded]; dup {
			return &configError{fmt.Sprintf("headers lines %d and %d both set %s, because header names ignore case; keep one of them", first, n, name)}
		}
		seen[folded] = n
	}
	return nil
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
