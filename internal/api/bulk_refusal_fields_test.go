package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The bulk tag and channel changes and a token's expiry refuse a single body
// field, so each refusal names it: a client places the sentence beside that
// input instead of matching on its words, and reads it without the store's
// Go-error prefix in front.

func TestBulkTagRefusalsNameTheirField(t *testing.T) {
	s, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "a", Type: "http", Target: "https://example.com", Tags: map[string]string{"env": "prod"}})
	id := itoa(m.ID)
	for name, tc := range map[string]struct{ body, field, want string }{
		// The action is refused as itself, not as the selection it would
		// not have allowed.
		"unknown action":        {`{"action":"bogus","monitor_ids":[` + id + `],"key":"env","value":"x"}`, "action", "action must be apply, remove, rename_key or rename_value"},
		"empty action":          {`{"monitor_ids":[` + id + `],"key":"env","value":"x"}`, "action", "action must be apply"},
		"selection on a rename": {`{"action":"rename_key","monitor_ids":[1],"key":"env","new_key":"x"}`, "monitor_ids", "monitor_ids is not allowed for action rename_key"},
		"new_key on apply":      {`{"action":"apply","monitor_ids":[` + id + `],"key":"env","value":"x","new_key":"y"}`, "new_key", "new_key is not allowed for action apply"},
		"empty selection":       {`{"action":"apply","monitor_ids":[],"key":"env","value":"x"}`, "monitor_ids", "monitor_ids must contain between 1 and 10000 IDs"},
		"duplicate id":          {`{"action":"apply","monitor_ids":[` + id + `,` + id + `],"key":"env","value":"x"}`, "monitor_ids", "monitor_ids must be positive and unique"},
		"empty key":             {`{"action":"apply","monitor_ids":[` + id + `],"key":"","value":"x"}`, "key", "tag key must not be empty"},
		"bad key":               {`{"action":"apply","monitor_ids":[` + id + `],"key":"Bad Key!","value":"x"}`, "key", "must be lowercase letters"},
		// One tag rule refuses both halves of the pair; an empty value with
		// a good key is the value's fault.
		"empty value":     {`{"action":"apply","monitor_ids":[` + id + `],"key":"env","value":""}`, "value", "must have a value"},
		"long value":      {`{"action":"remove","monitor_ids":[` + id + `],"key":"env","value":"` + strings.Repeat("v", 65) + `"}`, "value", "longer than 64 characters"},
		"bad new key":     {`{"action":"rename_key","key":"env","new_key":"bad key"}`, "new_key", "must be lowercase letters"},
		"same new key":    {`{"action":"rename_key","key":" Env ","new_key":"env"}`, "new_key", "new_key must differ from key"},
		"bad new value":   {`{"action":"rename_value","key":"env","value":"prod","new_value":"a\tb"}`, "new_value", "must not contain line breaks or tabs"},
		"same new value":  {`{"action":"rename_value","key":"env","value":"prod","new_value":"prod"}`, "new_value", "new_value must differ from value"},
		"empty new value": {`{"action":"rename_value","key":"env","value":"prod","new_value":""}`, "new_value", "must have a value"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/preview", ""} {
				etag := ""
				if path == "" {
					etag = `"tags-` + strings.Repeat("0", 64) + `"`
				}
				w := tagRequest(s, path, tc.body, etag)
				wantStatusProblem(t, w.Code, w.Body.String(), tc.field, tc.want)
				wantNoStorePrefix(t, w.Body.String(), "invalid tag operation")
			}
		})
	}
}

// A change that would leave a monitor with too many tags is about the result,
// not one input, so it names no field; it still drops the store's prefix.
func TestBulkTagResultRefusalNamesNoField(t *testing.T) {
	s, db := testServerWithDB(t)
	tags := map[string]string{}
	for i := range store.MaxTagsPerMonitor {
		tags["k"+itoa(int64(i))] = "v"
	}
	m := seedMonitor(t, db, store.Monitor{Name: "full", Type: "http", Target: "https://example.com", Tags: tags})
	w := tagRequest(s, "/preview", `{"action":"apply","monitor_ids":[`+itoa(m.ID)+`],"key":"extra","value":"x"}`, "")
	wantStatusProblem(t, w.Code, w.Body.String(), "", "at most 20 tags per monitor")
	wantNoStorePrefix(t, w.Body.String(), "invalid tag operation")
}

func TestBulkChannelRefusalsNameTheirField(t *testing.T) {
	s, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "a", Type: "http", Target: "https://example.com"})
	id := itoa(m.ID)
	cid := itoa(bulkChannel(t, db, "ops", true).ID)
	for name, tc := range map[string]struct{ body, field, want string }{
		"unknown action":  {`{"action":"replace","monitor_ids":[` + id + `],"channel_id":` + cid + `}`, "action", "action must be add or remove"},
		"no channel":      {`{"action":"add","monitor_ids":[` + id + `],"channel_id":0}`, "channel_id", "channel_id must be a positive channel ID"},
		"empty selection": {`{"action":"add","monitor_ids":[],"channel_id":` + cid + `}`, "monitor_ids", "monitor_ids must contain between 1 and 10000 IDs"},
		"duplicate id":    {`{"action":"add","monitor_ids":[` + id + `,` + id + `],"channel_id":` + cid + `}`, "monitor_ids", "monitor_ids must be positive and unique"},
		"negative id":     {`{"action":"remove","monitor_ids":[-1],"channel_id":` + cid + `}`, "monitor_ids", "monitor_ids must be positive and unique"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/monitors/channels/preview", "/api/v1/monitors/channels"} {
				r := jsonRequest(http.MethodPost, path, tc.body)
				if !strings.HasSuffix(path, "/preview") {
					r.Header.Set("If-Match", `"channels-`+strings.Repeat("0", 64)+`"`)
				}
				w := httptest.NewRecorder()
				authedHandler(s).ServeHTTP(w, r)
				wantStatusProblem(t, w.Code, w.Body.String(), tc.field, tc.want)
				wantNoStorePrefix(t, w.Body.String(), "invalid channel operation")
			}
		})
	}
}

func TestTokenExpiryRefusalsNameTheField(t *testing.T) {
	s, _ := testServerWithDB(t)
	for name, tc := range map[string]struct{ body, want string }{
		"not a duration": {`{"name":"x","expires_in":"tomorrow"}`, "expires_in must be a duration such as 720h"},
		"negative":       {`{"name":"x","expires_in":"-1h"}`, "expires_in must be positive"},
		"zero":           {`{"name":"x","expires_in":"0s"}`, "expires_in must be positive"},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			authedHandler(s).ServeHTTP(w, jsonRequest(http.MethodPost, "/api/v1/tokens", tc.body))
			wantStatusProblem(t, w.Code, w.Body.String(), "expires_in", tc.want)
		})
	}
}

func wantNoStorePrefix(t *testing.T, body, prefix string) {
	t.Helper()
	var e errorResponse
	must(t, json.Unmarshal([]byte(body), &e))
	if strings.Contains(e.Error, prefix) {
		t.Errorf("error = %q, want it without the store's %q prefix", e.Error, prefix)
	}
}
