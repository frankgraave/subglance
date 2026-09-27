package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

func decodeRule(t *testing.T, rec *httptest.ResponseRecorder) routingRuleResponse {
	t.Helper()
	var got routingRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode rule: %v: %s", err, rec.Body.String())
	}
	return got
}

func TestRoutingRulesCanBeCreatedUpdatedAndDeleted(t *testing.T) {
	srv, db := testServerWithDB(t)
	a := createSlackChannel(t, srv, "a", "https://example.com/a")
	b := createSlackChannel(t, srv, "b", "https://example.com/b")
	ids := func(c ...channelResponse) string {
		out := "["
		for i, ch := range c {
			if i > 0 {
				out += ","
			}
			out += strconv.FormatInt(ch.ID, 10)
		}
		return out + "]"
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules",
		`{"tag_key":" Env ","tag_value":" prod ","channel_ids":`+ids(b, a)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", rec.Code, rec.Body.String())
	}
	rule := decodeRule(t, rec)
	if rule.TagKey != "env" || rule.TagValue != "prod" || len(rule.ChannelIDs) != 2 || rule.ChannelIDs[0] != a.ID {
		t.Fatalf("created = %+v, want env=prod with both channels sorted", rule)
	}
	if rule.ExcludedMonitorIDs == nil {
		t.Fatal("excluded_monitor_ids must be [] rather than null")
	}

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"duplicate tag pair": {`{"tag_key":"env","tag_value":"prod"}`, http.StatusConflict},
		"unknown channel":    {`{"tag_key":"env","tag_value":"dev","channel_ids":[4242]}`, http.StatusBadRequest},
		"invalid tag key":    {`{"tag_key":"has space","tag_value":"x"}`, http.StatusBadRequest},
		"missing value":      {`{"tag_key":"env"}`, http.StatusBadRequest},
		"unknown field":      {`{"tag_key":"env","tag_value":"x","first_match":true}`, http.StatusBadRequest},
	} {
		if rec := doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules", tc.body); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d: %s", name, rec.Code, tc.want, rec.Body.String())
		}
	}

	path := "/api/v1/routing-rules/" + strconv.FormatInt(rule.ID, 10)
	rec = doJSON(t, srv, http.MethodPut, path, `{"tag_key":"env","tag_value":"live","channel_ids":`+ids(b)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeRule(t, rec); got.TagValue != "live" || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != b.ID {
		t.Fatalf("updated = %+v", got)
	}
	if rec := doJSON(t, srv, http.MethodPut, "/api/v1/routing-rules/4242", `{"tag_key":"a","tag_value":"b"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("update unknown: status = %d, want 404", rec.Code)
	}

	rec = doJSON(t, srv, http.MethodGet, "/api/v1/routing-rules", "")
	var list struct {
		Rules []routingRuleResponse `json:"rules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Rules) != 1 {
		t.Fatalf("list = %s (%v)", rec.Body.String(), err)
	}

	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status = %d, want 404", rec.Code)
	}
	if rules, _ := db.ListRoutingRules(t.Context()); len(rules) != 0 {
		t.Fatalf("rules left: %+v", rules)
	}
}

