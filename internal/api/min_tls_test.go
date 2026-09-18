package api

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// SUB-110 gave the checkers a per-monitor TLS floor and nothing that could set
// it. These tests are about the connection between the two halves: what the API
// accepts, what the column holds, and — the one that matters — that the value
// survives the whole way to the tls.Config a check dials with.

func TestCreateMonitorStoresMinTLSVersion(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors", `{
		"name": "appliance",
		"type": "http",
		"target": "https://appliance.example.com",
		"min_tls_version": "1.0"
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var created monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.MinTLSVersion != "1.0" {
		t.Errorf("response says min_tls_version %q, want \"1.0\"", created.MinTLSVersion)
	}

	stored, err := db.GetMonitor(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	// The column holds the crypto/tls constant, which is what goes to
	// tls.Config.MinVersion; the label is an API spelling, not a storage one.
	if stored.MinTLSVersion != tls.VersionTLS10 {
		t.Errorf("stored MinTLSVersion = %d, want %d", stored.MinTLSVersion, tls.VersionTLS10)
	}
}

// A monitor that never mentioned TLS must come back with no opinion rather
// than with the current default. The distinction is invisible today and stops
// being invisible the day the default moves.
func TestCreateMonitorWithoutMinTLSVersionHasNoOpinion(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"plain","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.MinTLSVersion != "" {
		t.Errorf("min_tls_version = %q on a monitor that never set one", created.MinTLSVersion)
	}
	if strings.Contains(rec.Body.String(), "min_tls_version") {
		t.Errorf("the field is present on a monitor with no opinion: %s", rec.Body.String())
	}

	stored, err := db.GetMonitor(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.MinTLSVersion != 0 {
		t.Errorf("stored MinTLSVersion = %d, want 0 (unset)", stored.MinTLSVersion)
	}
}

func TestCreateMonitorRejectsUnknownMinTLSVersion(t *testing.T) {
	srv, _ := testServerWithDB(t)

	for _, label := range []string{
		"TLSv1.2", // the OpenSSL spelling
		"771",     // the constant, which is what the column holds
		"1.4",     // does not exist
		"",        // computed to nothing; say so rather than storing a floor
		"1.2 ",
	} {
		rec := post(t, srv, "/api/v1/monitors",
			`{"name":"x","type":"http","target":"https://example.com","min_tls_version":`+
				strconv.Quote(label)+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("min_tls_version %q: status = %d, want 400: %s",
				label, rec.Code, rec.Body.String())
			continue
		}
		// The message has to name what would have worked; "invalid" leaves
		// the caller guessing between three plausible spellings.
		body := rec.Body.String()
		if !strings.Contains(body, "min_tls_version") || !strings.Contains(body, "1.3") {
			t.Errorf("min_tls_version %q: unhelpful rejection: %s", label, body)
		}
	}
}

func TestPatchMonitorSetsAndClearsMinTLSVersion(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "appliance", Type: "http", Target: "https://appliance.example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
	})

	rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10), `{"min_tls_version":"1.3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.MinTLSVersion != tls.VersionTLS13 {
		t.Fatalf("stored MinTLSVersion = %d, want %d", stored.MinTLSVersion, tls.VersionTLS13)
	}

	// The empty string is how a floor set earlier is taken back off. It is
	// rejected on create, where it can only be a client that computed
	// nothing, and accepted here, where it says something a PATCH otherwise
	// has no way to express.
	rec = patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10), `{"min_tls_version":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, err = db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.MinTLSVersion != 0 {
		t.Errorf("after clearing, stored MinTLSVersion = %d, want 0", stored.MinTLSVersion)
	}
}

func TestPatchMonitorRejectsUnknownMinTLSVersion(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "x", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true,
		MinTLSVersion: tls.VersionTLS12,
	})

	rec := patch(t, srv, "/api/v1/monitors/"+strconv.FormatInt(m.ID, 10), `{"min_tls_version":"1.4"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	// A rejected patch must not have written anything.
	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.MinTLSVersion != tls.VersionTLS12 {
		t.Errorf("a rejected patch changed the stored floor to %d", stored.MinTLSVersion)
	}
}

// A preview exists to tell you whether a monitor will work before you save
// it, so the floor has to reach the probe. It is the setting most likely to
// be the reason an old endpoint cannot be reached, and a preview that dialled
// with the default while the saved monitor dialled with TLS 1.0 would be
// answering about a differently-configured probe.
func TestPreviewCheckDialsWithTheRequestedTLSFloor(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{
		OK: true, StatusCode: 200, Latency: 5 * time.Millisecond,
		CheckedAt: time.Now().UTC(),
	}}
	srv.WithProber(prober)

	rec := preview(t, srv,
		`{"type":"http","target":"https://appliance.example.com","min_tls_version":"1.0"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	prober.mu.Lock()
	defer prober.mu.Unlock()
	if len(prober.calls) != 1 {
		t.Fatalf("prober called %d times, want 1", len(prober.calls))
	}
	if got := prober.calls[0].MinTLSVersion; got != tls.VersionTLS10 {
		t.Errorf("probed with MinTLSVersion %d, want %d", got, tls.VersionTLS10)
	}
}

// And a floor the save would reject must be rejected here too, or a preview
// passes and the create that follows it fails — the one outcome this endpoint
// exists to prevent.
func TestPreviewCheckRejectsUnknownMinTLSVersion(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true, CheckedAt: time.Now().UTC()}}
	srv.WithProber(prober)

	rec := preview(t, srv,
		`{"type":"http","target":"https://example.com","min_tls_version":"TLSv1.2"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "min_tls_version") {
		t.Errorf("rejection does not name the field: %s", rec.Body.String())
	}
	if prober.callCount() != 0 {
		t.Errorf("a rejected preview still made an outbound request")
	}
}
