package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// These pin the API contract of a dns monitor: the dns object through
// create, detail read, PATCH, preview and the configuration file, and field
// problems that name the sub-field at fault. The query and the comparison
// are tested in internal/checker.

const dnsMonitor = `{"name":"mail","type":"dns","target":"example.com",` +
	`"dns":{"record_type":"MX","expected":["10 mx1.example.com"," 20 mx2.example.com "],"resolver":"1.1.1.1"}}`

func detailDNS(t *testing.T, srv *Server, id int64) map[string]any {
	t.Helper()
	rec := getMonitorRaw(t, srv, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	d, _ := got["dns"].(map[string]any)
	return d
}

func TestCreateDNSMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors", dnsMonitor)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	id := createdID(t, rec.Body.Bytes())
	stored, err := db.GetMonitor(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := store.DNSCheck{RecordType: "MX", Expected: []string{"10 mx1.example.com", "20 mx2.example.com"}, Resolver: "1.1.1.1"}
	if stored.DNS == nil || !reflect.DeepEqual(*stored.DNS, want) {
		t.Fatalf("stored %+v, want %+v (values trimmed)", stored.DNS, want)
	}
	got := detailDNS(t, srv, id)
	if got["record_type"] != "MX" || got["resolver"] != "1.1.1.1" || len(got["expected"].([]any)) != 2 {
		t.Errorf("detail dns = %v", got)
	}
}

func TestDNSMonitorWithoutValuesReadsAnEmptyList(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"A"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := detailDNS(t, srv, createdID(t, rec.Body.Bytes()))
	if v, ok := got["expected"].([]any); !ok || len(v) != 0 {
		t.Errorf("expected = %#v, want []", got["expected"])
	}
	if _, present := got["resolver"]; present {
		t.Errorf("resolver present for the system resolver: %v", got)
	}
}

func TestOtherMonitorsHaveNoDNSKey(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})
	if got := detailDNS(t, srv, m.ID); got != nil {
		t.Errorf("http monitor has dns settings %v", got)
	}
}

func TestCreateDNSMonitorRefusals(t *testing.T) {
	tests := []struct {
		name, body, field string
	}{
		{"no dns object", `{"name":"a","type":"dns","target":"example.com"}`, "dns.record_type"},
		{"unknown record type", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"SOA"}}`, "dns.record_type"},
		{"lower-case record type", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"a"}}`, "dns.record_type"},
		{"value of the wrong shape", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"A","expected":["2001:db8::1"]}}`, "dns.expected"},
		{"value listed twice", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"CNAME","expected":["Shop.example.com","shop.example.com."]}}`, "dns.expected"},
		{"resolver as a URL", `{"name":"a","type":"dns","target":"example.com","dns":{"record_type":"A","resolver":"https://dns.example/dns-query"}}`, "dns.resolver"},
		{"dns settings on an http monitor", `{"name":"a","type":"http","target":"https://example.com","dns":{"record_type":"A"}}`, "dns"},
		{"target is a URL", `{"name":"a","type":"dns","target":"https://example.com/","dns":{"record_type":"A"}}`, "target"},
		{"target has a port", `{"name":"a","type":"dns","target":"example.com:53","dns":{"record_type":"A"}}`, "target"},
		{"target is an address", `{"name":"a","type":"dns","target":"192.0.2.1","dns":{"record_type":"A"}}`, "target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := post(t, srv, "/api/v1/monitors", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			assertProblemField(t, rec, tt.field)
		})
	}
}

func TestDNSTargetRefusalSaysWhatToType(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", `{"name":"a","type":"dns","target":"https://www.example.com/x","dns":{"record_type":"A"}}`)
	if !strings.Contains(rec.Body.String(), "use www.example.com") {
		t.Errorf("refusal does not name the host to use: %s", rec.Body.String())
	}
}

func TestPatchDNSMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := post(t, srv, "/api/v1/monitors", dnsMonitor)
	id := createdID(t, rec.Body.Bytes())

	if rec := patch(t, srv, monitorPath(id), `{"name":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if m, _ := db.GetMonitor(t.Context(), id); m.DNS == nil || m.DNS.RecordType != "MX" {
		t.Fatalf("a rename changed the dns settings to %+v", m.DNS)
	}

	rec = patch(t, srv, monitorPath(id), `{"dns":{"record_type":"TXT","expected":["v=spf1 -all"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body.String())
	}
	if m, _ := db.GetMonitor(t.Context(), id); m.DNS == nil || m.DNS.RecordType != "TXT" || m.DNS.Resolver != "" {
		t.Fatalf("replace stored %+v; the object replaces the settings as a whole", m.DNS)
	}

	rec = patch(t, srv, monitorPath(id), `{"dns":{"record_type":"A","expected":["not-an-address"]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad value: %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "dns.expected")

	// Away from dns: the settings go with the type.
	rec = patch(t, srv, monitorPath(id), `{"type":"ping","target":"example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("type change away: %d %s", rec.Code, rec.Body.String())
	}
	if m, _ := db.GetMonitor(t.Context(), id); m.DNS != nil {
		t.Fatalf("a ping monitor kept dns settings %+v", m.DNS)
	}

	// Back to dns: the settings have to come along.
	rec = patch(t, srv, monitorPath(id), `{"type":"dns"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("type change to dns without settings: %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "dns.record_type")
	rec = patch(t, srv, monitorPath(id), `{"type":"dns","dns":{"record_type":"AAAA"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("type change to dns: %d %s", rec.Code, rec.Body.String())
	}

	// Settings sent to a monitor of another type are refused.
	other := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})
	rec = patch(t, srv, monitorPath(other.ID), `{"dns":{"record_type":"A"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dns on http: %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "dns")
}

func TestPreviewCarriesDNSSettings(t *testing.T) {
	srv, _ := testServerWithDB(t)
	prober := &fakeProber{result: checker.Result{OK: false, Kind: checker.FailDNSMismatch,
		Error: "A records for example.com: expected 192.0.2.1, got 192.0.2.9"}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"dns","target":"example.com","dns":{"record_type":"A","expected":["192.0.2.1"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodePreview(t, rec); got.Kind != "dns_mismatch" || got.Type != "dns" {
		t.Errorf("kind = %q type = %q", got.Kind, got.Type)
	}
	sent := prober.lastMonitor().DNS
	if sent == nil || sent.RecordType != "A" || len(sent.Expected) != 1 {
		t.Errorf("prober got %+v", sent)
	}

	rec = preview(t, srv, `{"type":"dns","target":"example.com"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preview without settings: %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "dns.record_type")
}

func TestConfigFileCarriesDNSSettings(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	if _, err := srcDB.CreateMonitor(context.Background(), store.Monitor{Name: "Mail", Type: store.TypeDNS,
		Target: "example.com", Enabled: true,
		DNS: &store.DNSCheck{RecordType: "MX", Expected: []string{"10 mx1.example.com"}, Resolver: "1.1.1.1"}}); err != nil {
		t.Fatal(err)
	}
	doc := exportYAML(t, src)
	for _, want := range []string{"dns:", "record_type: MX", "- 10 mx1.example.com", "resolver: 1.1.1.1"} {
		if !strings.Contains(doc, want) {
			t.Errorf("export lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "ssl_warn_days") || strings.Contains(doc, "method:") {
		t.Errorf("export writes http settings for a dns monitor:\n%s", doc)
	}

	dst, dstDB := testServerWithDB(t)
	code, _, body := importYAML(t, dst, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	mons, err := dstDB.ListMonitors(context.Background())
	must(t, err)
	if len(mons) != 1 || mons[0].DNS == nil || mons[0].DNS.Resolver != "1.1.1.1" || mons[0].DNS.Expected[0] != "10 mx1.example.com" {
		t.Fatalf("imported %+v", mons)
	}

	// Importing the same file again changes nothing.
	code, rep, body := importYAML(t, dst, doc, true)
	if code != http.StatusOK || rep.Summary.Unchanged != 1 {
		t.Fatalf("re-import = %d %+v %s", code, rep.Summary, body)
	}

	// A changed value is an update named by the field.
	changed := strings.Replace(doc, "- 10 mx1.example.com", "- 20 mx1.example.com", 1)
	code, rep, body = importYAML(t, dst, changed, true)
	if code != http.StatusOK || len(rep.Monitors) != 1 || strings.Join(rep.Monitors[0].Changes, ",") != "dns" {
		t.Fatalf("changed value = %d %+v %s", code, rep.Monitors, body)
	}

	bad := strings.Replace(doc, "record_type: MX", "record_type: SOA", 1)
	code, _, body = importYAML(t, dst, bad, true)
	if code != http.StatusBadRequest || !strings.Contains(body, `"field":"monitors[0].dns.record_type"`) {
		t.Errorf("bad record type = %d %s, want 400 at monitors[0].dns.record_type", code, body)
	}
}
