package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
)

// A dns monitor's resolver given by a name the lookup refuses without asking
// a server (two dots in a row, digits and dots that are not an address) used
// to be saved, and every check after that failed as "no answer from ...":
// a resolver outage reported for what was a typo. Each way the setting
// reaches the store refuses it now, on dns.resolver.

func TestPatchDNSMonitorRefusesAnUnresolvableResolver(t *testing.T) {
	srv, db := testServerWithDB(t)
	id := createdID(t, post(t, srv, "/api/v1/monitors", dnsMonitor).Body.Bytes())
	path := "/api/v1/monitors/" + strconv.FormatInt(id, 10)

	rec := patch(t, srv, path, `{"dns":{"record_type":"MX","resolver":"ns1..example.com"}}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "dns.resolver", "empty label")
	m, err := db.GetMonitor(context.Background(), id)
	must(t, err)
	if m.DNS == nil || m.DNS.Resolver != "1.1.1.1" {
		t.Errorf("a refused patch changed the dns settings to %+v", m.DNS)
	}
}

func TestPreviewRefusesAnUnresolvableResolver(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: true}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"dns","target":"example.com","dns":{"record_type":"A","resolver":"-ns1.example.com:53"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"dns.resolver"`) {
		t.Fatalf("status = %d (body %s), want 400 on dns.resolver", rec.Code, rec.Body.String())
	}
	if prober.callCount() != 0 {
		t.Errorf("prober called %d times for a refused resolver", prober.callCount())
	}
}

func TestImportRefusesAnUnresolvableResolver(t *testing.T) {
	srv, _ := testServerWithDB(t)
	doc := "version: 1\nmonitors:\n  - key: a\n    name: A\n    type: dns\n    target: example.com\n" +
		"    dns:\n      record_type: A\n      resolver: 999.1.1.1\n"
	code, _, body := importYAML(t, srv, doc, true)
	if code != http.StatusBadRequest || !strings.Contains(body, "dns.resolver") {
		t.Errorf("import = %d %s, want 400 on the resolver", code, body)
	}
}
