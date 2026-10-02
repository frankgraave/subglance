package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The monitor form chooses a monitor's own channels (SUB-179), so the routes
// it writes through are pinned here: create and PATCH carry channel_ids, the
// single-monitor read returns what the form starts from, and the version
// behind If-Match covers the assignments as well as the row.

type routedMonitor struct {
	ID       int64 `json:"id"`
	Name     string
	Channels *[]struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"channels"`
	RuleChannels *[]struct {
		TagKey   string `json:"tag_key"`
		TagValue string `json:"tag_value"`
	} `json:"rule_channels"`
	DefaultChannel *struct {
		ID int64 `json:"id"`
	} `json:"default_channel"`
}

func ownChannelIDs(t *testing.T, db *store.DB, id int64) []int64 {
	t.Helper()
	got, err := db.ListMonitorChannels(t.Context(), id)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	out := make([]int64, 0, len(got))
	for _, c := range got {
		out = append(out, c.ID)
	}
	return out
}

func idList(ids ...int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestCreateMonitorAttachesChosenChannels(t *testing.T) {
	srv, db := testServerWithDB(t)
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"site","type":"http","target":"https://example.com","channel_ids":`+idList(b.ID, a.ID)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created routedMonitor
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if got := ownChannelIDs(t, db, created.ID); len(got) != 2 || got[0] != a.ID || got[1] != b.ID {
		t.Fatalf("own channels = %v, want [%d %d]", got, a.ID, b.ID)
	}
}

// One bad id must not leave a monitor behind with no channels: the user was
// told the save failed, so nothing may have been saved.
func TestCreateMonitorWithUnknownChannelCreatesNothing(t *testing.T) {
	srv, db := testServerWithDB(t)
	a := createSlackChannel(t, srv, "a", "https://example.com/a")

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"site","type":"http","target":"https://example.com","channel_ids":`+idList(a.ID, 999999)+`}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Field != "channel_ids" || !strings.Contains(body.Error, "999999") {
		t.Errorf("error = %+v, want it to blame channel_ids and name the id", body)
	}
	monitors, err := db.ListMonitors(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 0 {
		t.Fatalf("a rejected create stored %d monitor(s)", len(monitors))
	}
}

func TestPatchMonitorReplacesOwnChannels(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")
	path := monitorPath(m.ID)

	if rec := patchWithHeaders(t, srv, path, `{"name":"renamed","channel_ids":`+idList(a.ID, b.ID)+`}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("set: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := ownChannelIDs(t, db, m.ID); len(got) != 2 {
		t.Fatalf("own channels = %v, want both", got)
	}

	// Omitted leaves them alone: a rename is not a routing change.
	if rec := patchWithHeaders(t, srv, path, `{"name":"again"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := ownChannelIDs(t, db, m.ID); len(got) != 2 {
		t.Fatalf("a rename changed the channels to %v", got)
	}

	// Replace, not append.
	if rec := patchWithHeaders(t, srv, path, `{"channel_ids":`+idList(b.ID)+`}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("replace: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := ownChannelIDs(t, db, m.ID); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("own channels = %v, want only %d", got, b.ID)
	}

	// [] removes them, which hands the monitor back to its rules or the default.
	if rec := patchWithHeaders(t, srv, path, `{"channel_ids":[]}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := ownChannelIDs(t, db, m.ID); len(got) != 0 {
		t.Fatalf("own channels = %v after [], want none", got)
	}
}

// The edit form saves the name and the channels in one request. Half of it
// landing would be a save that reports failure and changed something.
func TestPatchMonitorWithUnknownChannelChangesNothing(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	if err := db.SetMonitorChannels(t.Context(), m.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}

	rec := patchWithHeaders(t, srv, monitorPath(m.ID), `{"name":"renamed","channel_ids":[999999]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"field":"channel_ids"`) {
		t.Errorf("body = %s, want the problem placed on channel_ids", rec.Body.String())
	}
	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "site" {
		t.Errorf("name = %q: the rejected patch wrote the rest of its fields", got.Name)
	}
	if ids := ownChannelIDs(t, db, m.ID); len(ids) != 1 || ids[0] != a.ID {
		t.Errorf("own channels = %v, want the original set to survive", ids)
	}
}

// The form starts from the single-monitor read, so that read has to say
// who hears the monitor the way the list does.
func TestGetMonitorDescribesItsRouting(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
		Tags: map[string]string{"env": "prod"}})
	own := createSlackChannel(t, srv, "own", "https://example.com/own")
	ruled := createSlackChannel(t, srv, "ruled", "https://example.com/ruled")
	def := createSlackChannel(t, srv, "fallback", "https://example.com/fallback")
	if err := db.SetDefaultChannel(ctx, def.ID); err != nil {
		t.Fatal(err)
	}

	read := func() routedMonitor {
		t.Helper()
		rec := getMonitorRaw(t, srv, m.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var got routedMonitor
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Channels == nil || got.RuleChannels == nil {
			t.Fatalf("channels or rule_channels missing from %s", rec.Body.String())
		}
		return got
	}

	// Nothing of its own and no rule: the default stands in, and says so.
	if got := read(); len(*got.Channels) != 0 || got.DefaultChannel == nil || got.DefaultChannel.ID != def.ID {
		t.Fatalf("unrouted read = %+v, want no channels and the default", got)
	}

	if _, err := db.CreateRoutingRule(ctx, store.RoutingRule{TagKey: "env", TagValue: "prod", ChannelIDs: []int64{ruled.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMonitorChannels(ctx, m.ID, []int64{own.ID}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if len(*got.Channels) != 1 || (*got.Channels)[0].ID != own.ID || (*got.Channels)[0].Name != "own" {
		t.Errorf("channels = %+v, want only %q", *got.Channels, "own")
	}
	if len(*got.RuleChannels) != 1 || (*got.RuleChannels)[0].TagKey != "env" {
		t.Errorf("rule_channels = %+v, want the env:prod rule", *got.RuleChannels)
	}
	if got.DefaultChannel != nil {
		t.Errorf("default_channel = %+v beside real routes; it only stands in for none", got.DefaultChannel)
	}
}

// The edit form reads the channels beside the ETag and sends them back under
// If-Match. A change made through PUT /channels in between has to make that
// save fail, or the form puts back the set it read and the change is lost
// without anyone being told.
func TestSetMonitorChannelsInvalidatesTheMonitorVersion(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")

	etag := getMonitorRaw(t, srv, m.ID).Header().Get("ETag")
	if rec := doJSON(t, srv, http.MethodPut, monitorPath(m.ID)+"/channels", `{"channel_ids":`+idList(b.ID)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("put channels: status = %d: %s", rec.Code, rec.Body.String())
	}
	if fresh := getMonitorRaw(t, srv, m.ID).Header().Get("ETag"); fresh == etag {
		t.Fatalf("ETag stayed %s across a channel change", etag)
	}

	rec := patchWithHeaders(t, srv, monitorPath(m.ID), `{"channel_ids":`+idList(a.ID)+`}`,
		map[string]string{"If-Match": etag})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale save: status = %d, want 412: %s", rec.Code, rec.Body.String())
	}
	if got := ownChannelIDs(t, db, m.ID); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("own channels = %v, want the newer set [%d] to stand", got, b.ID)
	}
}
