package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

const (
	apiPin  = "3F:9A:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2"
	apiPin2 = "AA:9A:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2"
)

func decodeMonitor(t *testing.T, body string) monitorResponse {
	t.Helper()
	var m monitorResponse
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode monitor: %v (%s)", err, body)
	}
	return m
}

func TestCreateMonitorStoresATrustedCertificateInOneSpelling(t *testing.T) {
	srv, db := testServerWithDB(t)
	bare := strings.ToLower(strings.ReplaceAll(apiPin, ":", ""))
	before := time.Now().Add(-time.Second)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"pve","type":"http","target":"https://192.0.2.10:8006","trusted_cert_sha256":"`+bare+`"}`)
	mustStatus(t, rec, http.StatusCreated, "create with a pin")
	got := decodeMonitor(t, rec.Body.String())
	if got.TrustedCertSHA256 != apiPin {
		t.Errorf("trusted_cert_sha256 = %q, want %q", got.TrustedCertSHA256, apiPin)
	}
	if got.TrustedCertAt == nil || got.TrustedCertAt.Before(before.Truncate(time.Second)) {
		t.Errorf("trusted_cert_at = %v, want the time of the create", got.TrustedCertAt)
	}
	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TrustedCertSHA256 != apiPin {
		t.Errorf("stored %q", stored.TrustedCertSHA256)
	}

	plain := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", `{"name":"site","type":"http","target":"https://example.com"}`)
	mustStatus(t, plain, http.StatusCreated, "create without a pin")
	if strings.Contains(plain.Body.String(), "trusted_cert") {
		t.Errorf("a monitor without a pin reports one: %s", plain.Body.String())
	}
}

func TestTrustedCertificateIsRefusedWhereNoCheckReadsIt(t *testing.T) {
	for name, body := range map[string]string{
		"tcp":        `{"name":"x","type":"tcp","target":"db.example.com:5432","trusted_cert_sha256":"` + apiPin + `"}`,
		"plain http": `{"name":"x","type":"http","target":"http://example.com","trusted_cert_sha256":"` + apiPin + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "trusted_cert_sha256", "https address")
		})
	}
	t.Run("not a fingerprint", func(t *testing.T) {
		srv, _ := testServerWithDB(t)
		rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
			`{"name":"x","type":"ssl","target":"example.com:443","trusted_cert_sha256":"sha1:abcd"}`)
		wantStatusProblem(t, rec.Code, rec.Body.String(), "trusted_cert_sha256", "64 hex digits")
	})
}

func TestPatchTrustedCertificateKeepsItsTimeUntilItChanges(t *testing.T) {
	srv, db := testServerWithDB(t)
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	m := seedMonitor(t, db, store.Monitor{Name: "nas", Type: "ssl", Target: "nas.example.com:5001", Enabled: true,
		TrustedCertSHA256: apiPin, TrustedCertAt: at})
	path := monitorPath(m.ID)

	got := decodeMonitor(t, patch(t, srv, path, `{"trusted_cert_sha256":"`+strings.ToLower(apiPin)+`"}`).Body.String())
	if got.TrustedCertAt == nil || !got.TrustedCertAt.Equal(at) {
		t.Errorf("the same pin sent again moved trusted_cert_at to %v, want %v", got.TrustedCertAt, at)
	}

	got = decodeMonitor(t, patch(t, srv, path, `{"trusted_cert_sha256":"`+apiPin2+`"}`).Body.String())
	if got.TrustedCertSHA256 != apiPin2 || got.TrustedCertAt == nil || !got.TrustedCertAt.After(at) {
		t.Errorf("new pin: %q at %v, want %q at now", got.TrustedCertSHA256, got.TrustedCertAt, apiPin2)
	}

	rec := patch(t, srv, path, `{"trusted_cert_sha256":""}`)
	mustStatus(t, rec, http.StatusOK, "clear the pin")
	if strings.Contains(rec.Body.String(), "trusted_cert") {
		t.Errorf("a cleared pin is still reported: %s", rec.Body.String())
	}
}

