package notifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/store"
)

// A webhook's body template.
//
// Teams, Matrix and Pushover each want their own JSON, and none of them reads
// SubGlance's payload. A template lets one channel type reach all three
// without a sender per service. It is deliberately not a template engine: a
// fixed set of names, replaced by their values, with no conditions, loops,
// includes or functions. Template engines with includes are how a webhook body
// became a way to read files off the server elsewhere, and nothing a JSON body
// needs requires one.

// WebhookBodyMaxLen is the longest body template a webhook may hold. Other
// channel settings stop at 2048 characters; an adaptive card with a few
// facts in it is already close to that, and the body is the one setting
// that is prose-sized by nature.
const WebhookBodyMaxLen = 8192

// webhookPlaceholders maps each name a body may use to the value it stands
// for. The names are a published contract: renaming one breaks every
// template that uses it, so a new name is added beside an old one rather
// than replacing it.
var webhookPlaceholders = map[string]func(Alert) string{
	// One line that covers any alert, grouped and digest ones included:
	// the title a push notification shows.
	"summary": func(a Alert) string { return a.Title() },
	// The detail under it, several lines for a grouped alert or a digest.
	"details":      func(a Alert) string { return a.Body() },
	"status":       alertStatus,
	"event":        func(a Alert) string { return a.Event },
	"monitor_name": func(a Alert) string { return a.MonitorName },
	"monitor_type": func(a Alert) string { return a.MonitorType },
	"target":       func(a Alert) string { return a.Target },
	"cause":        func(a Alert) string { return a.Cause },
	"last_error":   func(a Alert) string { return a.LastError },
	"started_at":   func(a Alert) string { return timestamp(a.StartedAt) },
	"at":           func(a Alert) string { return timestamp(a.At) },
	// txn_id is filled in by the sender, which knows the URL it is for.
	"txn_id": nil,
}

