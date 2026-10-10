package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// An incident's delivery list answers "did anyone hear about this?" (SUB-217).
// The dangerous mistakes: a delivery that did not arrive reading as one that
// did, a merged delivery reading as lost, and a viewer being handed a
// channel's credential through an error message.

func TestIncidentDeliveryStateNeverCallsAnUnsentAlertDelivered(t *testing.T) {
	cases := []struct {
		name string
		d    store.Delivery
		want string
	}{
		{"delivered", store.Delivery{Status: store.OutboxDelivered, Attempts: 1}, incidentDeliveryDelivered},
		{"failed", store.Delivery{Status: store.OutboxFailed, Attempts: 5}, incidentDeliveryFailed},
		{"failed after maintenance closed it", store.Delivery{Status: store.OutboxFailed, Suppressed: true}, incidentDeliveryFailed},
		{"queued", store.Delivery{Status: store.OutboxPending}, incidentDeliveryQueued},
		{"retrying", store.Delivery{Status: store.OutboxPending, Attempts: 2}, incidentDeliveryRetrying},
		{"held for quiet hours", store.Delivery{Status: store.OutboxPending, QuietHeld: true}, incidentDeliveryHeld},
		{"dropped in quiet hours", store.Delivery{Status: store.OutboxPending, Suppressed: true, LastError: "dropped during quiet hours"}, incidentDeliveryNotSent},
		{"folded into a digest", store.Delivery{Status: store.OutboxPending, Suppressed: true, LastError: "sent in quiet-hours digest 12"}, incidentDeliveryMerged},
		{"replaced by its recovery", store.Delivery{Status: store.OutboxPending, Suppressed: true, LastError: "sent as part of recovery 9: the monitor was back up before this alert went out"}, incidentDeliveryMerged},
	}
	for _, c := range cases {
		if got := incidentDeliveryState(c.d); got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
	}
}

type incidentDeliveriesFixture struct {
	srv     *Server
	db      *store.DB
	monitor int64
	slack   channelResponse
	other   channelResponse
}

func newIncidentDeliveriesFixture(t *testing.T) incidentDeliveriesFixture {
	t.Helper()
	srv, db := testServerWithDB(t)
	m, err := db.CreateMonitor(t.Context(), store.Monitor{Name: "api", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return incidentDeliveriesFixture{
		srv: srv, db: db, monitor: m.ID,
		slack: createSlackChannel(t, srv, "ops", "https://hooks.slack.com/services/T000/B000/verysecret"),
		other: createSlackChannel(t, srv, "team", "https://hooks.slack.com/services/T000/B000/othersecret"),
	}
}

// incident seeds an incident; resolved is its resolution, zero for one that
// is still open.
func (f incidentDeliveriesFixture) incident(t *testing.T, started, resolved time.Time) store.Incident {
	t.Helper()
	inc, err := f.db.SeedIncident(t.Context(), store.Incident{
		MonitorID: f.monitor, StartedAt: started, ConfirmedAt: started.Add(time.Minute), ResolvedAt: resolved,
	})
	if err != nil {
		t.Fatalf("SeedIncident: %v", err)
	}
	return inc
}

func (f incidentDeliveriesFixture) seed(t *testing.T, d store.Delivery) store.Delivery {
	t.Helper()
	d.MonitorID = f.monitor
	if d.Payload == "" {
		d.Payload = fmt.Sprintf(`{"incident_id":%d}`, d.IncidentID)
	}
	got, err := f.db.SeedDelivery(t.Context(), d)
	if err != nil {
		t.Fatalf("SeedDelivery: %v", err)
	}
	return got
}

func getIncidentDeliveries(t *testing.T, srv *Server, token string, id int64) (int, incidentDeliveriesResponse, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/incidents/%d/deliveries", id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body incidentDeliveriesResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, body, rec.Body.String()
}

func TestIncidentDeliveriesListsEveryChannelAndWhatBecameOfIt(t *testing.T) {
	f := newIncidentDeliveriesFixture(t)
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	// Another incident's alert on the same channel is not this one's.
	other := f.incident(t, start.Add(-time.Hour), start.Add(-30*time.Minute))
	inc := f.incident(t, start, time.Time{})
	at := start.Add(2 * time.Minute)

	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	delivered := f.seed(t, store.Delivery{ChannelID: f.other.ID, IncidentID: inc.ID, Event: "incident_confirmed",
		Status: store.OutboxDelivered, Attempts: 1, CreatedAt: at, UpdatedAt: at.Add(time.Second)})
	failed := f.seed(t, store.Delivery{ChannelID: f.slack.ID, IncidentID: inc.ID, Event: "incident_confirmed",
		Status: store.OutboxFailed, Attempts: 5, CreatedAt: at, UpdatedAt: at.Add(10 * time.Minute),
		LastError: `gave up after 5 attempts: Post "` + hook + `": dial tcp: lookup hooks.slack.com: no such host`})
	f.seed(t, store.Delivery{ChannelID: f.slack.ID, IncidentID: other.ID, Event: "incident_confirmed",
		Status: store.OutboxDelivered, CreatedAt: at, UpdatedAt: at})

	viewer := seedUser(t, f.srv, f.db, "viewer@example.com", store.RoleViewer)
	code, body, raw := getIncidentDeliveries(t, f.srv, viewer, inc.ID)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, raw)
	}
	if strings.Contains(raw, "verysecret") {
		t.Fatalf("a viewer read the webhook credential through an incident's delivery list: %s", raw)
	}
	if body.IncidentID != inc.ID || body.WindowDays != 30 || !body.Complete {
		t.Errorf("header = %+v, want incident %d, a 30-day window, complete", body, inc.ID)
	}
	if len(body.Deliveries) != 2 {
		t.Fatalf("deliveries = %+v, want the two about this incident", body.Deliveries)
	}
	byID := map[int64]incidentDelivery{}
	for _, d := range body.Deliveries {
		byID[d.ID] = d
	}
	if d := byID[delivered.ID]; d.State != incidentDeliveryDelivered || d.ChannelName != "team" || d.ChannelType != "slack" ||
		d.EndedAt == nil || d.Error != "" {
		t.Errorf("delivered row = %+v", d)
	}
	d := byID[failed.ID]
	if d.State != incidentDeliveryFailed || d.Attempts != 5 || d.EndedAt == nil {
		t.Errorf("failed row = %+v", d)
	}
	if !strings.Contains(d.Error, "no such host") || !strings.Contains(d.Error, "gave up after 5 attempts") {
		t.Errorf("failed row error = %q, want the cause kept", d.Error)
	}
}

