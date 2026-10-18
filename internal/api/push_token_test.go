package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// replacePushToken asks for a new push URL through the API and returns the
// response.
func replacePushToken(t *testing.T, srv *Server, id int64) monitorResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost,
		"/api/v1/monitors/"+itoa(id)+"/push-token", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("replace push token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q: a response carrying a credential must not be cached", cc)
	}
	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func pushPathOf(url string) string { return url[strings.Index(url, pushPath):] }

// TestReplacePushTokenSwapsTheURLAndKeepsHistory walks the case the route
// exists for: a URL leaked, the owner replaces it, the leaked one is refused
// like any made-up token, the new one records, and nothing the monitor had
// measured is lost.
func TestReplacePushTokenSwapsTheURLAndKeepsHistory(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	ctx := context.Background()
	at := time.Now().Add(-30 * time.Minute)
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: created.ID, TS: at, OK: true}); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if _, err := db.OpenIncident(ctx, created.ID, at, "push", "no report"); err != nil {
		t.Fatalf("open incident: %v", err)
	}

	replaced := replacePushToken(t, srv, created.ID)
	if replaced.ID != created.ID || replaced.Name != created.Name {
		t.Errorf("response describes monitor %d %q, want %d %q", replaced.ID, replaced.Name, created.ID, created.Name)
	}
	if replaced.PushURL == "" || replaced.PushURL == created.PushURL {
		t.Fatalf("push_url = %q, want a new one (old %q)", replaced.PushURL, created.PushURL)
	}
	if !strings.HasPrefix(pushPathOf(replaced.PushURL), pushPath+replaced.PushTokenPrefix) {
		t.Errorf("push_token_prefix %q does not start the new URL %q", replaced.PushTokenPrefix, replaced.PushURL)
	}

	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pushPathOf(created.PushURL), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("old URL: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown push URL") {
		t.Errorf("old URL: body = %s, want the answer an unknown token gets", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pushPathOf(replaced.PushURL), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("new URL: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if len(pusher.ids) != 1 || pusher.ids[0] != created.ID {
		t.Fatalf("recorded for %v, want exactly monitor %d", pusher.ids, created.ID)
	}

	hbs, err := db.ListHeartbeats(ctx, created.ID, 10)
	if err != nil || len(hbs) != 1 {
		t.Errorf("heartbeats = %d, %v; want the one from before the replace", len(hbs), err)
	}
	incs, err := db.ListIncidents(ctx, created.ID, 10)
	if err != nil || len(incs) != 1 {
		t.Errorf("incidents = %d, %v; want the one from before the replace", len(incs), err)
	}

	// The new URL is shown once, like the first one: no read returns it.
	for _, path := range []string{"/api/v1/monitors", "/api/v1/monitors/" + itoa(created.ID)} {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		token := replaced.PushURL[strings.LastIndex(replaced.PushURL, "/")+1:]
		if strings.Contains(rec.Body.String(), token) {
			t.Errorf("GET %s contains the new push token", path)
		}
	}
}

func TestReplacePushTokenRefusals(t *testing.T) {
	srv, db := testServerWithDB(t)
	push := createPushMonitor(t, srv, pushMonitorBody)
	httpMon := seedCheckMonitor(t, db, "site", true)

	cases := map[string]struct {
		id        string
		token     string
		want      int
		wantField string
	}{
		"not a push monitor": {id: itoa(httpMon.ID), want: http.StatusBadRequest, wantField: "type"},
		"unknown id":         {id: "999999", want: http.StatusNotFound},
		"invalid id":         {id: "abc", want: http.StatusBadRequest},
		"viewer":             {id: itoa(push.ID), token: seedUser(t, srv, db, "viewer@push-token.test", store.RoleViewer), want: http.StatusForbidden},
		"signed out":         {id: itoa(push.ID), token: "-", want: http.StatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := jsonRequest(http.MethodPost, "/api/v1/monitors/"+tc.id+"/push-token", "")
			rec := httptest.NewRecorder()
			switch tc.token {
			case "":
				authedHandler(srv).ServeHTTP(rec, r)
			case "-":
				srv.Handler().ServeHTTP(rec, r)
			default:
				r.Header.Set("Authorization", "Bearer "+tc.token)
				srv.Handler().ServeHTTP(rec, r)
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.wantField != "" {
				var got errorResponse
				if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got.Field != tc.wantField {
					t.Errorf("field = %q, want %q", got.Field, tc.wantField)
				}
			}
		})
	}

	// None of the refusals touched the push monitor's URL.
	if _, err := db.MonitorByPushToken(context.Background(),
		push.PushURL[strings.LastIndex(push.PushURL, "/")+1:]); err != nil {
		t.Errorf("a refused replace retired the URL: %v", err)
	}
}
