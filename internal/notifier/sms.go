package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// SMS providers. One channel type with a provider choice, rather than one
// type per provider, so that numbers, the length rule, the hourly limit and
// the wording exist once and a third provider is only a new request.
const (
	// SMSProviderAndroidGateway is SMS Gateway for Android: an Android phone
	// with a SIM card, running an app that accepts messages over HTTP on the
	// local network (or through the app's cloud relay, which speaks the same
	// API). It costs what the phone plan costs.
	SMSProviderAndroidGateway = "android-gateway"

	// SMSProviderTwilio is Twilio's Messages API.
	SMSProviderTwilio = "twilio"
)

// smsDefaultHourlyLimit is how many alerts a channel sends by SMS in any hour
// unless it says otherwise. Every Twilio message costs money, and a flapping
// monitor or a network fault across twenty monitors must not produce a bill or
// get a SIM card blocked by its carrier. Grouping already folds a wave of
// failures into one alert before this limit is consulted.
const (
	smsDefaultHourlyLimit = 10
	smsMaxHourlyLimit     = 100
	smsWindow             = time.Hour
)

// twilioAccountSID is the shape of a Twilio account SID. Checking it catches
// the usual paste error, the auth token in the SID field, with a message that
// says so instead of Twilio's bare 401.
var twilioAccountSID = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)

// twilioSenderID is a Twilio "From": a number in E.164, or an alphanumeric
// sender ID of at most eleven letters, digits and spaces with at least one
// letter. Whether a country accepts an alphanumeric sender is Twilio's call;
// its refusal is passed on as it comes.
var twilioSenderID = regexp.MustCompile(`^[A-Za-z0-9 ]{1,11}$`)

// SMSSender delivers an alert as one text message to each of a channel's
// numbers.
//
// It holds two pieces of state, both in memory: which numbers already have a
// delivery that is being retried, so a retry only goes to the numbers that
// failed, and when the channel last sent, for the hourly limit. Both reset on
// a restart. The first costs at most one duplicate message per number for a
// delivery that was half done when the process stopped; the second at most
// one extra hour's allowance.
type SMSSender struct {
	client *http.Client

	// twilioBase is Twilio's API root, overridden in tests only, like
	// TelegramSender.apiBase: pointing a real install elsewhere would be a
	// way to hand over the auth token, not a feature.
	twilioBase string

	now func() time.Time

	mu sync.Mutex
	// progress remembers, per delivery, the text that went out and the
	// numbers it reached.
	progress map[string]*smsProgress
	// windows is the recent sends and the withheld count per destination.
	windows map[string]*smsWindowState
	// reserved is, per delivery that Withhold admitted, when it took
	// its slot; Send uses that slot instead of taking another.
	reserved map[string]time.Time
}

type smsProgress struct {
	text    string
	reached map[string]bool
	started time.Time
	// slot is when the delivery's slot in the hourly limit was taken.
	slot time.Time
	// held is the count of held-back alerts that the text reports.
	held int
}

type smsWindowState struct {
	sent     []time.Time
	withheld int
}

// NewSMSSender builds an SMS channel. The guard may be nil.
//
// The Android gateway nearly always sits on a private address, so on an
// install that has not opted into private targets its deliveries are refused
// with the hint that names the setting (see httpSend and blocked).
func NewSMSSender(guard *checker.Guard) *SMSSender {
	return &SMSSender{
		client:     newHTTPClient(guard),
		twilioBase: "https://api.twilio.com",
		now:        time.Now,
		progress:   map[string]*smsProgress{},
		windows:    map[string]*smsWindowState{},
		reserved:   map[string]time.Time{},
	}
}