func TestIncidentDeliveriesNamesTheDigestThatCarriedAMergedAlert(t *testing.T) {
	f := newIncidentDeliveriesFixture(t)
	start := time.Now().Add(-8 * time.Hour).Truncate(time.Second)
	// The night's first alert, about an earlier incident, carries the
	// digest. It was queued before this incident began.
	earlier := f.incident(t, start.Add(-time.Hour), start.Add(-time.Minute))
	inc := f.incident(t, start, time.Time{})
	carrier := f.seed(t, store.Delivery{ChannelID: f.slack.ID, IncidentID: earlier.ID, Event: "incident_confirmed",
		CreatedAt: start.Add(-50 * time.Minute)})
	mine := f.seed(t, store.Delivery{ChannelID: f.slack.ID, IncidentID: inc.ID, Event: "incident_confirmed",
		CreatedAt: start.Add(2 * time.Minute)})
	if err := f.db.FoldDigest(t.Context(), carrier.ID, "quiet_hours_digest",
		fmt.Sprintf(`{"members":[{"incident_id":%d},{"incident_id":%d}]}`, earlier.ID, inc.ID),
		[]int64{mine.ID}, time.Now()); err != nil {
		t.Fatalf("FoldDigest: %v", err)
	}
	if err := f.db.MarkDelivered(t.Context(), carrier.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	code, body, raw := getIncidentDeliveries(t, f.srv, testCredentials[f.srv], inc.ID)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, raw)
	}
	if len(body.Deliveries) != 2 {
		t.Fatalf("deliveries = %+v, want the merged alert and the digest that carried it", body.Deliveries)
	}
	digest, merged := body.Deliveries[0], body.Deliveries[1]
	if digest.ID != carrier.ID || digest.State != incidentDeliveryDelivered || digest.Event != "quiet_hours_digest" {
		t.Errorf("first row = %+v, want the delivered digest %d", digest, carrier.ID)
	}
	if merged.ID != mine.ID || merged.State != incidentDeliveryMerged || merged.MergedInto != store.MergedIntoDigest ||
		merged.CarriedBy == nil || *merged.CarriedBy != carrier.ID || merged.Reason != "" {
		t.Errorf("merged row = %+v, want merged into the digest %d", merged, carrier.ID)
	}
}

func TestIncidentDeliveriesSaysWhyAnAlertWasNotSent(t *testing.T) {
	f := newIncidentDeliveriesFixture(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	inc := f.incident(t, start, time.Time{})
	dropped := f.seed(t, store.Delivery{ChannelID: f.slack.ID, IncidentID: inc.ID, Event: "incident_confirmed", CreatedAt: start})
	if err := f.db.DropDelivery(t.Context(), dropped.ID); err != nil {
		t.Fatalf("DropDelivery: %v", err)
	}
	held := f.seed(t, store.Delivery{ChannelID: f.other.ID, IncidentID: inc.ID, Event: "incident_confirmed", CreatedAt: start})
	if err := f.db.HoldDelivery(t.Context(), held.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("HoldDelivery: %v", err)
	}

	_, body, raw := getIncidentDeliveries(t, f.srv, testCredentials[f.srv], inc.ID)
	if len(body.Deliveries) != 2 {
		t.Fatalf("deliveries = %s", raw)
	}
	if d := body.Deliveries[0]; d.State != incidentDeliveryNotSent || d.Reason != "dropped during quiet hours" || d.EndedAt == nil {
		t.Errorf("dropped row = %+v", d)
	}
	if d := body.Deliveries[1]; d.State != incidentDeliveryHeld || d.EndedAt != nil || d.Reason != "" || d.Error != "" {
		t.Errorf("held row = %+v, want held, still open, with no reason or error", d)
	}
}

func TestIncidentDeliveriesSaysWhenTheListMayBeShort(t *testing.T) {
	f := newIncidentDeliveriesFixture(t)
	inc := f.incident(t, time.Now().Add(-store.DeliveryLogRetention-time.Hour), time.Time{})
	code, body, raw := getIncidentDeliveries(t, f.srv, testCredentials[f.srv], inc.ID)
	if code != http.StatusOK || body.Complete || body.Deliveries == nil {
		t.Fatalf("status %d, body %s: want 200, complete false, and an empty list rather than null", code, raw)
	}
}

func TestIncidentDeliveriesRefusesABadOrUnknownIncident(t *testing.T) {
	f := newIncidentDeliveriesFixture(t)
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/v1/incidents/0/deliveries", http.StatusBadRequest},
		{"/api/v1/incidents/x/deliveries", http.StatusBadRequest},
		{"/api/v1/incidents/987654/deliveries", http.StatusNotFound},
	} {
		if rec := doJSON(t, f.srv, http.MethodGet, c.path, ""); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d: %s", c.path, rec.Code, c.want, rec.Body.String())
		}
	}
}
