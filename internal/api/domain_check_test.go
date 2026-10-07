package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// These pin the API contract of a domain monitor: domain_warn_days through
// create, detail read, PATCH, preview and the configuration file, the
// six-hour interval floor, and the "unknown" status of a monitor whose last
// check could not read the date. Reading the date is tested in
// internal/checker.

func detailDomainWarnDays(t *testing.T, srv *Server, id int64) (int, bool) {
	t.Helper()
	rec := getMonitorRaw(t, srv, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	v, ok := got["domain_warn_days"].(float64)
	return int(v), ok
}

func TestCreateDomainMonitorDefaults(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors", `{"name":"reg","type":"domain","target":"www.example.co.uk"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	id := createdID(t, rec.Body.Bytes())
	m, err := db.GetMonitor(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if m.IntervalS != store.DefaultDomainIntervalS || m.DomainWarnDays != checker.DefaultDomainWarnDays || m.Retries != 0 {
		t.Errorf("stored interval %d, warn days %d, retries %d; want a day, %d and 0",
			m.IntervalS, m.DomainWarnDays, m.Retries, checker.DefaultDomainWarnDays)
	}
	if got, ok := detailDomainWarnDays(t, srv, id); !ok || got != checker.DefaultDomainWarnDays {
		t.Errorf("detail domain_warn_days = %d (present %v)", got, ok)
	}

	// Zero is a setting, read back as 0 rather than left out.
	rec = post(t, srv, "/api/v1/monitors", `{"name":"quiet","type":"domain","target":"example.org","domain_warn_days":0,"retries":2,"interval_s":21600}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	id = createdID(t, rec.Body.Bytes())
	if got, ok := detailDomainWarnDays(t, srv, id); !ok || got != 0 {
		t.Errorf("detail domain_warn_days = %d (present %v), want 0", got, ok)
	}
	if m, _ := db.GetMonitor(context.Background(), id); m.Retries != 2 || m.IntervalS != 21600 {
		t.Errorf("stored retries %d interval %d, want what was sent", m.Retries, m.IntervalS)
	}
}

func TestCreateDomainMonitorRefusals(t *testing.T) {
	srv, _ := testServerWithDB(t)
	for _, tc := range []struct{ body, field, says string }{
		{`{"name":"a","type":"domain","target":"example.com","interval_s":3600}`, "interval_s", "every 6 hours"},
		{`{"name":"a","type":"domain","target":"example.com","domain_warn_days":400}`, "domain_warn_days", "between 0 and 365"},
		{`{"name":"a","type":"domain","target":"https://example.com/x"}`, "target", "use example.com"},
		{`{"name":"a","type":"domain","target":"example.com:443"}`, "target", "use example.com"},
		{`{"name":"a","type":"domain","target":"co.uk"}`, "target", "public suffix"},
		{`{"name":"a","type":"domain","target":"192.0.2.1"}`, "target", "not an IP address"},
		{`{"name":"a","type":"http","target":"https://example.com","domain_warn_days":30}`, "domain_warn_days", "only to domain monitors"},
	} {
		rec := post(t, srv, "/api/v1/monitors", tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.body, rec.Code)
			continue
		}
		assertProblemField(t, rec, tc.field)
		if !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %s, want it to say %q", tc.body, rec.Body.String(), tc.says)
		}
	}
}

func TestPatchDomainMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "reg", Type: store.TypeDomain, Target: "example.com",
		IntervalS: 86400, Enabled: true, DomainWarnDays: 30})

	if rec := patch(t, srv, monitorPath(m.ID), `{"domain_warn_days":60}`); rec.Code != http.StatusOK {
		t.Fatalf("patch = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := db.GetMonitor(context.Background(), m.ID); got.DomainWarnDays != 60 {
		t.Errorf("warn days = %d, want 60", got.DomainWarnDays)
	}
	rec := patch(t, srv, monitorPath(m.ID), `{"interval_s":600}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("interval under the floor = %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "interval_s")

	// Leaving the type drops the setting; another type refuses it.
	if rec := patch(t, srv, monitorPath(m.ID), `{"type":"dns","dns":{"record_type":"A"},"interval_s":300}`); rec.Code != http.StatusOK {
		t.Fatalf("to dns = %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := detailDomainWarnDays(t, srv, m.ID); ok {
		t.Error("a dns monitor still reads domain_warn_days")
	}
	rec = patch(t, srv, monitorPath(m.ID), `{"domain_warn_days":10}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("warn days on dns = %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "domain_warn_days")

	// Becoming a domain monitor brings the default threshold, and has to
	// bring an interval it is allowed to have.
	rec = patch(t, srv, monitorPath(m.ID), `{"type":"domain"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("to domain at 300 s = %d, want 400", rec.Code)
	}
	assertProblemField(t, rec, "interval_s")
	if rec := patch(t, srv, monitorPath(m.ID), `{"type":"domain","interval_s":43200}`); rec.Code != http.StatusOK {
		t.Fatalf("to domain = %d: %s", rec.Code, rec.Body.String())
	}
	if got, ok := detailDomainWarnDays(t, srv, m.ID); !ok || got != checker.DefaultDomainWarnDays {
		t.Errorf("warn days after becoming a domain monitor = %d (present %v)", got, ok)
	}
}

func TestPreviewCarriesDomainSettings(t *testing.T) {
	srv, _ := testServerWithDB(t)
	expiry := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	prober := &fakeProber{result: checker.Result{OK: true, Expiring: true, Kind: checker.FailDomainExpiry,
		Error: "domain registration of example.com expires in 12 days (on 2026-10-19)", DomainExpiry: expiry}}
	srv.WithProber(prober)

	rec := preview(t, srv, `{"type":"domain","target":"example.com","domain_warn_days":20}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodePreview(t, rec)
	if got.Type != "domain" || got.Kind != "domain_expiry" || got.DomainExpiry == nil || !got.DomainExpiry.Equal(expiry) {
		t.Errorf("preview = %+v", got)
	}
	if sent := prober.lastMonitor(); sent.DomainWarnDays != 20 || sent.Type != store.TypeDomain {
		t.Errorf("prober got %+v", sent)
	}

	// A second server: previews are rate-limited per user.
	srv, _ = testServerWithDB(t)
	srv.WithProber(prober)
	rec = preview(t, srv, `{"type":"domain","target":"example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if sent := prober.lastMonitor(); sent.DomainWarnDays != checker.DefaultDomainWarnDays {
		t.Errorf("preview without a threshold checked with %d, want the default", sent.DomainWarnDays)
	}
}

// A domain monitor whose latest check could not read the date says so, with
// the reason, until a check reads it. An open notice wins over it.
func TestDomainMonitorReadsUnknown(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "reg", Type: store.TypeDomain, Target: "example.nl",
		IntervalS: 86400, Enabled: true, DomainWarnDays: 30})
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := db.RecordUnknownCheck(ctx, store.UnknownCheck{MonitorID: m.ID, At: at,
		Reason: "the .nl registry publishes no RDAP service"}); err != nil {
		t.Fatal(err)
	}

	got := getMonitor(t, srv, m.ID)
	if got.Status != "unknown" || got.Error != "the .nl registry publishes no RDAP service" ||
		got.FailureKind != "unknown" || got.LastCheck == nil || !got.LastCheck.Equal(at) {
		t.Errorf("monitor = %+v, want unknown with its reason", got)
	}

	if _, err := db.OpenNotice(ctx, m.ID, at, string(checker.FailDomainExpiry), "expires in 12 days"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordHeartbeat(ctx, store.Heartbeat{MonitorID: m.ID, TS: at.Add(-time.Hour), OK: true, Assessment: "up"}); err != nil {
		t.Fatal(err)
	}
	if got := getMonitor(t, srv, m.ID); got.Status != "expiring" {
		t.Errorf("status with an open notice = %q, want expiring", got.Status)
	}

	// Another type never reads one.
	other := seedMonitor(t, db, store.Monitor{Name: "api", Type: "http", Target: "https://example.com", Enabled: true})
	if err := db.RecordUnknownCheck(ctx, store.UnknownCheck{MonitorID: other.ID, At: at, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := getMonitor(t, srv, other.ID); got.Status == "unknown" {
		t.Error("an http monitor reads unknown")
	}
}

func TestConfigFileCarriesDomainSettings(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	if _, err := srcDB.CreateMonitor(context.Background(), store.Monitor{Name: "Registration", Type: store.TypeDomain,
		Target: "example.com", Enabled: true, DomainWarnDays: 45}); err != nil {
		t.Fatal(err)
	}
	doc := exportYAML(t, src)
	for _, want := range []string{"type: domain", "domain_warn_days: 45", "interval_s: 86400"} {
		if !strings.Contains(doc, want) {
			t.Errorf("export lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "ssl_warn_days") || strings.Contains(doc, "method:") {
		t.Errorf("export writes http settings for a domain monitor:\n%s", doc)
	}

	dst, dstDB := testServerWithDB(t)
	code, _, body := importYAML(t, dst, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	mons, err := dstDB.ListMonitors(context.Background())
	must(t, err)
	if len(mons) != 1 || mons[0].Type != store.TypeDomain || mons[0].DomainWarnDays != 45 {
		t.Fatalf("imported %+v", mons)
	}

	code, rep, body := importYAML(t, dst, doc, true)
	if code != http.StatusOK || rep.Summary.Unchanged != 1 {
		t.Fatalf("re-import = %d %+v %s", code, rep.Summary, body)
	}
	changed := strings.Replace(doc, "domain_warn_days: 45", "domain_warn_days: 10", 1)
	code, rep, body = importYAML(t, dst, changed, true)
	if code != http.StatusOK || len(rep.Monitors) != 1 || strings.Join(rep.Monitors[0].Changes, ",") != "domain_warn_days" {
		t.Fatalf("changed value = %d %+v %s", code, rep.Monitors, body)
	}
	fast := strings.Replace(doc, "interval_s: 86400", "interval_s: 60", 1)
	code, _, body = importYAML(t, dst, fast, true)
	if code != http.StatusBadRequest || !strings.Contains(body, `"field":"monitors[0].interval_s"`) {
		t.Errorf("fast domain monitor = %d %s, want 400 at monitors[0].interval_s", code, body)
	}

	// A file written by hand that leaves the threshold out gets the default.
	bare := "version: 1\nmonitors:\n  - key: reg\n    name: Reg\n    type: domain\n    target: example.org\n"
	other, otherDB := testServerWithDB(t)
	if code, _, body := importYAML(t, other, bare, false); code != http.StatusOK {
		t.Fatalf("bare import = %d: %s", code, body)
	}
	mons, err = otherDB.ListMonitors(context.Background())
	must(t, err)
	if len(mons) != 1 || mons[0].DomainWarnDays != checker.DefaultDomainWarnDays || mons[0].IntervalS != store.DefaultDomainIntervalS || mons[0].Retries != 0 {
		t.Fatalf("bare import = %+v", mons)
	}
}
