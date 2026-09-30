package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/store"
)

type connectivitySettingsBody struct {
	Enabled struct {
		Value    bool    `json:"value"`
		Source   string  `json:"source"`
		PinnedBy *string `json:"pinned_by"`
	} `json:"enabled"`
	Targets struct {
		Value    []string `json:"value"`
		Source   string   `json:"source"`
		PinnedBy *string  `json:"pinned_by"`
	} `json:"targets"`
	DefaultTargets []string `json:"default_targets"`
	MaxTargets     int      `json:"max_targets"`
}

// connectivitySettingsServer wires a canary whose dials never leave the
// process, over a fresh database.
func connectivitySettingsServer(t *testing.T) (*Server, *store.DB, *connectivity.Canary, *switchableNet) {
	t.Helper()
	srv, db := testServerWithDB(t)
	network := &switchableNet{}
	canary, err := connectivity.New(connectivity.Options{
		Targets: connectivity.DefaultTargets,
		Dial:    network.dial,
		Now:     (&steppingClock{t: time.Unix(1000, 0)}).now,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithConnectivity(canary)
	return srv, db, canary, network
}

func decodeConnectivitySettings(t *testing.T, rec *httptest.ResponseRecorder) connectivitySettingsBody {
	t.Helper()
	var body connectivitySettingsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body
}

func TestConnectivitySettingsReadTheDefaults(t *testing.T) {
	srv, _, _, _ := connectivitySettingsServer(t)
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/settings/connectivity", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `W/"0"` {
		t.Fatalf("ETag = %q, want W/\"0\"", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	body := decodeConnectivitySettings(t, rec)
	if !body.Enabled.Value || body.Enabled.Source != "default" || body.Enabled.PinnedBy != nil {
		t.Fatalf("enabled = %+v", body.Enabled)
	}
	if !reflect.DeepEqual(body.Targets.Value, connectivity.DefaultTargets) || body.Targets.Source != "default" {
		t.Fatalf("targets = %+v", body.Targets)
	}
	if !reflect.DeepEqual(body.DefaultTargets, connectivity.DefaultTargets) || body.MaxTargets != connectivity.MaxTargets {
		t.Fatalf("defaults = %v, max = %d", body.DefaultTargets, body.MaxTargets)
	}
}

// The point of the endpoint: a saved change reaches the running canary, so
// the next suspected outage is judged against the new targets without a
// restart.
func TestConnectivitySettingsSaveAppliesToTheRunningCanary(t *testing.T) {
	srv, db, canary, _ := connectivitySettingsServer(t)
	rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/connectivity",
		`{"targets":[" gateway:443 ","[2001:db8::1]:22"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `W/"1"` {
		t.Fatalf("ETag = %q, want W/\"1\"", got)
	}
	want := []string{"gateway:443", "[2001:db8::1]:22"}
	body := decodeConnectivitySettings(t, rec)
	if !reflect.DeepEqual(body.Targets.Value, want) || body.Targets.Source != "database" {
		t.Fatalf("response targets = %+v", body.Targets)
	}
	if got := canary.Targets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("running canary dials %v, want %v", got, want)
	}
	// And the next start reads the same thing.
	resolved, err := db.ResolveConnectivity(context.Background(), store.ConnectivityPins{})
	if err != nil || !reflect.DeepEqual(resolved.Targets.Value, want) {
		t.Fatalf("stored = %+v, %v", resolved.Targets, err)
	}

	// Off: the canary stops holding failures back, and the dashboard's
	// endpoint says the check is off.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/settings/connectivity", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT enabled=false = %d: %s", rec.Code, rec.Body.String())
	}
	if canary.Enabled() {
		t.Fatal("the running canary is still on after it was turned off")
	}
	if got, _ := getConnectivity(t, authedHandler(srv)); got.Enabled {
		t.Fatal("GET /api/v1/connectivity still reports the check as enabled")
	}
	if got := canary.Targets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("turning the check off changed its targets to %v", got)
	}
}

func TestConnectivitySettingsRefuseBadInput(t *testing.T) {
	srv, _, canary, _ := connectivitySettingsServer(t)
	tooMany := make([]string, connectivity.MaxTargets+1)
	for i := range tooMany {
		tooMany[i] = "\"gateway:443\""
	}
	for name, body := range map[string]string{
		"no field":          `{}`,
		"empty list":        `{"targets":[]}`,
		"no port":           `{"targets":["gateway"]}`,
		"port out of range": `{"targets":["gateway:0"]}`,
		"blank entry":       `{"targets":["gateway:443","  "]}`,
		"comma in host":     `{"targets":["gate,way:443"]}`,
		"too many":          `{"targets":[` + strings.Join(tooMany, ",") + `]}`,
		"unknown field":     `{"targets":["gateway:443"],"interval":5}`,
		"null list":         `{"targets":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/connectivity", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s = %d: %s", body, rec.Code, rec.Body.String())
			}
		})
	}
	if got := canary.Targets(); !reflect.DeepEqual(got, connectivity.DefaultTargets) {
		t.Fatalf("a refused save changed the running canary to %v", got)
	}
}

