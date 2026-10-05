package notifier

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// hostileAlert carries every character that could end a JSON string or a line
// early, in every field a template can name.
func hostileAlert() Alert {
	nasty := "a \"quoted\" name\nwith a newline, a tab\t, a backslash \\ and </script>"
	return Alert{
		MonitorID: 7, MonitorName: nasty, MonitorType: "http", Target: nasty,
		Event: string(state.EventIncidentConfirmed), IncidentID: 3,
		StartedAt: time.Date(2026, 10, 2, 3, 12, 40, 0, time.UTC),
		At:        time.Date(2026, 10, 2, 3, 14, 10, 0, time.UTC),
		Cause:     "timeout", LastError: nasty,
	}
}

// hookRequest is one request a test endpoint saw.
type hookRequest struct {
	method, path, contentType string
	header                    http.Header
	body                      []byte
}

// recordingEndpoint answers 204 and keeps every request it was sent.
func recordingEndpoint(t *testing.T) (*httptest.Server, func() []hookRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []hookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, hookRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Clone(), body})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []hookRequest {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// TestWebhookBodyValuesCannotBreakTheJSON is the property the whole design
// rests on: whatever a monitor is called and whatever its error says, the
// body that leaves is the JSON document the template describes, with each
// value intact inside it.
func TestWebhookBodyValuesCannotBreakTheJSON(t *testing.T) {
	srv, got := recordingEndpoint(t)
	a := hostileAlert()
	tpl := `{"name": "{{monitor_name}}", "error": "{{ last_error }}", "both": "{{target}} / {{cause}}"}`
	if err := NewWebhookSender(nil).Validate(map[string]string{"url": srv.URL, "body": tpl}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := NewWebhookSender(nil).Send(context.Background(),
		map[string]string{"url": srv.URL, "body": tpl}, a); err != nil {
		t.Fatalf("send: %v", err)
	}

	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("endpoint saw %d requests, want 1", len(reqs))
	}
	var doc map[string]string
	if err := json.Unmarshal(reqs[0].body, &doc); err != nil {
		t.Fatalf("the body is not JSON: %v\n%s", err, reqs[0].body)
	}
	if doc["name"] != a.MonitorName || doc["error"] != a.LastError || doc["both"] != a.Target+" / timeout" {
		t.Errorf("values did not survive the template: %#v", doc)
	}
	if reqs[0].contentType != "application/json" || reqs[0].method != http.MethodPost {
		t.Errorf("request = %s with %q, want POST with application/json", reqs[0].method, reqs[0].contentType)
	}
}

// TestWebhookBodyFillsEveryPlaceholder checks each name against the alert
// field it promises, so a placeholder wired to the wrong field is caught.
func TestWebhookBodyFillsEveryPlaceholder(t *testing.T) {
	a := hostileAlert()
	names := WebhookPlaceholderNames()
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = `"` + n + `": "{{` + n + `}}"`
	}
	out := renderWebhookBody("{"+strings.Join(parts, ", ")+"}", "application/json", a, "txn-1")
	var doc map[string]string
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rendered body is not JSON: %v\n%s", err, out)
	}
	want := map[string]string{
		"summary": a.Title(), "details": a.Body(), "status": "down", "event": "incident_confirmed",
		"monitor_name": a.MonitorName, "monitor_type": "http", "target": a.Target, "cause": "timeout",
		"last_error": a.LastError, "started_at": "2026-10-02T03:12:40Z", "at": "2026-10-02T03:14:10Z",
		"txn_id": "txn-1",
	}
	for _, n := range names {
		w, ok := want[n]
		if !ok {
			t.Errorf("placeholder {{%s}} has no expectation in this test", n)
			continue
		}
		if doc[n] != w {
			t.Errorf("{{%s}} = %q, want %q", n, doc[n], w)
		}
	}

	// A recovery says "up", and an alert with no outage behind it has no
	// start time rather than the year 1.
	up := renderWebhookBody(`{{status}}|{{started_at}}`, "text/plain",
		Alert{Event: string(state.EventIncidentResolved), At: a.At}, "")
	if string(up) != "up|" {
		t.Errorf("recovery renders %q, want %q", up, "up|")
	}
}

