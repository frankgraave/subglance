package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// A channel's delivery record is what the notifications screen draws its
// Delivery column from (SUB-180). The two things that matter: a channel whose
// alerts are not arriving never reads as healthy, and the record never hands
// a viewer the credential the config mask keeps from them.

func TestDeliveryStateNeverCallsAFailingChannelHealthy(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		h    store.ChannelHealth
		want string
	}{
		{"nothing in the window", store.ChannelHealth{}, deliveryNone},
		{"delivered", store.ChannelHealth{LastDeliveredAt: now}, deliveryDelivered},
		{"failed, nothing since", store.ChannelHealth{Failed: 1, LastFailedAt: now}, deliveryFailed},
		{"failed after a success", store.ChannelHealth{Failed: 1, LastFailedAt: now, LastDeliveredAt: now.Add(-time.Hour)}, deliveryFailed},
		{"failed in the same second as a success", store.ChannelHealth{Failed: 1, LastFailedAt: now, LastDeliveredAt: now}, deliveryFailed},
		{"recovered after a failure", store.ChannelHealth{Failed: 1, LastFailedAt: now.Add(-time.Hour), LastDeliveredAt: now}, deliveryDelivered},
		{"retrying after a success", store.ChannelHealth{Pending: 3, Retrying: 1, LastDeliveredAt: now}, deliveryRetrying},
		{"retrying with nothing finished", store.ChannelHealth{Pending: 1, Retrying: 1}, deliveryRetrying},
		{"queued, not yet tried", store.ChannelHealth{Pending: 2}, deliveryNone},
		{"queued after a success", store.ChannelHealth{Pending: 2, LastDeliveredAt: now}, deliveryDelivered},
	}
	for _, c := range cases {
		if got := deliveryState(c.h); got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRedactDeliveryErrorTakesOutTheCredential(t *testing.T) {
	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	ch := store.Channel{Type: store.ChannelSlack, Config: map[string]string{"url": hook}}

	// What Go's HTTP client writes, verbatim, when the host is unreachable.
	msg := `gave up after 5 attempts: Post "` + hook + `": dial tcp: lookup hooks.slack.com: no such host`
	got := redactDeliveryError(msg, ch)
	if strings.Contains(got, "verysecret") || strings.Contains(got, "/services/") {
		t.Fatalf("the credential survived redaction: %q", got)
	}
	// The part that diagnoses the failure stays. The configured URL goes as
	// a whole, host included, because the config mask hides it as a whole:
	// a Gotify or ntfy server's address is masked there too.
	for _, keep := range []string{"****cret", "no such host", "gave up after 5 attempts"} {
		if !strings.Contains(got, keep) {
			t.Errorf("redaction removed %q: %q", keep, got)
		}
	}

	// A token that is not in a URL is replaced by its own mask.
	tg := store.Channel{Type: store.ChannelTelegram, Config: map[string]string{
		"bot_token": "123456:ABCDEFsecret", "chat_id": "-100123",
	}}
	got = redactDeliveryError("bot 123456:ABCDEFsecret was rejected for chat -100123", tg)
	if strings.Contains(got, "ABCDEFsecret") {
		t.Errorf("a bare token survived: %q", got)
	}
	if !strings.Contains(got, "-100123") {
		t.Errorf("a public value was masked: %q", got)
	}

	// A URL that is not the stored value, such as one a sender built from
	// it, keeps its host and loses its path.
	got = redactDeliveryError(`Post "https://api.telegram.org/bot42:other/sendMessage": EOF`, store.Channel{})
	if strings.Contains(got, "bot42") || !strings.Contains(got, "https://api.telegram.org/…") {
		t.Errorf("a built URL was not trimmed to its host: %q", got)
	}

	// Userinfo in a URL is a credential too, even with no path after it.
	got = redactDeliveryError("Post https://user:pw@gotify.example: refused", store.Channel{})
	if strings.Contains(got, "pw@") || strings.Contains(got, "user:") {
		t.Errorf("userinfo survived: %q", got)
	}
}

func TestChannelListCarriesTheDeliveryRecord(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()

	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	ch := createSlackChannel(t, srv, "ops", hook)
	quiet := createSlackChannel(t, srv, "quiet", "https://hooks.slack.com/services/T000/B000/othersecret")
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "api", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if _, err := db.SeedDelivery(ctx, store.Delivery{
		ChannelID: ch.ID, MonitorID: m.ID, Event: "incident_confirmed", Payload: "{}",
		Status: store.OutboxFailed, Attempts: 5,
		LastError: `gave up after 5 attempts: Post "` + hook + `": dial tcp: lookup hooks.slack.com: no such host`,
		CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("SeedDelivery: %v", err)
	}

	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "verysecret") {
		t.Fatalf("a viewer read the webhook credential through the delivery record: %s", rec.Body.String())
	}

	var body struct {
		Channels []channelResponse `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[int64]channelResponse{}
	for _, c := range body.Channels {
		byID[c.ID] = c
	}

	d := byID[ch.ID].Delivery
	if d == nil {
		t.Fatal("the failing channel has no delivery record")
	}
	if d.State != deliveryFailed || d.Failed != 1 || d.LastFailedAt == nil || !d.LastFailedAt.Equal(at) {
		t.Errorf("delivery = %+v, want one failure at %v", d, at)
	}
	if !strings.Contains(d.LastError, "no such host") {
		t.Errorf("last_error = %q, want the cause kept", d.LastError)
	}
	if d.WindowDays != 30 {
		t.Errorf("window_days = %d, want 30", d.WindowDays)
	}

	if q := byID[quiet.ID].Delivery; q == nil || q.State != deliveryNone || q.LastDeliveredAt != nil {
		t.Errorf("quiet channel delivery = %+v, want state none and no moments", q)
	}
}

func TestChannelResponsesAlwaysCarryTheRecord(t *testing.T) {
	// Create, read and update all return a record. A response without one
	// would make the screen redraw the row as "not verified" after an edit,
	// as if the channel's history had gone.
	srv, _ := testServerWithDB(t)
	created := createSlackChannel(t, srv, "ops", "https://hooks.slack.com/services/T/B/x")
	if created.Delivery == nil || created.Delivery.State != deliveryNone {
		t.Fatalf("create response delivery = %+v, want state none", created.Delivery)
	}

	path := "/api/v1/channels/" + jsonID(created.ID)
	for _, req := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"name":"ops2","type":"slack","config":{"url":"https://hooks.slack.com/services/T/B/y"}}`},
	} {
		rec := doJSON(t, srv, req.method, path, req.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", req.method, rec.Code, rec.Body.String())
		}
		var got channelResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Delivery == nil {
			t.Errorf("%s response has no delivery record", req.method)
		}
	}
}

func jsonID(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
