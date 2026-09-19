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

func TestMonitorListIncludesAllChannelAttachments(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	channel, err := db.CreateChannel(ctx, store.Channel{Name: "Operations", Type: store.ChannelWebhook, Enabled: false, Config: map[string]string{"url": "https://secret.invalid/token"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateChannel(ctx, store.Channel{Name: "Backup", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 45)
	for i := range ids {
		ids[i] = seedMonitor(t, db, store.Monitor{Name: strconv.Itoa(i), Type: "http", Target: "https://example.com"}).ID
	}
	if err := db.SetMonitorChannels(ctx, ids[44], []int64{second.ID, channel.ID}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Monitors []struct {
			ID       int64           `json:"id"`
			Channels json.RawMessage `json:"channels"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != len(ids) {
		t.Fatalf("got %d monitors, want %d", len(got.Monitors), len(ids))
	}
	for _, m := range got.Monitors {
		want := `[]`
		if m.ID == ids[44] {
			want = `[{"id":` + strconv.FormatInt(channel.ID, 10) + `,"name":"Operations"},{"id":` + strconv.FormatInt(second.ID, 10) + `,"name":"Backup"}]`
		}
		if string(m.Channels) != want {
			t.Errorf("monitor %d channels = %s, want %s", m.ID, m.Channels, want)
		}
	}
	if strings.Contains(rec.Body.String(), "secret.invalid") || strings.Contains(rec.Body.String(), "config") {
		t.Fatal("list leaked channel configuration")
	}
}

func TestMonitorListChannelFailureIsUnknownNotEmpty(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com"})
	if _, err := db.Writer.ExecContext(t.Context(), "DROP TABLE monitor_channels"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("channel failure must not blank monitors: %d", rec.Code)
	}
	var got struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 1 {
		t.Fatalf("monitors = %d", len(got.Monitors))
	}
	if _, ok := got.Monitors[0]["channels"]; ok {
		t.Fatal("failed channel query claimed a known attachment set")
	}
}