// TestWebhookBodyIsRefusedWhenItCannotWork covers the form's side: a template
// that would fail, or silently send less than was meant, is refused when it
// is saved, and the message says where.
func TestWebhookBodyIsRefusedWhenItCannotWork(t *testing.T) {
	cases := map[string]struct {
		cfg  map[string]string
		want string
	}{
		"unknown placeholder": {map[string]string{"body": `{"text": "{{monitor}}"}`},
			"unknown placeholder {{monitor}} at line 1, column 11"},
		"not JSON": {map[string]string{"body": "{\n  \"text\": \"{{summary}}\",\n}"},
			"not valid JSON at line 3"},
		"placeholder outside a string": {map[string]string{"body": `{"text": {{summary}}}`},
			"{{summary}} at line 1, column 10 outside a JSON string"},
		"cut short": {map[string]string{"body": `{"text": "{{summary}}"`},
			"ends before the document does"},
		"unclosed braces": {map[string]string{"body": `{"text": "{{summary"}`},
			"{{ at line 1, column 11 that is not closed"},
		"unknown method": {map[string]string{"method": "PATCH"}, `method must be POST or PUT, not "PATCH"`},
		"placeholder in the URL": {map[string]string{"url": "https://h.example/{{monitor_name}}"},
			"url may use only the {{txn_id}} placeholder"},
		"too long": {map[string]string{"body": `"` + strings.Repeat("x", WebhookBodyMaxLen) + `"`},
			"8192 characters or fewer"},
		// An unknown placeholder is refused whatever the body's type: it
		// would otherwise go out as the literal text {{monitor}}.
		"unknown placeholder in a form": {map[string]string{"body": "text={{monitor}}",
			"headers": "Content-Type: application/x-www-form-urlencoded"}, "unknown placeholder {{monitor}}"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := map[string]string{"url": "https://h.example/hook"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			err := NewWebhookSender(nil).Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	// Not refused: a body that is not JSON because the channel says it is
	// not, and a Matrix room address with its transaction id.
	for name, cfg := range map[string]map[string]string{
		"form body": {"body": "message={{summary}}&title=SubGlance",
			"headers": "content-type: application/x-www-form-urlencoded"},
		"plain text": {"body": "{{summary}}", "headers": "Content-Type: text/plain; charset=utf-8"},
		"matrix url": {"url": "https://m.example/_matrix/client/v3/rooms/!r:m.example/send/m.room.message/{{txn_id}}",
			"method": "put", "body": `{"msgtype": "m.text", "body": "{{summary}}"}`},
		"vendor json": {"body": `{"a": "{{summary}}"}`, "headers": "Content-Type: application/vnd.api+json"},
	} {
		t.Run(name, func(t *testing.T) {
			full := map[string]string{"url": "https://h.example/hook"}
			for k, v := range cfg {
				full[k] = v
			}
			if err := NewWebhookSender(nil).Validate(full); err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}
		})
	}
}

// TestWebhookFormBodyEscapesValues: in a form, an & or = in a monitor name
// would otherwise start a new field.
func TestWebhookFormBodyEscapesValues(t *testing.T) {
	srv, got := recordingEndpoint(t)
	cfg := map[string]string{"url": srv.URL, "body": "message={{monitor_name}}&priority=1",
		"headers": "Content-Type: application/x-www-form-urlencoded"}
	a := Alert{MonitorName: "a&b=c d", Event: string(state.EventIncidentConfirmed)}
	if err := NewWebhookSender(nil).Send(context.Background(), cfg, a); err != nil {
		t.Fatalf("send: %v", err)
	}
	reqs := got()
	form, err := url.ParseQuery(string(reqs[0].body))
	if err != nil || form.Get("message") != "a&b=c d" || form.Get("priority") != "1" {
		t.Errorf("form = %v (%v), body %q", form, err, reqs[0].body)
	}
	if reqs[0].contentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", reqs[0].contentType)
	}
}

// TestWebhookPutWithTransactionID is the Matrix case: a PUT to a URL ending
// in a transaction id that is the same on a retry of one alert, so the room
// shows it once, and different for the next alert, so that one is not
// swallowed as a repeat.
func TestWebhookPutWithTransactionID(t *testing.T) {
	srv, got := recordingEndpoint(t)
	cfg := map[string]string{
		"url":    srv.URL + "/_matrix/client/v3/rooms/!r:m.example/send/m.room.message/{{txn_id}}",
		"method": "PUT", "body": `{"msgtype": "m.text", "body": "{{summary}}", "txn": "{{txn_id}}"}`,
	}
	s := NewWebhookSender(nil)
	first := hostileAlert()
	second := first
	second.Event = string(state.EventIncidentResolved)
	for _, a := range []Alert{first, first, second} {
		if err := s.Send(context.Background(), cfg, a); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	reqs := got()
	ids := make([]string, len(reqs))
	for i, r := range reqs {
		if r.method != http.MethodPut {
			t.Errorf("request %d method = %s, want PUT", i, r.method)
		}
		ids[i] = r.path[strings.LastIndex(r.path, "/")+1:]
		if !strings.HasPrefix(ids[i], "subglance-") || strings.Contains(ids[i], "{") {
			t.Errorf("request %d path = %q: no transaction id", i, r.path)
		}
		if !strings.Contains(string(r.body), `"txn": "`+ids[i]+`"`) {
			t.Errorf("request %d body does not carry the id from its URL: %s", i, r.body)
		}
	}
	if ids[0] != ids[1] {
		t.Errorf("a retry of one alert changed its transaction id: %s then %s", ids[0], ids[1])
	}
	if ids[1] == ids[2] {
		t.Errorf("two alerts shared transaction id %s", ids[1])
	}
}

// TestWebhookWithoutABodyIsUnchanged: an existing webhook, one without the new
// settings, still posts the documented payload as JSON.
func TestWebhookWithoutABodyIsUnchanged(t *testing.T) {
	srv, got := recordingEndpoint(t)
	a := hostileAlert()
	if err := NewWebhookSender(nil).Send(context.Background(), map[string]string{"url": srv.URL, "body": "  "}, a); err != nil {
		t.Fatalf("send: %v", err)
	}
	reqs := got()
	back, err := DecodeAlert(string(reqs[0].body))
	if err != nil || back.MonitorName != a.MonitorName || back.LastError != a.LastError {
		t.Errorf("payload = %s (%v)", reqs[0].body, err)
	}
	if reqs[0].method != http.MethodPost || reqs[0].contentType != "application/json" {
		t.Errorf("request = %s %q", reqs[0].method, reqs[0].contentType)
	}

	// The method applies to the standard payload too.
	if err := NewWebhookSender(nil).Send(context.Background(), map[string]string{"url": srv.URL, "method": "put"}, a); err != nil {
		t.Fatalf("send: %v", err)
	}
	if reqs = got(); reqs[1].method != http.MethodPut {
		t.Errorf("method = %s, want PUT", reqs[1].method)
	}
}

// TestSendTestUsesTheBodyTemplate: the button that proves a channel works
// has to send what an alert will send, or it proves nothing about a Teams
// card.
func TestSendTestUsesTheBodyTemplate(t *testing.T) {
	db, _, _ := testDB(t)
	srv, got := recordingEndpoint(t)
	n := New(Options{DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Senders: map[string]Sender{store.ChannelWebhook: NewWebhookSender(nil)}})
	err := n.Test(context.Background(), store.Channel{Name: "teams", Type: store.ChannelWebhook, Enabled: true,
		Config: map[string]string{"url": srv.URL, "body": `{"text": "{{summary}}", "status": "{{status}}"}`}})
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("endpoint saw %d requests, want 1", len(reqs))
	}
	if string(reqs[0].body) != `{"text": "SubGlance test is back up", "status": "up"}` {
		t.Errorf("test message body = %s", reqs[0].body)
	}
}
