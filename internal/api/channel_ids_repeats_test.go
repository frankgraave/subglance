package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Every route that assigns channels answers a channel_ids list that repeats
// one id 500,000 times (about 1 MiB, the body limit) quickly, and stores the
// channel once. Each repeat used to cost a lookup and an insert on the single
// writer connection: 8 s for that body, with every other write on the
// instance waiting behind it.
func TestChannelIDsRepeatingOneIDAreAnsweredQuickly(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules",
		`{"tag_key":"env","tag_value":"prod"}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup rule: status = %d: %s", rec.Code, rec.Body.String())
	}
	id := strconv.FormatInt(a.ID, 10)
	list := `[` + strings.TrimSuffix(strings.Repeat(id+",", 500_000), ",") + `]`

	routes := []struct {
		name, method, path, body string
		want                     int
	}{
		{"monitor channels", http.MethodPut, monitorPath(m.ID) + "/channels", `{"channel_ids":` + list + `}`, http.StatusOK},
		{"monitor edit", http.MethodPatch, monitorPath(m.ID), `{"channel_ids":` + list + `}`, http.StatusOK},
		{"monitor create", http.MethodPost, "/api/v1/monitors",
			`{"name":"new","type":"http","target":"https://example.org","channel_ids":` + list + `}`, http.StatusCreated},
		{"rule create", http.MethodPost, "/api/v1/routing-rules",
			`{"tag_key":"env","tag_value":"dev","channel_ids":` + list + `}`, http.StatusCreated},
		{"rule update", http.MethodPut, "/api/v1/routing-rules/1",
			`{"tag_key":"env","tag_value":"prod","channel_ids":` + list + `}`, http.StatusOK},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			if len(rt.body) > maxJSONBody {
				t.Fatalf("body is %d bytes, over the %d byte limit", len(rt.body), maxJSONBody)
			}
			start := time.Now()
			rec := doJSON(t, srv, rt.method, rt.path, rt.body)
			took := time.Since(start)
			if rec.Code != rt.want {
				t.Fatalf("status = %d, want %d: %.300s", rec.Code, rt.want, rec.Body.String())
			}
			// The repeats took about 8 s on each route before. Now the time
			// is decoding the body: about 70 ms, 700 ms under the race
			// detector. Three seconds leaves a slow CI runner room and still
			// fails the old cost by more than twice over.
			if took > 3*time.Second {
				t.Errorf("answered in %s, want under 3s", took)
			}
		})
	}

	got, err := db.ListMonitorChannels(t.Context(), m.ID)
	if err != nil || len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("monitor channels = %+v (err %v), want only channel %d", got, err, a.ID)
	}
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/routing-rules", "")
	var rules struct {
		Rules []routingRuleResponse `json:"rules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rules); err != nil {
		t.Fatalf("decode rules: %v: %s", err, rec.Body.String())
	}
	for _, r := range rules.Rules {
		if !slices.Equal(r.ChannelIDs, []int64{a.ID}) {
			t.Errorf("rule %s=%s channels = %v, want [%d]", r.TagKey, r.TagValue, r.ChannelIDs, a.ID)
		}
	}
	if len(rules.Rules) != 2 {
		t.Errorf("rules = %+v, want 2", rules.Rules)
	}
}
