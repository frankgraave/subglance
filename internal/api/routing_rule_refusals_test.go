package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Every route that takes a tag pair or a channel id answers a refusal with
// the request field it is about and in the API's own words. The routing rule
// routes and the two channel assignment routes answered with the store's
// error text, "store: unknown notification channel: 4242", and named no field,
// so a form could only show the sentence somewhere above its inputs.
func TestRoutingRuleRefusalsNameTheirField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	createSlackChannel(t, srv, "a", "https://example.com/a")
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules",
		`{"tag_key":"env","tag_value":"prod"}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup: status = %d: %s", rec.Code, rec.Body.String())
	}
	cases := map[string]struct {
		body, field, want string
	}{
		"unknown channel": {`{"tag_key":"env","tag_value":"dev","channel_ids":[4242]}`,
			"channel_ids", "channel 4242 does not exist; it may have been deleted"},
		"invalid key": {`{"tag_key":"has space","tag_value":"x"}`, "tag_key", `tag key "has space"`},
		"empty key":   {`{"tag_key":" ","tag_value":"x"}`, "tag_key", "tag key must not be empty"},
		// The key is valid on its own, so a missing or broken value is the
		// value's fault even though one tag rule refuses the pair.
		"missing value": {`{"tag_key":"env"}`, "tag_value", "must have a value"},
		"value with tab": {`{"tag_key":"env","tag_value":"a\tb"}`, "tag_value",
			"must not contain line breaks, tabs or other control characters"},
	}
	for name, tc := range cases {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(name+"/"+method, func(t *testing.T) {
				path := "/api/v1/routing-rules"
				if method == http.MethodPut {
					path += "/1"
				}
				rec := doJSON(t, srv, method, path, tc.body)
				wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
				if strings.Contains(rec.Body.String(), "store:") {
					t.Errorf("refusal carries the store's prefix: %s", rec.Body.String())
				}
			})
		}
	}
}

func TestAssigningAnUnknownChannelNamesTheField(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	id := itoa(m.ID)
	want := "channel 4242 does not exist; it may have been deleted"

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/monitors/"+id+"/channels", `{"channel_ids":[4242]}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "channel_ids", want)

	// The bulk routes take one channel, under the singular name.
	rec = channelOpRequest(srv, "/preview", `{"action":"add","monitor_ids":[`+id+`],"channel_id":4242}`, "")
	wantStatusProblem(t, rec.Code, rec.Body.String(), "channel_id", want)
}

// Excluding a monitor from a rule names which of the two is missing, as the
// API document promises, in the API's words rather than "store: not found:".
func TestExcludingFromAMissingRuleOrMonitorSaysWhichIsMissing(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/routing-rules",
		`{"tag_key":"env","tag_value":"prod"}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup: status = %d: %s", rec.Code, rec.Body.String())
	}
	for name, tc := range map[string]struct{ path, want string }{
		"missing monitor": {"/api/v1/routing-rules/1/exclusions/4242", "monitor 4242 does not exist"},
		"missing rule":    {"/api/v1/routing-rules/99/exclusions/" + itoa(m.ID), "routing rule 99 does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPut, tc.path, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
			}
			if got := decodeErrorResponse(t, rec).Error; got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
		})
	}
}
