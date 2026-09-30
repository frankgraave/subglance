package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// smsRequest is one request a fake provider received.
type smsRequest struct {
	method, path, contentType string
	user, pass                string
	form                      url.Values
	json                      map[string]any
}

// fakeSMSProvider stands in for Twilio or the Android gateway. answer decides
// the reply per request; nil answers with the provider's success.
type fakeSMSProvider struct {
	mu     sync.Mutex
	got    []smsRequest
	answer func(r smsRequest) (int, string)
}

func (f *fakeSMSProvider) requests() []smsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]smsRequest(nil), f.got...)
}

func (f *fakeSMSProvider) server(t *testing.T, success int, successBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := smsRequest{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type")}
		req.user, req.pass, _ = r.BasicAuth()
		if strings.HasPrefix(req.contentType, "application/json") {
			if err := json.Unmarshal(body, &req.json); err != nil {
				t.Errorf("body is not JSON: %s", body)
			}
		} else {
			req.form, _ = url.ParseQuery(string(body))
		}
		f.mu.Lock()
		f.got = append(f.got, req)
		answer := f.answer
		f.mu.Unlock()

		status, reply := success, successBody
		if answer != nil {
			if s, b := answer(req); s != 0 {
				status, reply = s, b
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// twilioTestSID has the shape of a Twilio account SID. It is assembled at
// run time so that no SID-shaped literal sits in the source for a secret
// scanner to report.
var twilioTestSID = "AC" + strings.Repeat("0123456789abcdef", 2)

func twilioSender(t *testing.T, f *fakeSMSProvider) (*SMSSender, map[string]string) {
	t.Helper()
	srv := f.server(t, http.StatusCreated, `{"sid":"SM1","status":"queued"}`)
	s := NewSMSSender(nil)
	s.twilioBase = srv.URL
	return s, map[string]string{
		"provider":    SMSProviderTwilio,
		"account_sid": twilioTestSID,
		"auth_token":  "secret-token",
		"from":        "SubGlance",
		"numbers":     "+31612345678",
	}
}

func gatewaySender(t *testing.T, f *fakeSMSProvider) (*SMSSender, map[string]string) {
	t.Helper()
	srv := f.server(t, http.StatusAccepted, `{"id":"m1","state":"Pending"}`)
	return NewSMSSender(nil), map[string]string{
		"provider": SMSProviderAndroidGateway,
		"url":      srv.URL,
		"username": "sms",
		"password": "phone-pass",
		"numbers":  "+31612345678",
		"timezone": "Europe/Amsterdam",
	}
}

// TestTwilioRequestShape checks the request Twilio's Messages API expects: a
// form-encoded POST to the account's Messages.json, with the SID and token as
// basic auth.
func TestTwilioRequestShape(t *testing.T) {
	f := &fakeSMSProvider{}
	s, cfg := twilioSender(t, f)
	if err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	r := got[0]
	if r.method != http.MethodPost || r.path != "/2010-04-01/Accounts/"+twilioTestSID+"/Messages.json" {
		t.Errorf("request = %s %s", r.method, r.path)
	}
	if r.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", r.contentType)
	}
	if r.user != twilioTestSID || r.pass != "secret-token" {
		t.Errorf("basic auth = %q:%q, want SID and token", r.user, r.pass)
	}
	if r.form.Get("To") != "+31612345678" || r.form.Get("From") != "SubGlance" {
		t.Errorf("To/From = %q/%q", r.form.Get("To"), r.form.Get("From"))
	}
	if !strings.HasPrefix(r.form.Get("Body"), "DOWN Production API: timeout after 10s") {
		t.Errorf("Body = %q", r.form.Get("Body"))
	}
}

// TestTwilioErrorIsReadable: Twilio explains a refusal with a code and a
// message, and that explanation is what belongs in the delivery log, with the
// number masked.
func TestTwilioErrorIsReadable(t *testing.T) {
	f := &fakeSMSProvider{answer: func(smsRequest) (int, string) {
		return http.StatusBadRequest,
			`{"code":21211,"message":"The 'To' number +31612345678 is not a valid phone number.","status":400}`
	}}
	s, cfg := twilioSender(t, f)
	err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed))
	if err == nil {
		t.Fatal("Send succeeded on a 400")
	}
	var r *Retryable
	if errors.As(err, &r) {
		t.Errorf("a 400 is retryable: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"Twilio error 21211", "is not a valid phone number", "+31 6 •••• 5678"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	if strings.Contains(msg, "12345678") {
		t.Errorf("error %q contains the full number", msg)
	}
}

func TestTwilioFailedStatusIsAFailure(t *testing.T) {
	f := &fakeSMSProvider{answer: func(smsRequest) (int, string) {
		return http.StatusCreated, `{"sid":"SM1","status":"failed","error_code":30008}`
	}}
	s, cfg := twilioSender(t, f)
	err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed))
	if err == nil || !strings.Contains(err.Error(), "30008") {
		t.Fatalf("Send = %v, want the failed status with its code", err)
	}
}