// ValidateSMSConfig checks an SMS channel's settings. The API calls it when a
// channel is saved, so the message lands on the form; Validate calls it again
// before every send.
func ValidateSMSConfig(cfg map[string]string) error {
	if _, err := smsRecipients(cfg); err != nil {
		return err
	}
	switch strings.TrimSpace(cfg["provider"]) {
	case SMSProviderAndroidGateway:
		if err := validateHTTPSURL(strings.TrimSpace(cfg["url"]), "url"); err != nil {
			return &configError{err.Error() + " (the address the app shows, such as http://192.168.1.50:8080)"}
		}
		if strings.TrimSpace(cfg["username"]) == "" || cfg["password"] == "" {
			return &configError{"username and password are required (the app shows both under Local Server)"}
		}
	case SMSProviderTwilio:
		if !twilioAccountSID.MatchString(strings.TrimSpace(cfg["account_sid"])) {
			return &configError{"account_sid does not look like a Twilio account SID (AC followed by 32 characters)"}
		}
		if strings.TrimSpace(cfg["auth_token"]) == "" {
			return &configError{"auth_token is required"}
		}
		from := strings.TrimSpace(cfg["from"])
		switch {
		case from == "":
			return &configError{"from is required (a Twilio number, or a sender name of up to 11 letters and digits)"}
		case strings.HasPrefix(from, "+"):
			if !e164.MatchString(from) {
				return &configError{fmt.Sprintf("from %q is not a phone number in international form", from)}
			}
		case !twilioSenderID.MatchString(from) || !strings.ContainsFunc(from, isASCIILetter):
			return &configError{"from must be a number such as +14155550100, or a sender name of up to 11 letters, digits and spaces"}
		}
	case "":
		return &configError{"provider is required: android-gateway or twilio"}
	default:
		return &configError{fmt.Sprintf("provider %q is not supported: use android-gateway or twilio", cfg["provider"])}
	}
	if _, err := smsHourlyLimit(cfg); err != nil {
		return err
	}
	switch strings.TrimSpace(cfg["recoveries"]) {
	case "", "true", "false":
	default:
		return &configError{"recoveries must be true or false"}
	}
	if tz := strings.TrimSpace(cfg["timezone"]); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
			return &configError{fmt.Sprintf("timezone %q is not an IANA time zone such as Europe/Amsterdam", tz)}
		}
	}
	return nil
}

func isASCIILetter(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }

