package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Notification channels hold the credentials that let SubGlance post to Slack,
// Discord and the like. The masking behaviour below is the reason these tests
// exist: a leak there hands anyone with read access the ability to post into
// the user's chat.

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, r)
	return rec
}

func createSlackChannel(t *testing.T, srv *Server, name, url string) channelResponse {
	t.Helper()

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		`{"name":"`+name+`","type":"slack","config":{"url":"`+url+`"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestCreateChannelRoundTrip(t *testing.T) {
	srv, db := testServerWithDB(t)

	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	got := createSlackChannel(t, srv, "ops", hook)

	if got.ID == 0 {
		t.Fatal("response carries no ID")
	}
	if !got.Enabled {
		t.Error("a new channel should be enabled by default")
	}

	// The secret must not come back in full, not even to its creator.
	if got.Config["url"] == hook {
		t.Fatal("the webhook URL was echoed back verbatim")
	}
	if !strings.HasSuffix(got.Config["url"], "cret") {
		t.Errorf("masked url = %q, want the last four characters preserved", got.Config["url"])
	}

	// But the real value did land in the database, or the channel is useless.
	stored, err := db.GetChannel(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if stored.Config["url"] != hook {
		t.Errorf("stored url = %q, want the unmasked value", stored.Config["url"])
	}
}

func TestListChannelsMasksSecrets(t *testing.T) {
	srv, _ := testServerWithDB(t)

	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	createSlackChannel(t, srv, "ops", hook)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/channels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "verysecret") {
		t.Fatalf("the listing leaked the webhook secret: %s", rec.Body.String())
	}
}

func TestUpdateChannelKeepsSecretWhenMaskedValueIsSentBack(t *testing.T) {
	srv, db := testServerWithDB(t)

	const hook = "https://hooks.slack.com/services/T000/B000/verysecret"
	created := createSlackChannel(t, srv, "ops", hook)

	// A UI reads the channel, the user renames it, and the form posts back
	// exactly what it was given — including the masked URL.
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		`{"name":"ops-oncall","type":"slack","config":{"url":"`+created.Config["url"]+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	stored, err := db.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if stored.Name != "ops-oncall" {
		t.Errorf("name = %q, want ops-oncall", stored.Name)
	}
	if stored.Config["url"] != hook {
		t.Fatalf("url = %q, want the original secret to survive a round trip", stored.Config["url"])
	}
}

func TestUpdateChannelStoresANewSecret(t *testing.T) {
	srv, db := testServerWithDB(t)

	created := createSlackChannel(t, srv, "ops",
		"https://hooks.slack.com/services/T000/B000/verysecret")

	const replacement = "https://hooks.slack.com/services/T111/B111/fresh"
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+strconv.FormatInt(created.ID, 10),
		`{"name":"ops","type":"slack","config":{"url":"`+replacement+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	stored, err := db.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if stored.Config["url"] != replacement {
		t.Errorf("url = %q, want the new value %q", stored.Config["url"], replacement)
	}
}

func TestCreateChannelRejectsBadInput(t *testing.T) {
	srv, _ := testServerWithDB(t)

	cases := []struct {
		name string
		body string
	}{
		{"no name", `{"name":"","type":"slack","config":{"url":"https://example.com/h"}}`},
		{"unknown type", `{"name":"x","type":"carrier-pigeon","config":{"url":"https://example.com/h"}}`},
		{"missing required config", `{"name":"x","type":"slack","config":{}}`},
		{"non-http scheme", `{"name":"x","type":"webhook","config":{"url":"file:///etc/passwd"}}`},
		{"url without host", `{"name":"x","type":"webhook","config":{"url":"https://"}}`},
		{"telegram without token", `{"name":"x","type":"telegram","config":{"chat_id":"1"}}`},
		{"unknown field", `{"name":"x","type":"slack","config":{"url":"https://example.com/h"},"nope":1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDeleteChannelIsIdempotentlyHonest(t *testing.T) {
	srv, _ := testServerWithDB(t)

	created := createSlackChannel(t, srv, "ops", "https://example.com/hook")
	path := "/api/v1/channels/" + strconv.FormatInt(created.ID, 10)

	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("first delete: status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	// Deleting something that is already gone is a 404, not a silent 204:
	// a script that reports success for a channel it never removed is lying.
	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestSetMonitorChannelsReplacesTheSet(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")

	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/channels"

	rec := doJSON(t, srv, http.MethodPut, path,
		`{"channel_ids":[`+strconv.FormatInt(a.ID, 10)+`,`+strconv.FormatInt(b.ID, 10)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Replace, not append: sending only b must drop a.
	rec = doJSON(t, srv, http.MethodPut, path, `{"channel_ids":[`+strconv.FormatInt(b.ID, 10)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("assignments = %+v, want only channel %d", got, b.ID)
	}

	// And an empty set clears everything, which is how alerting is turned off.
	if rec := doJSON(t, srv, http.MethodPut, path, `{"channel_ids":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, err = db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("assignments = %+v, want none", got)
	}
}

// A request naming one unknown channel must not clear the assignments the
// monitor already had. Half-applying an alert routing change is worse than
// rejecting it: the user is told it failed while the alerts silently stop.
func TestSetMonitorChannelsIsAllOrNothing(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	good := createSlackChannel(t, srv, "a", "https://example.com/a")
	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/channels"

	if rec := doJSON(t, srv, http.MethodPut, path,
		`{"channel_ids":[`+strconv.FormatInt(good.ID, 10)+`]}`); rec.Code != http.StatusOK {
		t.Fatalf("setup: status = %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, srv, http.MethodPut, path,
		`{"channel_ids":[`+strconv.FormatInt(good.ID, 10)+`,999999]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown channel: %s", rec.Code, rec.Body.String())
	}

	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(got) != 1 || got[0].ID != good.ID {
		t.Fatalf("assignments = %+v, want the original set to survive the failed request", got)
	}
}

func TestSetMonitorChannelsRejectsUnknownMonitor(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/monitors/999999/channels", `{"channel_ids":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// Deleting a channel must take its assignments with it, or a monitor keeps a
// dangling reference and alert delivery fails at run time instead of here.
func TestDeletingAChannelClearsItsAssignments(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	c := createSlackChannel(t, srv, "a", "https://example.com/a")

	path := "/api/v1/monitors/" + strconv.FormatInt(m.ID, 10) + "/channels"
	if rec := doJSON(t, srv, http.MethodPut, path,
		`{"channel_ids":[`+strconv.FormatInt(c.ID, 10)+`]}`); rec.Code != http.StatusOK {
		t.Fatalf("setup: status = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := doJSON(t, srv, http.MethodDelete,
		"/api/v1/channels/"+strconv.FormatInt(c.ID, 10), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body.String())
	}

	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("assignments = %+v, want none after the channel was deleted", got)
	}
}

// Viewers may look at channels but must not be able to create or delete them.
func TestChannelWritesRequireAnEditor(t *testing.T) {
	srv, db := testServerWithDB(t)
	_ = db

	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels",
		strings.NewReader(`{"name":"x","type":"slack","config":{"url":"https://example.com/h"}}`))
	req.Header.Set("Authorization", "Bearer "+viewer)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a viewer creating a channel: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a viewer listing channels: %s", rec.Code, rec.Body.String())
	}
}
