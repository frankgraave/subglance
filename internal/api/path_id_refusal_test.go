package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pathIDResource names the resource each collection's {id} addresses, as the
// refusal of a malformed id should word it. A route under a collection that
// is missing here fails TestMalformedPathIDNamesItsResource, so whoever adds
// one decides its noun instead of inheriting another resource's.
var pathIDResource = map[string]string{
	"monitors":      "monitor",
	"channels":      "channel",
	"tokens":        "token",
	"users":         "user",
	"sessions":      "session",
	"incidents":     "incident",
	"maintenance":   "maintenance",
	"routing-rules": "routing rule",
}

// TestMalformedPathIDNamesItsResource walks every route with an {id} or
// {monitor_id} wildcard and sends a malformed value in it. The 400 must name
// the resource the path is about: a channel, token or user id refused as
// "invalid monitor id" sends the client looking for a monitor id it never
// sent.
func TestMalformedPathIDNamesItsResource(t *testing.T) {
	srv, _ := testServerWithDB(t)
	// Without a tester the channel test route refuses before it reads its id.
	srv = srv.WithChannelTester(&stubTester{})
	h := authedHandler(srv)

	checked := 0
	for _, rt := range srv.routes() {
		segs := strings.Split(strings.TrimPrefix(rt.Pattern, "/api/v1/"), "/")
		for i, seg := range segs {
			if seg != "{id}" && seg != "{monitor_id}" {
				continue
			}
			want := "monitor"
			if seg == "{id}" {
				collection := ""
				if i > 0 {
					collection = segs[i-1]
				}
				noun, ok := pathIDResource[collection]
				if !ok {
					t.Errorf("%s %s: no resource noun for the %q collection; add it to pathIDResource", rt.Method, rt.Pattern, collection)
					continue
				}
				want = noun
			}
			for _, bad := range []string{"abc", "0", "-1", "99999999999999999999"} {
				path := rt.Pattern
				for _, other := range []string{"{id}", "{monitor_id}"} {
					v := "1"
					if other == seg {
						v = bad
					}
					path = strings.ReplaceAll(path, other, v)
				}
				req := httptest.NewRequest(rt.Method, path, strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				var body struct {
					Error string `json:"error"`
					Field string `json:"field"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				wantMsg := "invalid " + want + " id"
				if rec.Code != http.StatusBadRequest || body.Error != wantMsg || body.Field != "" {
					t.Errorf("%s %s: got %d %q (field %q), want 400 %q", rt.Method, path, rec.Code, body.Error, body.Field, wantMsg)
				}
			}
			checked++
		}
	}
	// The walk is the test; a refactor that renamed the wildcard would make
	// it pass by checking nothing.
	if checked < 35 {
		t.Fatalf("checked %d id wildcards; the route table has more than that", checked)
	}
}