// A setting fixed by a flag cannot be changed underneath it: saving would look
// like it worked and change nothing once the flag is read again.
func TestConnectivitySettingsRespectPins(t *testing.T) {
	srv, _, canary, _ := connectivitySettingsServer(t)
	srv.WithConnectivityPins(store.ConnectivityPins{
		Enabled: &store.ConnectivityEnabledPin{Value: true, By: "SUBGLANCE_CONNECTIVITY_CHECK"},
		Targets: &store.ConnectivityTargetsPin{Value: []string{"router:80"}, By: "--connectivity-targets"},
	})
	for body, by := range map[string]string{
		`{"enabled":false}`:           "SUBGLANCE_CONNECTIVITY_CHECK",
		`{"targets":["gateway:443"]}`: "--connectivity-targets",
	} {
		rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/connectivity", body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), by) {
			t.Fatalf("PUT %s = %d %s, want 409 naming %s", body, rec.Code, rec.Body.String(), by)
		}
	}
	if !canary.Enabled() || !reflect.DeepEqual(canary.Targets(), connectivity.DefaultTargets) {
		t.Fatalf("a refused save changed the running canary: %+v", canary.Settings())
	}
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/settings/connectivity", "")
	body := decodeConnectivitySettings(t, rec)
	if body.Targets.Source != "pinned" || body.Targets.PinnedBy == nil || *body.Targets.PinnedBy != "--connectivity-targets" {
		t.Fatalf("GET targets = %+v, want pinned by --connectivity-targets", body.Targets)
	}
}

func TestConnectivitySettingsIfMatch(t *testing.T) {
	srv, _, canary, _ := connectivitySettingsServer(t)
	put := func(ifMatch, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/connectivity", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", ifMatch)
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, req)
		return rec
	}
	if rec := put(`W/"0"`, `{"targets":["gateway:443"]}`); rec.Code != http.StatusOK {
		t.Fatalf("save at the current version = %d: %s", rec.Code, rec.Body.String())
	}
	rec := put(`W/"0"`, `{"targets":["other:22"]}`)
	if rec.Code != http.StatusPreconditionFailed || rec.Header().Get("ETag") != "" {
		t.Fatalf("stale save = %d, ETag %q: %s", rec.Code, rec.Header().Get("ETag"), rec.Body.String())
	}
	if got := canary.Targets(); !reflect.DeepEqual(got, []string{"gateway:443"}) {
		t.Fatalf("a refused stale save reached the canary: %v", got)
	}
	if rec := put(`nonsense`, `{"enabled":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed If-Match = %d", rec.Code)
	}
	if rec := put(`*`, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("If-Match: * = %d", rec.Code)
	}
}

// The targets can name hosts on the operator's own network: only an
// administrator reads or changes them.
func TestConnectivitySettingsAreForAdministrators(t *testing.T) {
	srv, db, _, _ := connectivitySettingsServer(t)
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, srv, db, string(role)+"@connectivity-settings.test", role)
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			req := httptest.NewRequest(method, "/api/v1/settings/connectivity", strings.NewReader(`{"enabled":false}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d, want 403", role, method, rec.Code)
			}
		}
	}
}

// Without a canary there is nothing a saved change would reach, so the
// endpoint says so instead of storing a setting nothing reads until restart.
func TestConnectivitySettingsWithoutACanary(t *testing.T) {
	srv, _ := testServerWithDB(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := doJSON(t, srv, method, "/api/v1/settings/connectivity", `{"enabled":true}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without a canary = %d", method, rec.Code)
		}
	}
}