// smsHourlyLimit reads the channel's limit, or the default.
func smsHourlyLimit(cfg map[string]string) (int, error) {
	raw := strings.TrimSpace(cfg["hourly_limit"])
	if raw == "" {
		return smsDefaultHourlyLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > smsMaxHourlyLimit {
		return 0, &configError{fmt.Sprintf("hourly_limit must be a whole number from 1 to %d", smsMaxHourlyLimit)}
	}
	return n, nil
}

// Validate checks the settings.
func (s *SMSSender) Validate(cfg map[string]string) error { return ValidateSMSConfig(cfg) }

// destination identifies where a channel's messages go, for the hourly limit.
// Two channels that send to the same numbers through the same account share
// one limit: it is the same phones, and the same bill.
func smsDestination(cfg map[string]string, numbers []string) string {
	sorted := append([]string(nil), numbers...)
	sort.Strings(sorted)
	return strings.Join([]string{
		strings.TrimSpace(cfg["provider"]),
		strings.TrimSpace(cfg["url"]),
		strings.TrimSpace(cfg["account_sid"]),
		strings.Join(sorted, ","),
	}, "|")
}

// Withhold decides, before a delivery is attempted, whether this alert is
// sent by SMS at all. A non-empty reason means no: the notifier records the
// delivery as not sent, with the reason, and does not retry it.
//
// Two things withhold an alert: a recovery on a channel set to send outages
// only, and the hourly limit. Every alert withheld by the limit is counted,
// and the next message that does go out says how many there were.
func (s *SMSSender) Withhold(cfg map[string]string, a Alert, now time.Time) string {
	// A recovery that replaces its alert is the only word of that outage
	// the channel gets, so an outages-only channel sends it.
	if strings.TrimSpace(cfg["recoveries"]) == "false" && !a.Down() && !replacesAnAlert(a) {
		return "not sent: this SMS channel sends outages only"
	}
	if ValidateSMSConfig(cfg) != nil {
		// Send will refuse it with the real reason; that is where it
		// belongs, in the delivery log as a failure. No slot is taken
		// for a message that cannot go out.
		return ""
	}
	numbers, _ := smsRecipients(cfg)
	limit, _ := smsHourlyLimit(cfg)

	dest := smsDestination(cfg, numbers)
	encoded, err := a.Encode()
	if err != nil {
		return ""
	}
	key := dest + "|" + encoded

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, retrying := s.progress[key]; retrying {
		// A retry of a delivery that already reached some numbers is
		// the same message, already counted. Withholding it now would
		// leave the numbers that failed without it.
		return ""
	}
	if _, reserved := s.reserved[key]; reserved {
		return ""
	}
	if s.takeSlot(dest, limit, now) {
		// The slot is taken here, not once the message is out, so a
		// notice or a test message sent in between cannot take it as
		// well. Send uses this reservation instead of taking its own.
		s.reserved[key] = now
		return ""
	}
	s.window(dest, now).withheld++
	return fmt.Sprintf("not sent: SMS limit of %d per hour reached; the next message says how many were held back", limit)
}

// takeSlot counts one message against the destination's hourly limit, if the
// limit allows it. The caller holds s.mu.
func (s *SMSSender) takeSlot(dest string, limit int, now time.Time) bool {
	w := s.window(dest, now)
	if len(w.sent) >= limit {
		return false
	}
	w.sent = append(w.sent, now)
	return true
}

// releaseSlot gives back the slot taken at the given time. The caller holds
// s.mu.
func (s *SMSSender) releaseSlot(dest string, at time.Time) {
	w, ok := s.windows[dest]
	if !ok {
		return
	}
	for i, t := range w.sent {
		if t.Equal(at) {
			w.sent = append(w.sent[:i], w.sent[i+1:]...)
			return
		}
	}
}

// window returns the destination's state with sends older than an hour
// dropped. The caller holds s.mu.
func (s *SMSSender) window(key string, now time.Time) *smsWindowState {
	w, ok := s.windows[key]
	if !ok {
		w = &smsWindowState{}
		s.windows[key] = w
	}
	keep := w.sent[:0]
	for _, t := range w.sent {
		if now.Sub(t) < smsWindow {
			keep = append(keep, t)
		}
	}
	w.sent = keep
	return w
}

// smsProgressTTL is how long a half-done delivery is remembered. The notifier
// gives up on a delivery after about half an hour of retries, so anything
// older is a delivery that will never be retried again.
const smsProgressTTL = 2 * time.Hour

// Send delivers the alert to every number that does not have it yet.
func (s *SMSSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	if err := s.Validate(cfg); err != nil {
		return err
	}
	numbers, _ := smsRecipients(cfg)
	limit, _ := smsHourlyLimit(cfg)
	dest := smsDestination(cfg, numbers)
	now := s.now()

	encoded, err := a.Encode()
	if err != nil {
		return err
	}
	key := dest + "|" + encoded

	s.mu.Lock()
	for k, p := range s.progress {
		if now.Sub(p.started) > smsProgressTTL {
			delete(s.progress, k)
		}
	}
	for k, at := range s.reserved {
		if now.Sub(at) >= smsWindow {
			// Withhold admitted it, but it never came to Send.
			delete(s.reserved, k)
		}
	}
	p, retry := s.progress[key]
	if !retry {
		// Every message is counted when it is admitted, whichever way
		// it comes: through Withhold, which reserved the slot, or
		// straight here, as a notice or a test message does. A retry
		// keeps the slot its first attempt took.
		slot, reserved := s.reserved[key]
		switch {
		case reserved:
			delete(s.reserved, key)
		case s.takeSlot(dest, limit, now):
			slot = now
		default:
			s.mu.Unlock()
			return fmt.Errorf("not sent: SMS limit of %d per hour reached", limit)
		}
		// The count of held-back alerts is taken when the text is first
		// rendered, and the text is kept: a retry to the numbers that
		// failed repeats the message the others already have.
		w := s.window(dest, now)
		p = &smsProgress{
			text:    smsMessage(a, strings.TrimSpace(cfg["timezone"]), w.withheld),
			reached: map[string]bool{},
			started: now,
			slot:    slot,
			held:    w.withheld,
		}
		w.withheld = 0
	}
	s.mu.Unlock()

	var (
		failures []string
		causes   []error
	)
	allPermanent := true
	for _, n := range numbers {
		if p.reached[n] {
			continue
		}
		err := s.sendOne(ctx, cfg, n, p.text)
		if err == nil {
			p.reached[n] = true
			continue
		}
		var r *Retryable
		if errors.As(err, &r) {
			allPermanent = false
		}
		failures = append(failures, maskPhone(n)+": "+scrubPhones(err.Error(), numbers))
		causes = append(causes, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(failures) == 0 {
		delete(s.progress, key)
		return nil
	}
	if len(p.reached) == 0 {
		// Nobody got this message, so it has cost nothing: the slot
		// goes back, the held-back count goes back for the next message
		// to report, and a retry is admitted by the limit afresh.
		s.releaseSlot(dest, p.slot)
		s.window(dest, now).withheld += p.held
		delete(s.progress, key)
	} else {
		s.progress[key] = p
	}

	failed := &smsDeliveryError{
		msg: fmt.Sprintf("sent to %d of %d numbers; %s",
			len(p.reached), len(numbers), strings.Join(failures, "; ")),
		causes: causes,
	}
	if allPermanent {
		return failed
	}
	// Some failures may pass on a later attempt. Numbers that were refused
	// outright are tried again with them, which costs nothing: a message
	// the provider refuses is not billed.
	return &Retryable{Err: failed}
}

// smsDeliveryError reports which numbers did not get a message. Its text has
// every number masked; the per-number causes stay reachable through
// errors.Is and errors.As, so a refused private address is still recognised
// as one.
type smsDeliveryError struct {
	msg    string
	causes []error
}

func (e *smsDeliveryError) Error() string   { return e.msg }
func (e *smsDeliveryError) Unwrap() []error { return e.causes }

// sendOne sends one message to one number through the channel's provider.
func (s *SMSSender) sendOne(ctx context.Context, cfg map[string]string, number, text string) error {
	switch strings.TrimSpace(cfg["provider"]) {
	case SMSProviderTwilio:
		return s.sendTwilio(ctx, cfg, number, text)
	default:
		return s.sendAndroidGateway(ctx, cfg, number, text)
	}
}

// sendAndroidGateway posts to the app's messages endpoint.
//
// The app answers 202 once the message is queued on the phone. Whether the
// phone then manages to send it is reported later, by polling or webhook;
// this version does not ask. "Accepted by the phone" is the delivery.
//
// The path is /messages, which the app serves from version 1.28 (January
// 2025) and the cloud relay serves at .../3rdparty/v1/messages. The older
// /message is deprecated on both.
func (s *SMSSender) sendAndroidGateway(ctx context.Context, cfg map[string]string, number, text string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg["url"]), "/") + "/messages"
	payload := map[string]any{
		"textMessage":  map[string]string{"text": text},
		"phoneNumbers": []string{number},
	}
	req, err := jsonRequest(ctx, endpoint, payload)
	if err != nil {
		return err
	}
	req.SetBasicAuth(strings.TrimSpace(cfg["username"]), cfg["password"])
	return s.do(req, "the SMS gateway", func(body []byte) string {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil {
			return e.Message
		}
		return ""
	})
}

// sendTwilio creates one Message resource.
//
// Twilio answers 201 with the message's status. "queued" or "accepted" is a
// delivery; "failed" or "undelivered" in that first answer is not, and says
// why in error_code.
func (s *SMSSender) sendTwilio(ctx context.Context, cfg map[string]string, number, text string) error {
	sid := strings.TrimSpace(cfg["account_sid"])
	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", strings.TrimRight(s.twilioBase, "/"), sid)
	form := url.Values{
		"To":   {number},
		"From": {strings.TrimSpace(cfg["from"])},
		"Body": {text},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.SetBasicAuth(sid, strings.TrimSpace(cfg["auth_token"]))

	var accepted struct {
		Status    string `json:"status"`
		ErrorCode *int   `json:"error_code"`
	}
	err = s.do(req, "Twilio", func(body []byte) string {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) != nil || e.Message == "" {
			return ""
		}
		if e.Code != 0 {
			return fmt.Sprintf("%s (Twilio error %d)", e.Message, e.Code)
		}
		return e.Message
	}, &accepted)
	if err != nil {
		return err
	}
	if accepted.Status == "failed" || accepted.Status == "undelivered" {
		if accepted.ErrorCode != nil {
			return fmt.Errorf("the message is %s at Twilio (Twilio error %d)", accepted.Status, *accepted.ErrorCode)
		}
		return fmt.Errorf("the message is %s at Twilio", accepted.Status)
	}
	return nil
}

// smsResponseLimit bounds how much of a provider's answer is read. Both
// answer with a small JSON object; anything larger is not one.
const smsResponseLimit = 16 << 10

// do performs a provider request and classifies the outcome the way httpSend
// does: a transport error, 429 and 5xx are worth another attempt, a refused
// address and any other 4xx are not. Unlike httpSend it reads the answer,
// because both providers explain a refusal in a JSON body, and that
// explanation is what the operator needs in the delivery log.
func (s *SMSSender) do(req *http.Request, who string, explain func([]byte) string, into ...any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, checker.ErrPrivateTarget) {
			return blocked(err)
		}
		return retryable("could not reach %s: %w", who, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, smsResponseLimit))

	reason := ""
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		reason = explain(body)
		if reason == "" {
			reason = readSnippet(strings.NewReader(string(body)))
		}
		if reason != "" {
			reason = ": " + reason
		}
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		for _, v := range into {
			// An unreadable success is still a success: the provider
			// said 2xx, and the status in the body is a bonus.
			_ = json.Unmarshal(body, v)
		}
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return retryable("%s is rate limiting (429)%s", who, reason)
	case resp.StatusCode >= 500:
		return retryable("%s returned %d%s", who, resp.StatusCode, reason)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s refused the credentials (401)%s", who, reason)
	default:
		return fmt.Errorf("%s refused the message (%d)%s", who, resp.StatusCode, reason)
	}
}

