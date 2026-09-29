package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/store"
)

// switchableNet answers canary dials without touching the network. Every dial
// is counted, so a test can prove that reading the endpoint dials nothing.
type switchableNet struct {
	up    atomic.Bool
	dials atomic.Int64
}

func (n *switchableNet) dial(_ context.Context, _, _ string) (net.Conn, error) {
	n.dials.Add(1)
	if !n.up.Load() {
		return nil, errors.New("network is unreachable")
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

type steppingClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *steppingClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *steppingClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type connectivityBody struct {
	Enabled      bool       `json:"enabled"`
	Offline      bool       `json:"offline"`
	OfflineSince *time.Time `json:"offline_since"`
}

func getConnectivity(t *testing.T, h http.Handler) (connectivityBody, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connectivity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/connectivity = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The documented shape exactly: a new field is a decision about what a
	// viewer may read, and must be made in the spec, not slipped in here.
	if len(raw) != 3 {
		t.Fatalf("want exactly enabled, offline, offline_since; got %s", rec.Body.String())
	}
	var body connectivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body, rec
}

func TestConnectivityReportsAnOfflineEpisodeAndItsEnd(t *testing.T) {
	srv, _ := testServerWithDB(t)
	network := &switchableNet{}
	start := time.Date(2026, 9, 29, 3, 12, 0, 0, time.FixedZone("CEST", 2*60*60))
	clock := &steppingClock{t: start}
	canary, err := connectivity.New(connectivity.Options{
		Targets: []string{"gateway.internal:443", "other.internal:22"},
		Dial:    network.dial,
		Now:     clock.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithConnectivity(canary)
	h := authedHandler(srv)

	// Never asked: nothing is known to be wrong, so the host is online.
	body, _ := getConnectivity(t, h)
	if !body.Enabled || body.Offline || body.OfflineSince != nil {
		t.Fatalf("before any round: %+v, want enabled and online", body)
	}

	// Every target fails: the episode starts at the round's time.
	if !canary.Offline(context.Background()) {
		t.Fatal("canary should be offline with every dial failing")
	}
	dials := network.dials.Load()
	clock.advance(10 * time.Minute)
	body, rec := getConnectivity(t, h)
	if !body.Offline || body.OfflineSince == nil || !body.OfflineSince.Equal(start) {
		t.Fatalf("offline episode: %+v, want offline since %s", body, start)
	}
	if body.OfflineSince.Location() != time.UTC {
		t.Fatalf("offline_since %s is not UTC", body.OfflineSince)
	}
	// Readable by every role, so the targets must not be in it: they may
	// name hosts on the operator's own network.
	if strings.Contains(rec.Body.String(), "internal") {
		t.Fatalf("response names a connectivity target: %s", rec.Body.String())
	}
	// Reading the state is not a reason to dial; polling this endpoint must
	// not become the periodic outbound traffic the canary is built to avoid.
	if got := network.dials.Load(); got != dials {
		t.Fatalf("reading the endpoint dialled %d times", got-dials)
	}

	// A target answers again: the episode is over.
	network.up.Store(true)
	canary.Sweep(context.Background())
	body, _ = getConnectivity(t, h)
	if !body.Enabled || body.Offline || body.OfflineSince != nil {
		t.Fatalf("after recovery: %+v, want online with no offline_since", body)
	}
}

func TestConnectivityDisabledIsNotUnknown(t *testing.T) {
	srv, db := testServerWithDB(t)
	// What main passes when --connectivity-check=false.
	srv.WithConnectivity(nil)
	body, _ := getConnectivity(t, authedHandler(srv))
	if body.Enabled || body.Offline || body.OfflineSince != nil {
		t.Fatalf("disabled check: %+v, want enabled=false, offline=false", body)
	}

	// Any role reads it: a viewer's dashboard needs the sentence as much
	// as an administrator's.
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, srv, db, string(role)+"@connectivity.test", role)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/connectivity", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", role, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connectivity", nil))
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "offline") {
		t.Fatalf("anonymous read: %d %s", rec.Code, rec.Body.String())
	}
}

func TestConnectivityUnwiredIsUnavailable(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connectivity", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired source = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
