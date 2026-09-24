package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func TestQuietHoursRoundTrip(t *testing.T) {
	srv, _ := testServerWithDB(t)
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	path := fmt.Sprintf("/api/v1/channels/%d/quiet-hours", ch.ID)

	// During omitted: the answer must be the one that cannot lose an alert.
	rec := doJSON(t, srv, http.MethodPut, path,
		`{"start":"23:00","end":"07:00","timezone":"Europe/Amsterdam"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: status %d: %s", rec.Code, rec.Body.String())
	}
	var set store.QuietHours
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if set.During != store.QuietHold {
		t.Fatalf("during defaulted to %q, want hold", set.During)
	}

	rec = doJSON(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/channels/%d", ch.ID), "")
	var got channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.QuietHours == nil || got.QuietHours.Start != "23:00" || got.QuietHours.Timezone != "Europe/Amsterdam" {
		t.Fatalf("channel read back quiet hours %+v", got.QuietHours)
	}

	rec = doJSON(t, srv, http.MethodGet, "/api/v1/channels", "")
	if !strings.Contains(rec.Body.String(), `"quiet_hours":{"start":"23:00"`) {
		t.Fatalf("list does not carry quiet hours: %s", rec.Body.String())
	}

	// A channel replace leaves quiet hours alone.
	rec = doJSON(t, srv, http.MethodPut, fmt.Sprintf("/api/v1/channels/%d", ch.ID),
		`{"name":"phone 2","type":"slack","config":{"url":"https://hooks.slack.com/services/T/B/secret"}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"quiet_hours":{`) {
		t.Fatalf("replace dropped quiet hours: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodDelete, path, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear: status %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, path, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second clear: status %d, want 404", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/channels/%d", ch.ID), "")
	if !strings.Contains(rec.Body.String(), `"quiet_hours":null`) {
		t.Fatalf("cleared channel still reports quiet hours: %s", rec.Body.String())
	}
}

func TestQuietHoursRejectsBadInput(t *testing.T) {
	srv, _ := testServerWithDB(t)
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	path := fmt.Sprintf("/api/v1/channels/%d/quiet-hours", ch.ID)

	for _, body := range []string{
		`{"start":"23:00","end":"07:00","timezone":"Local"}`,
		`{"start":"23:00","end":"07:00","timezone":"Mars/Olympus"}`,
		`{"start":"11pm","end":"07:00","timezone":"UTC"}`,
		`{"start":"07:00","end":"07:00","timezone":"UTC"}`,
		`{"start":"23:00","end":"07:00","timezone":"UTC","during":"snooze"}`,
		`{"start":"23:00","end":"07:00","timezone":"UTC","extra":1}`,
	} {
		if rec := doJSON(t, srv, http.MethodPut, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, rec.Code)
		}
	}

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/999/quiet-hours",
		`{"start":"23:00","end":"07:00","timezone":"UTC"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing channel: status %d, want 404", rec.Code)
	}
}

// A viewer must not be able to silence the channel everyone else relies on.
func TestQuietHoursRequireAnEditor(t *testing.T) {
	srv, db := testServerWithDB(t)
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)

	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/channels/%d/quiet-hours", ch.ID),
		strings.NewReader(`{"start":"23:00","end":"07:00","timezone":"UTC","during":"drop"}`))
	req.Header.Set("Authorization", "Bearer "+viewer)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for a viewer setting quiet hours", rec.Code)
	}
}

// TestChannelReplaceFailsWhenQuietHoursCannotBeRead: a replace must not
// answer 200 with quiet_hours: null when the lookup failed, since null means
// none are configured. It must also fail before the channel is changed.
func TestChannelReplaceFailsWhenQuietHoursCannotBeRead(t *testing.T) {
	srv, db := testServerWithDB(t)
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	if _, err := db.Writer.ExecContext(t.Context(), "ALTER TABLE notif_quiet_hours RENAME TO notif_quiet_hours_gone"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, http.MethodPut, fmt.Sprintf("/api/v1/channels/%d", ch.ID),
		`{"name":"phone 2","type":"slack","config":{"url":"https://hooks.slack.com/services/T/B/secret"}}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("replace with unreadable quiet hours: status %d, want 500: %s", rec.Code, rec.Body.String())
	}
	got, err := db.GetChannel(t.Context(), ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "phone" {
		t.Fatalf("channel was renamed to %q despite the failed request", got.Name)
	}
}

// TestSetMonitorChannelsFailsBeforeWritingWhenQuietHoursCannotBeRead: the
// assignment replace answers 500 when quiet hours cannot be read, so it must
// not have stored the new assignments either, or the 500 is a false failure.
func TestSetMonitorChannelsFailsBeforeWritingWhenQuietHoursCannotBeRead(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	if _, err := db.Writer.ExecContext(t.Context(), "ALTER TABLE notif_quiet_hours RENAME TO notif_quiet_hours_gone"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, http.MethodPut, fmt.Sprintf("/api/v1/monitors/%d/channels", m.ID),
		fmt.Sprintf(`{"channel_ids":[%d]}`, ch.ID))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("assign with unreadable quiet hours: status %d, want 500: %s", rec.Code, rec.Body.String())
	}
	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("assignments = %+v stored despite the 500", got)
	}
}