// A monitor that stops being able to use its pin loses it, rather than
// carrying a value nothing reads, or being refused an unrelated edit.
func TestPatchDropsAPinTheMonitorCanNoLongerUse(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "router", Type: "http", Target: "https://192.0.2.1", Enabled: true,
		TrustedCertSHA256: apiPin, TrustedCertAt: time.Now()})

	rec := patch(t, srv, monitorPath(m.ID), `{"target":"http://192.0.2.1"}`)
	mustStatus(t, rec, http.StatusOK, "move to plain http")
	stored, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TrustedCertSHA256 != "" {
		t.Errorf("plain http monitor kept pin %q", stored.TrustedCertSHA256)
	}

	rec = patch(t, srv, monitorPath(m.ID), `{"trusted_cert_sha256":"`+apiPin+`"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "trusted_cert_sha256", "https address")
}

// The preview probes with the pin the form holds, and hands the offered
// certificate back in the shape the form sends as the pin.
func TestPreviewCarriesThePinAndTheOfferedCertificate(t *testing.T) {
	srv, _ := testServerWithDB(t)
	notAfter := time.Date(2027, 1, 9, 12, 0, 0, 0, time.UTC)
	prober := &fakeProber{result: checker.Result{
		OK: false, Kind: checker.FailTLS, Error: "certificate signed by unknown authority", CheckedAt: time.Now(),
		Certificate: &checker.CertificateInfo{Subject: "CN=pve", Issuer: "CN=pve", NotBefore: notAfter.AddDate(-1, 0, 0), NotAfter: notAfter, SHA256: apiPin},
	}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"ssl","target":"pve.example.com:8006","trusted_cert_sha256":"`+apiPin2+`"}`)
	mustStatus(t, rec, http.StatusOK, "preview")
	if got := prober.lastMonitor().TrustedCertSHA256; got != apiPin2 {
		t.Errorf("probed with pin %q, want %q", got, apiPin2)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cert, _ := body["certificate"].(map[string]any)
	if cert["sha256"] != apiPin || cert["subject"] != "CN=pve" || cert["not_after"] != "2027-01-09T12:00:00Z" {
		t.Errorf("certificate = %v, want the offered certificate", body["certificate"])
	}

	rec = preview(t, srv, `{"type":"tcp","target":"db.example.com:5432","trusted_cert_sha256":"`+apiPin+`"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "trusted_cert_sha256", "https address")
}

func TestManualCheckOffersTheCertificate(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "nas", Type: "ssl", Target: "nas.example.com:5001", Enabled: true})
	srv.WithProber(&fakeProber{result: checker.Result{
		Kind: checker.FailTLS, Error: "certificate signed by unknown authority", CheckedAt: time.Now(),
		Certificate: &checker.CertificateInfo{Subject: "CN=nas", Issuer: "CN=nas", SHA256: apiPin},
	}})
	rec := checkNow(t, srv, m.ID)
	mustStatus(t, rec, http.StatusOK, "manual check")
	if !strings.Contains(rec.Body.String(), `"sha256":"`+apiPin+`"`) {
		t.Errorf("manual check does not offer the certificate: %s", rec.Body.String())
	}
}

// Export writes the pin for every monitor that can carry one, "" included,
// so an import of the file restores it, removes it, and leaves an unchanged
// pin — and its time — alone.
func TestConfigFileCarriesThePin(t *testing.T) {
	srv, db := testServerWithDB(t)
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	pinned := seedMonitor(t, db, store.Monitor{Name: "pve", Type: "http", Target: "https://pve.example.com:8006", Enabled: true,
		TrustedCertSHA256: apiPin, TrustedCertAt: at})
	seedMonitor(t, db, store.Monitor{Name: "plain", Type: "http", Target: "http://example.com", Enabled: true})

	doc := exportYAML(t, srv)
	if !strings.Contains(doc, "trusted_cert_sha256: "+apiPin) {
		t.Fatalf("export does not carry the pin:\n%s", doc)
	}
	if strings.Count(doc, "trusted_cert_sha256") != 1 {
		t.Errorf("export writes a pin on a plain http monitor:\n%s", doc)
	}

	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("re-import: %d %s", code, body)
	}
	for _, item := range rep.Monitors {
		if item.Action != actionUnchanged {
			t.Errorf("re-import of its own export: %s %s %v", item.Name, item.Action, item.Changes)
		}
	}
	if got, _ := db.GetMonitor(t.Context(), pinned.ID); !got.TrustedCertAt.Equal(at) {
		t.Errorf("re-import moved trusted_cert_at to %v", got.TrustedCertAt)
	}

	code, rep, body = importYAML(t, srv, strings.Replace(doc, "trusted_cert_sha256: "+apiPin, `trusted_cert_sha256: ""`, 1), false)
	if code != http.StatusOK {
		t.Fatalf("import removing the pin: %d %s", code, body)
	}
	if got, _ := db.GetMonitor(t.Context(), pinned.ID); got.TrustedCertSHA256 != "" {
		t.Errorf("import of trusted_cert_sha256: \"\" kept %q", got.TrustedCertSHA256)
	}
	changed := false
	for _, item := range rep.Monitors {
		if item.Name == "pve" && strings.Contains(strings.Join(item.Changes, ","), "trusted_cert_sha256") {
			changed = true
		}
	}
	if !changed {
		t.Errorf("import report does not name the removed pin: %+v", rep.Monitors)
	}
}