// smsWithheldNote is appended to the first message after alerts were held
// back by the hourly limit.
func smsWithheldNote(n int) string {
	if n == 1 {
		return " (+1 alert not sent by SMS, see SubGlance)"
	}
	return fmt.Sprintf(" (+%d alerts not sent by SMS, see SubGlance)", n)
}

// smsMessage is the text of one alert SMS, with the note about held-back
// alerts when there were any. It is always one GSM-7 part.
//
// With a base URL it also carries one link, the incident's, after the text
// and before the note. The text is shortened to make room rather than the
// message split in two, and a link that would shorten it below
// smsTextMinWithLink septets is left out: the alert is what the message is
// for, and the incident is one tap away in SubGlance.
func smsMessage(a Alert, zone string, withheld int) string {
	note := ""
	if withheld > 0 {
		note = smsWithheldNote(withheld)
	}
	room := smsSeptets - len(note)
	text := smsText(a, zone, room)
	link := smsLink(a)
	if link == "" {
		return text + note
	}
	beside := room - smsSeptetLen(link) - 1
	switch {
	case smsSeptetLen(text) <= beside:
		return text + " " + link + note
	case beside >= smsTextMinWithLink:
		return smsText(a, zone, beside) + " " + link + note
	}
	return text + note
}

// smsTextMinWithLink is the least room an alert's text keeps beside a link:
// half a message, which holds the status, a monitor name and most of a
// reason.
const smsTextMinWithLink = smsSeptets / 2

// smsLink is the one link an SMS carries, or "" when there is none or it
// cannot be written in GSM-7 as it is. A link is not transliterated like the
// text: a changed character is a link to somewhere else.
func smsLink(a Alert) string {
	link := primaryLink(a)
	for _, r := range link {
		if _, ok := septets(r); !ok {
			return ""
		}
	}
	return link
}