// TestAndroidGatewayRequestShape checks the request the app's local server
// accepts: JSON with textMessage.text and phoneNumbers, basic auth, POST to
// /messages.
func TestAndroidGatewayRequestShape(t *testing.T) {
	f := &fakeSMSProvider{}
	s, cfg := gatewaySender(t, f)
	cfg["url"] += "/" // a trailing slash is not doubled
	if err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	r := got[0]
	if r.method != http.MethodPost || r.path != "/messages" {
		t.Errorf("request = %s %s, want POST /messages", r.method, r.path)
	}
	if r.user != "sms" || r.pass != "phone-pass" {
		t.Errorf("basic auth = %q:%q", r.user, r.pass)
	}
	text, _ := r.json["textMessage"].(map[string]any)
	if text["text"] != "DOWN Production API: timeout after 10s (since 14:03)" {
		t.Errorf("textMessage.text = %v", text["text"])
	}
	phones, _ := r.json["phoneNumbers"].([]any)
	if len(phones) != 1 || phones[0] != "+31612345678" {
		t.Errorf("phoneNumbers = %v", r.json["phoneNumbers"])
	}
}

func TestAndroidGatewayErrors(t *testing.T) {
	tests := map[string]struct {
		status    int
		body      string
		retryable bool
		want      string
	}{
		"bad credentials": {http.StatusUnauthorized, ``, false, "refused the credentials (401)"},
		"bad request":     {http.StatusBadRequest, `{"message":"invalid phone number"}`, false, "invalid phone number"},
		"phone trouble":   {http.StatusInternalServerError, `{"message":"no SIM"}`, true, "no SIM"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakeSMSProvider{answer: func(smsRequest) (int, string) { return tc.status, tc.body }}
			s, cfg := gatewaySender(t, f)
			err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed))
			if err == nil {
				t.Fatal("Send succeeded")
			}
			var r *Retryable
			if errors.As(err, &r) != tc.retryable {
				t.Errorf("retryable = %v, want %v: %v", !tc.retryable, tc.retryable, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestAndroidGatewayOnAPrivateAddressNamesTheSetting: the phone is on the LAN
// nearly always, and a refusal has to say which setting lets it through.
func TestAndroidGatewayOnAPrivateAddressNamesTheSetting(t *testing.T) {
	f := &fakeSMSProvider{}
	_, cfg := gatewaySender(t, f)
	s := NewSMSSender(checker.NewGuard(false))
	err := s.Send(context.Background(), cfg, smsAlert(state.EventIncidentConfirmed))
	if err == nil {
		t.Fatal("Send to a loopback gateway succeeded without --allow-private-targets")
	}
	if !errors.Is(err, checker.ErrPrivateTarget) || !strings.Contains(err.Error(), "--allow-private-targets") {
		t.Errorf("error = %v, want the private-target refusal with its hint", err)
	}
	var r *Retryable
	if errors.As(err, &r) {
		t.Errorf("a refused address is retried: %v", err)
	}
	if len(f.requests()) != 0 {
		t.Error("the guard let the request through")
	}
}

// TestSMSRetryGoesOnlyToNumbersThatFailed: with two numbers and one failing,
// the retry must not send the first number the same alert again.
func TestSMSRetryGoesOnlyToNumbersThatFailed(t *testing.T) {
	var failSecond = true
	var mu sync.Mutex
	f := &fakeSMSProvider{answer: func(r smsRequest) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if r.form.Get("To") == "+31687654321" && failSecond {
			return http.StatusServiceUnavailable, `{"code":20500,"message":"Internal Server Error"}`
		}
		return 0, ""
	}}
	s, cfg := twilioSender(t, f)
	cfg["numbers"] = "+31612345678, +31687654321"
	a := smsAlert(state.EventIncidentConfirmed)

	err := s.Send(context.Background(), cfg, a)
	var r *Retryable
	if err == nil || !errors.As(err, &r) {
		t.Fatalf("first attempt = %v, want a retryable failure", err)
	}
	if !strings.Contains(err.Error(), "sent to 1 of 2 numbers") || !strings.Contains(err.Error(), "+31 6 •••• 4321") {
		t.Errorf("error = %q, want the count and the masked failing number", err)
	}

	mu.Lock()
	failSecond = false
	mu.Unlock()
	if err := s.Send(context.Background(), cfg, a); err != nil {
		t.Fatalf("retry: %v", err)
	}

	count := map[string]int{}
	for _, req := range f.requests() {
		count[req.form.Get("To")]++
	}
	if count["+31612345678"] != 1 || count["+31687654321"] != 2 {
		t.Errorf("messages per number = %v, want the first once and the second twice", count)
	}

	// A new alert goes to both again.
	b := smsAlert(state.EventIncidentResolved)
	if err := s.Send(context.Background(), cfg, b); err != nil {
		t.Fatalf("next alert: %v", err)
	}
	if n := len(f.requests()); n != 5 {
		t.Errorf("requests = %d, want 5", n)
	}
}

// TestSMSHourlyLimit: the eleventh alert in an hour is not sent, and the first
// one after the hour says how many were held back, exactly once.
func TestSMSHourlyLimit(t *testing.T) {
	f := &fakeSMSProvider{}
	s, cfg := twilioSender(t, f)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	send := func(i int) (withheld string) {
		a := smsAlert(state.EventIncidentConfirmed)
		a.IncidentID = int64(i) // distinct alerts
		if reason := s.Withhold(cfg, a, now); reason != "" {
			return reason
		}
		if err := s.Send(context.Background(), cfg, a); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		return ""
	}

	for i := 1; i <= smsDefaultHourlyLimit; i++ {
		if reason := send(i); reason != "" {
			t.Fatalf("alert %d withheld under the limit: %s", i, reason)
		}
		now = now.Add(time.Minute)
	}
	for i := 11; i <= 13; i++ {
		if reason := send(i); !strings.Contains(reason, "limit of 10 per hour") {
			t.Fatalf("alert %d: withhold reason = %q, want the limit", i, reason)
		}
	}
	if n := len(f.requests()); n != smsDefaultHourlyLimit {
		t.Fatalf("messages sent = %d, want %d", n, smsDefaultHourlyLimit)
	}

	// The first send left the window an hour after it happened.
	now = time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	if reason := send(14); reason != "" {
		t.Fatalf("alert after the hour withheld: %s", reason)
	}
	got := f.requests()
	last := got[len(got)-1].form.Get("Body")
	if !strings.HasSuffix(last, "(+3 alerts not sent by SMS, see SubGlance)") {
		t.Errorf("first message after the limit = %q, want the held-back count", last)
	}
	if n := smsSeptetLen(last); n > smsSeptets {
		t.Errorf("message with the note is %d septets", n)
	}

	now = now.Add(time.Minute)
	if reason := send(15); reason != "" {
		t.Fatalf("second alert after the hour withheld: %s", reason)
	}
	got = f.requests()
	if last := got[len(got)-1].form.Get("Body"); strings.Contains(last, "not sent by SMS") {
		t.Errorf("the held-back count was repeated: %q", last)
	}
}

func TestSMSHourlyLimitIsPerChannelSetting(t *testing.T) {
	f := &fakeSMSProvider{}
	s, cfg := twilioSender(t, f)
	cfg["hourly_limit"] = "1"
	now := time.Now()
	a := smsAlert(state.EventIncidentConfirmed)
	if s.Withhold(cfg, a, now) != "" {
		t.Fatal("first alert withheld")
	}
	if err := s.Send(context.Background(), cfg, a); err != nil {
		t.Fatal(err)
	}
	b := smsAlert(state.EventIncidentResolved)
	if s.Withhold(cfg, b, now) == "" {
		t.Error("second alert in the hour sent with hourly_limit 1")
	}
}

func TestSMSOutagesOnly(t *testing.T) {
	s := NewSMSSender(nil)
	cfg := map[string]string{"recoveries": "false", "numbers": "+31612345678"}
	now := time.Now()
	if reason := s.Withhold(cfg, smsAlert(state.EventIncidentResolved), now); !strings.Contains(reason, "outages only") {
		t.Errorf("recovery on an outages-only channel: reason = %q", reason)
	}
	if reason := s.Withhold(cfg, smsAlert(state.EventIncidentConfirmed), now); reason != "" {
		t.Errorf("outage on an outages-only channel withheld: %q", reason)
	}
	cfg["recoveries"] = ""
	if reason := s.Withhold(cfg, smsAlert(state.EventIncidentResolved), now); reason != "" {
		t.Errorf("recovery withheld by default: %q", reason)
	}
}

func TestValidateSMSConfig(t *testing.T) {
	gateway := func(mut func(map[string]string)) map[string]string {
		cfg := map[string]string{"provider": "android-gateway", "url": "http://192.168.1.50:8080",
			"username": "sms", "password": "p", "numbers": "+31612345678"}
		mut(cfg)
		return cfg
	}
	twilio := func(mut func(map[string]string)) map[string]string {
		cfg := map[string]string{"provider": "twilio", "account_sid": twilioTestSID,
			"auth_token": "t", "from": "+14155550100", "numbers": "+31612345678"}
		mut(cfg)
		return cfg
	}
	ok := []map[string]string{
		gateway(func(map[string]string) {}),
		twilio(func(map[string]string) {}),
		twilio(func(c map[string]string) { c["from"] = "SubGlance" }),
		twilio(func(c map[string]string) { c["hourly_limit"] = "100"; c["recoveries"] = "false" }),
		gateway(func(c map[string]string) { c["timezone"] = "Europe/Amsterdam" }),
	}
	for _, cfg := range ok {
		if err := ValidateSMSConfig(cfg); err != nil {
			t.Errorf("ValidateSMSConfig(%v) = %v", cfg, err)
		}
	}
	bad := map[string]map[string]string{
		"no provider":       gateway(func(c map[string]string) { delete(c, "provider") }),
		"unknown provider":  gateway(func(c map[string]string) { c["provider"] = "bird" }),
		"gateway no url":    gateway(func(c map[string]string) { delete(c, "url") }),
		"gateway ftp url":   gateway(func(c map[string]string) { c["url"] = "ftp://phone" }),
		"gateway no pass":   gateway(func(c map[string]string) { delete(c, "password") }),
		"token in SID":      twilio(func(c map[string]string) { c["account_sid"] = "0123456789abcdef" }),
		"no token":          twilio(func(c map[string]string) { c["auth_token"] = "" }),
		"no from":           twilio(func(c map[string]string) { c["from"] = "" }),
		"long sender name":  twilio(func(c map[string]string) { c["from"] = "SubGlanceAlerts" }),
		"digits-only name":  twilio(func(c map[string]string) { c["from"] = "12345" }),
		"limit zero":        twilio(func(c map[string]string) { c["hourly_limit"] = "0" }),
		"limit too high":    twilio(func(c map[string]string) { c["hourly_limit"] = "101" }),
		"recoveries yes":    twilio(func(c map[string]string) { c["recoveries"] = "yes" }),
		"bad zone":          gateway(func(c map[string]string) { c["timezone"] = "Mars/Olympus" }),
		"no numbers":        gateway(func(c map[string]string) { c["numbers"] = "" }),
		"national, no code": gateway(func(c map[string]string) { c["numbers"] = "0612345678" }),
	}
	for name, cfg := range bad {
		if err := ValidateSMSConfig(cfg); err == nil {
			t.Errorf("%s: accepted %v", name, cfg)
		}
	}
}

// TestNotifierRecordsAWithheldSMS runs the limit through the real outbox: an
// alert over the limit is not sent, not retried, not counted as pending, and
// its row says why.
func TestNotifierRecordsAWithheldSMS(t *testing.T) {
	db, m, _ := testDB(t)
	ctx := context.Background()

	f := &fakeSMSProvider{}
	sms, cfg := twilioSender(t, f)
	cfg["hourly_limit"] = "1"
	ch, err := db.CreateChannel(ctx, store.Channel{Name: "on call", Type: store.ChannelSMS, Config: cfg, Enabled: true})
	if err != nil {
		t.Fatalf("create sms channel: %v", err)
	}
	if err := db.SetMonitorChannels(ctx, m.ID, []int64{ch.ID}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	n := New(Options{
		DB: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
		GroupWindow: GroupingDisabled,
		Senders:     map[string]Sender{store.ChannelSMS: sms},
	})
	inc := openIncident(t, db, m.ID, now, "timeout")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, now); err != nil {
		t.Fatal(err)
	}
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentReminder, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := n.sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(f.requests()); got != 1 {
		t.Fatalf("messages sent = %d, want 1 (limit 1)", got)
	}
	due, err := db.DueDeliveries(ctx, now.Add(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("%d deliveries still due; a withheld alert must not be retried", len(due))
	}
	health, err := db.ChannelHealthSince(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if h := health[ch.ID]; h.Pending != 0 || h.Failed != 0 {
		t.Errorf("channel health = %+v, want nothing pending or failed", h)
	}
	var reason string
	if err := db.Reader.QueryRowContext(ctx,
		`SELECT last_error FROM notif_outbox WHERE channel_id = ? AND suppressed = 1`, ch.ID).Scan(&reason); err != nil {
		t.Fatalf("no suppressed row: %v", err)
	}
	if !strings.Contains(reason, "limit of 1 per hour") {
		t.Errorf("reason = %q", reason)
	}
}
