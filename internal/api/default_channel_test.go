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

func TestDefaultChannelCanBeSetMovedAndCleared(t *testing.T) {
	srv, _ := testServerWithDB(t)
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")
	if a.IsDefault || b.IsDefault {
		t.Fatal("a new channel must not start as the default")
	}
	path := func(c channelResponse) string {
		return "/api/v1/channels/" + strconv.FormatInt(c.ID, 10) + "/default"
	}
	defaults := func() []string {
		rec := doJSON(t, srv, http.MethodGet, "/api/v1/channels", "")
		var got struct {
			Channels []channelResponse `json:"channels"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var names []string
		for _, c := range got.Channels {
			if c.IsDefault {
				names = append(names, c.Name)
			}
		}
		return names
	}

	if rec := doJSON(t, srv, http.MethodPut, path(a), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("set a: status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, srv, http.MethodPut, path(b), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("set b: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := defaults(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("defaults = %v, want only b", got)
	}
	if rec := doJSON(t, srv, http.MethodDelete, path(b), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("clear b: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := defaults(); len(got) != 0 {
		t.Fatalf("defaults = %v, want none", got)
	}
	if rec := doJSON(t, srv, http.MethodPut, "/api/v1/channels/4242/default", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown channel: status = %d, want 404", rec.Code)
	}
}

// Choosing where every unrouted alert goes is a write.
func TestDefaultChannelRequiresAnEditor(t *testing.T) {
	srv, db := testServerWithDB(t)
	c := createSlackChannel(t, srv, "a", "https://example.com/a")
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/channels/"+strconv.FormatInt(c.ID, 10)+"/default", nil)
		req.Header.Set("Authorization", "Bearer "+viewer)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as viewer: status = %d, want 403", method, rec.Code)
		}
	}
}

// The monitor list says who hears about an unrouted monitor, so an empty
// channel list is not mistaken for silence when a default catches it.
func TestMonitorListNamesTheDefaultForUnroutedMonitors(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	routed := seedMonitor(t, db, store.Monitor{Name: "routed", Type: "http", Target: "https://example.com"})
	unrouted := seedMonitor(t, db, store.Monitor{Name: "unrouted", Type: "http", Target: "https://example.com"})
	own, err := db.CreateChannel(ctx, store.Channel{Name: "Own", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, err := db.CreateChannel(ctx, store.Channel{Name: "Ops", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMonitorChannels(ctx, routed.ID, []int64{own.ID}); err != nil {
		t.Fatal(err)
	}

	list := func() map[int64]json.RawMessage {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
		var got struct {
			Monitors []struct {
				ID             int64           `json:"id"`
				DefaultChannel json.RawMessage `json:"default_channel"`
			} `json:"monitors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		out := map[int64]json.RawMessage{}
		for _, m := range got.Monitors {
			out[m.ID] = m.DefaultChannel
		}
		return out
	}

	if got := list(); got[unrouted.ID] != nil || got[routed.ID] != nil {
		t.Fatalf("without a default: default_channel = %s / %s, want absent", got[unrouted.ID], got[routed.ID])
	}
	if err := db.SetDefaultChannel(ctx, def.ID); err != nil {
		t.Fatal(err)
	}
	got := list()
	want := `{"id":` + strconv.FormatInt(def.ID, 10) + `,"name":"Ops"}`
	if string(got[unrouted.ID]) != want {
		t.Errorf("unrouted default_channel = %s, want %s", got[unrouted.ID], want)
	}
	if got[routed.ID] != nil {
		t.Errorf("routed monitor names the default %s; its own channel wins", got[routed.ID])
	}
}

// A failed default lookup must not let an empty list claim silence.
func TestMonitorListDefaultFailureIsUnknown(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com"})
	if _, err := db.Writer.ExecContext(t.Context(), "DROP INDEX notif_channels_one_default"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.ExecContext(t.Context(), "ALTER TABLE notif_channels DROP COLUMN is_default"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"channels"`) {
		t.Fatalf("channels claimed known while the default was unreadable: %s", rec.Body.String())
	}
}