// WebhookPlaceholderNames lists the names a body template may use, sorted.
func WebhookPlaceholderNames() []string {
	names := make([]string, 0, len(webhookPlaceholders))
	for name := range webhookPlaceholders {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// alertStatus is "down", "expiring" or "up": the one-word answer a template
// without conditions needs to tell bad news from good.
func alertStatus(a Alert) string {
	switch {
	case a.Digest:
		// The digest itself is not a notice, but it can carry one. A night
		// that left only a certificate notice open is "expiring": Down()
		// is true for it, and "down" would call it an outage.
		entries := digestEntries(a)
		switch {
		case digestStillDown(entries) > 0:
			return "down"
		case digestNoticeOpen(entries):
			return "expiring"
		}
		return "up"
	case a.Notice && a.Down():
		// Not "down": the service answers. A template that prints the
		// status has to be able to say so.
		return "expiring"
	case a.Down():
		return "down"
	}
	return "up"
}

// timestamp writes a time as RFC 3339 in UTC, and nothing for a zero time:
// "0001-01-01T00:00:00Z" in a chat message reads as a bug, not as "none".
func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// placeholderPattern matches one {{name}}, spaces inside the braces allowed.
var placeholderPattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// validateWebhookBody checks a body template the way delivery will read it,
// so a mistake is reported on the form and not as a 400 at 03:00.
//
// Every value is inserted as the text of a JSON string, escaped, so a monitor
// name with a quote or a newline in it cannot break the document. That makes
// validity a property of the template alone: each placeholder is replaced by
// a run of letters of the same length, which is what any value looks like
// once escaped, and the result has to parse. Same length, so a JSON error's
// offset is a place in the text the person typed.
func validateWebhookBody(tpl string, jsonBody bool) error {
	if utf8.RuneCountInString(tpl) > WebhookBodyMaxLen {
		return &configError{fmt.Sprintf("body must be %d characters or fewer", WebhookBodyMaxLen)}
	}
	masked := []byte(tpl)
	var spans [][]int
	for _, m := range placeholderPattern.FindAllStringSubmatchIndex(tpl, -1) {
		name := strings.TrimSpace(tpl[m[2]:m[3]])
		if _, ok := webhookPlaceholders[name]; !ok {
			return &configError{fmt.Sprintf("body has an unknown placeholder {{%s}} at %s; the placeholders are %s",
				name, position(tpl, m[0]), placeholderList())}
		}
		for i := m[0]; i < m[1]; i++ {
			masked[i] = 'x'
		}
		spans = append(spans, m[:2])
	}
	if i := bytes.Index(masked, []byte("{{")); i >= 0 {
		return &configError{fmt.Sprintf("body has a {{ at %s that is not closed with }}", position(tpl, i))}
	}
	if !jsonBody {
		return nil
	}

	var v any
	err := json.Unmarshal(masked, &v)
	var syntax *json.SyntaxError
	if err == nil || !errors.As(err, &syntax) {
		return nil
	}
	at := max(int(syntax.Offset)-1, 0)
	for _, s := range spans {
		if at >= s[0] && at < s[1] {
			return &configError{fmt.Sprintf("body has %s at %s outside a JSON string; "+
				"a value is inserted as text, so put it between quotes", tpl[s[0]:s[1]], position(tpl, s[0]))}
		}
	}
	if strings.Contains(syntax.Error(), "unexpected end") {
		return &configError{"body is not valid JSON: it ends before the document does"}
	}
	return &configError{fmt.Sprintf("body is not valid JSON at %s: %s", position(tpl, at), syntax.Error())}
}

// validateWebhookURL allows one placeholder in the URL, {{txn_id}}, which a
// Matrix room address ends in. Any other value would need escaping for a
// path, which is a different rule from the body's, and nothing asks for it.
func validateWebhookURL(raw string) error {
	for _, m := range placeholderPattern.FindAllStringSubmatchIndex(raw, -1) {
		if name := strings.TrimSpace(raw[m[2]:m[3]]); name != "txn_id" {
			return &configError{fmt.Sprintf("url may use only the {{txn_id}} placeholder, not {{%s}}", name)}
		}
	}
	if strings.Contains(placeholderPattern.ReplaceAllString(raw, ""), "{{") {
		return &configError{"url has a {{ that is not closed with }}"}
	}
	return nil
}

// placeholderList is the placeholder names for an error message.
func placeholderList() string {
	names := WebhookPlaceholderNames()
	for i, n := range names {
		names[i] = "{{" + n + "}}"
	}
	return strings.Join(names, ", ")
}

// position writes a byte offset as the line and column a person counts.
func position(s string, offset int) string {
	before := s[:offset]
	line := strings.Count(before, "\n") + 1
	col := utf8.RuneCountInString(before[strings.LastIndex(before, "\n")+1:]) + 1
	return fmt.Sprintf("line %d, column %d", line, col)
}

// renderWebhookBody fills a validated template in, escaping each value for
// the Content-Type the body is sent as: the inside of a JSON string for JSON,
// a form value for a form, and as it is for anything else, where there is no
// structure for a value to break.
func renderWebhookBody(tpl, contentType string, a Alert, txnID string) []byte {
	escape := func(s string) string { return s }
	switch {
	case isJSONType(contentType):
		escape = jsonText
	case isFormType(contentType):
		escape = url.QueryEscape
	}
	out := placeholderPattern.ReplaceAllStringFunc(tpl, func(m string) string {
		name := strings.TrimSpace(m[2 : len(m)-2])
		if name == "txn_id" {
			return txnID
		}
		value, ok := webhookPlaceholders[name]
		if !ok || value == nil {
			return m
		}
		return escape(value(a))
	})
	return []byte(out)
}

// jsonText escapes a value as the inside of a JSON string, without the
// quotes, which the template supplies.
func jsonText(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	// HTML escaping would turn "<" into \u003c: valid, and correct once
	// decoded, but a receiver that shows the raw body to someone debugging a
	// template should show what was meant.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	return string(out[1 : len(out)-1])
}

// webhookTxnID names one delivery for a receiver that deduplicates, as
// Matrix does with the transaction id in its URL.
//
// Derived rather than drawn at random, so a retry carries the same id and a
// receiver that already took the first attempt treats the second as a
// repeat. A queued delivery is named by its outbox row (see
// withDeliveryKey), not by what it says: maintenance is applied again before
// every attempt and can take a member out of a grouped alert, and a retry
// whose id followed the content would then post the same delivery twice. A
// send outside the outbox, the channel's test, has no row and is named by its
// alert, which carries the moment the test was pressed.
//
// The URL is part of it because a Matrix transaction id is scoped to the
// access token, not the room: two rooms reached with one token would
// otherwise swallow each other's copy.
func webhookTxnID(ctx context.Context, rawURL string, a Alert) string {
	seed := []byte(rawURL + "\n")
	if key := deliveryKey(ctx); key != "" {
		seed = append(seed, "delivery "+key...)
	} else {
		payload, _ := json.Marshal(a) // an Alert always encodes
		seed = append(seed, payload...)
	}
	sum := sha256.Sum256(seed)
	return "subglance-" + hex.EncodeToString(sum[:12])
}

// deliveryKeyCtx is the context key for the outbox row a send belongs to.
type deliveryKeyCtx struct{}

// withDeliveryKey tells a sender which outbox row it is sending, for a
// receiver that deduplicates by an id of the sender's choosing.
//
// The row id with its creation time, because SQLite may hand a deleted row's
// id to a new row once the outbox has been pruned: the pair is never reused.
// Passed in the context rather than on the Sender interface because only the
// webhook uses it, and the channel's test has no row to name.
func withDeliveryKey(ctx context.Context, d store.Delivery) context.Context {
	return context.WithValue(ctx, deliveryKeyCtx{}, fmt.Sprintf("%d-%d", d.ID, d.CreatedAt.Unix()))
}

// deliveryKey is the key withDeliveryKey set, or "" outside the outbox.
func deliveryKey(ctx context.Context) string {
	key, _ := ctx.Value(deliveryKeyCtx{}).(string)
	return key
}

// fieldError is a configuration error about one config key, so the API can
// say which field to correct and a form can put the sentence under it.
type fieldError struct {
	key string
	err error
}

func (e *fieldError) Error() string { return e.err.Error() }
func (e *fieldError) Unwrap() error { return e.err }

// onKey marks err as being about the config key, leaving nil as nil.
func onKey(key string, err error) error {
	if err == nil {
		return nil
	}
	return &fieldError{key: key, err: err}
}

// ConfigKey names the config key a validation error is about, or "" when the
// error is not about a single key.
func ConfigKey(err error) string {
	var f *fieldError
	if errors.As(err, &f) {
		return f.key
	}
	return ""
}

// webhookMethod is the HTTP method a webhook sends with: POST unless the
// channel says PUT, which Matrix requires.
func webhookMethod(cfg map[string]string) (string, error) {
	switch m := strings.ToUpper(strings.TrimSpace(cfg["method"])); m {
	case "", http.MethodPost:
		return http.MethodPost, nil
	case http.MethodPut:
		return http.MethodPut, nil
	default:
		return "", &configError{fmt.Sprintf("method must be POST or PUT, not %q", cfg["method"])}
	}
}

// webhookContentType is the Content-Type a webhook sends: application/json
// unless one of its headers names another.
func webhookContentType(cfg map[string]string) string {
	for _, line := range splitHeaderLines(cfg["headers"]) {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return strings.TrimSpace(value)
		}
	}
	return "application/json"
}

// isJSONType reports whether a Content-Type promises JSON: application/json
// itself, or a type with the +json suffix.
func isJSONType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// isFormType reports whether a Content-Type is an HTML form encoding.
func isFormType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}