func TestRoutingRuleExclusions(t *testing.T) {
	srv, db := testServerWithDB(t)
	c := createSlackChannel(t, srv, "pager", "https://example.com/p")
	m := seedMonitor(t, db, store.Monitor{Name: "noisy", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	rule, err := db.CreateRoutingRule(t.Context(), store.RoutingRule{TagKey: "env", TagValue: "prod", ChannelIDs: []int64{c.ID}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/routing-rules/" + strconv.FormatInt(rule.ID, 10) + "/exclusions/" + strconv.FormatInt(m.ID, 10)

	for range 2 {
		if rec := doJSON(t, srv, http.MethodPut, path, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("exclude: status = %d: %s", rec.Code, rec.Body.String())
		}
	}
	if got, _, _ := db.AlertChannels(t.Context(), m.ID); len(got) != 0 {
		t.Fatalf("an excluded monitor still reaches %+v", got)
	}
	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("include: status = %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("include twice: status = %d, want 404", rec.Code)
	}
	for _, bad := range []struct {
		path string
		want int
	}{
		{"/api/v1/routing-rules/4242/exclusions/" + strconv.FormatInt(m.ID, 10), http.StatusNotFound},
		{"/api/v1/routing-rules/" + strconv.FormatInt(rule.ID, 10) + "/exclusions/4242", http.StatusNotFound},
		{"/api/v1/routing-rules/x/exclusions/1", http.StatusBadRequest},
		{"/api/v1/routing-rules/1/exclusions/0", http.StatusBadRequest},
	} {
		if rec := doJSON(t, srv, http.MethodPut, bad.path, ""); rec.Code != bad.want {
			t.Errorf("PUT %s: status = %d, want %d", bad.path, rec.Code, bad.want)
		}
	}
}

// Routing decides who hears about an outage, so a viewer may read the rules
// but not change them.
func TestRoutingRulesRequireAnEditorToChange(t *testing.T) {
	srv, db := testServerWithDB(t)
	viewer := seedUser(t, srv, db, "viewer@example.com", store.RoleViewer)
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+viewer)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := do(http.MethodGet, "/api/v1/routing-rules"); code != http.StatusOK {
		t.Fatalf("viewer list: status = %d, want 200", code)
	}
	for _, rt := range [][2]string{
		{http.MethodPost, "/api/v1/routing-rules"},
		{http.MethodPut, "/api/v1/routing-rules/1"},
		{http.MethodDelete, "/api/v1/routing-rules/1"},
		{http.MethodPut, "/api/v1/routing-rules/1/exclusions/1"},
		{http.MethodDelete, "/api/v1/routing-rules/1/exclusions/1"},
	} {
		if code := do(rt[0], rt[1]); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: status = %d, want 403", rt[0], rt[1], code)
		}
	}
}

// The monitor list names the rule behind each rule-routed channel, and a
// monitor a rule catches no longer claims the default.
func TestMonitorListNamesTheRuleBehindEachChannel(t *testing.T) {
	srv, db := testServerWithDB(t)
	ctx := t.Context()
	tagged := seedMonitor(t, db, store.Monitor{Name: "tagged", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	plain := seedMonitor(t, db, store.Monitor{Name: "plain", Type: "http", Target: "https://example.com"})
	pager, err := db.CreateChannel(ctx, store.Channel{Name: "Pager", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, err := db.CreateChannel(ctx, store.Channel{Name: "Ops", Type: store.ChannelWebhook, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultChannel(ctx, def.ID); err != nil {
		t.Fatal(err)
	}
	rule, err := db.CreateRoutingRule(ctx, store.RoutingRule{TagKey: "env", TagValue: "prod", ChannelIDs: []int64{pager.ID}})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil))
	var got struct {
		Monitors []struct {
			ID             int64           `json:"id"`
			RuleChannels   json.RawMessage `json:"rule_channels"`
			DefaultChannel json.RawMessage `json:"default_channel"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]int{}
	for i, m := range got.Monitors {
		byID[m.ID] = i
	}
	tm, pm := got.Monitors[byID[tagged.ID]], got.Monitors[byID[plain.ID]]
	want := `[{"rule_id":` + strconv.FormatInt(rule.ID, 10) + `,"tag_key":"env","tag_value":"prod","channels":[{"id":` +
		strconv.FormatInt(pager.ID, 10) + `,"name":"Pager"}]}]`
	if string(tm.RuleChannels) != want {
		t.Errorf("tagged rule_channels = %s, want %s", tm.RuleChannels, want)
	}
	if tm.DefaultChannel != nil {
		t.Errorf("tagged monitor claims the default %s; the rule catches it", tm.DefaultChannel)
	}
	if string(pm.RuleChannels) != "[]" || pm.DefaultChannel == nil {
		t.Errorf("plain monitor: rule_channels = %s, default = %s; want [] and the default", pm.RuleChannels, pm.DefaultChannel)
	}
}
