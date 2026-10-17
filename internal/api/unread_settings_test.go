package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// A setting is stored and shown on a monitor whose check never reads it: a
// keyword on a tcp monitor passes whatever the service says. Each way a
// monitor gets one refuses it, on the field, with the type that reads it.

// unreadOn is one setting a type never reads, as JSON fields of a monitor.
type unreadOn struct {
	typ, target, fields, field, readBy string
}

// unreadSettings covers every judged field on at least one type that does not
// read it, and the TLS pair on the types that read the HTTP ones neither.
var unreadSettings = map[string]unreadOn{
	"tcp keyword":         {"tcp", "example.com:443", `"keyword":"ok","keyword_mode":"must_contain"`, "keyword", "an http check"},
	"tcp forbidden word":  {"tcp", "example.com:443", `"keyword":"error","keyword_mode":"must_not_contain"`, "keyword", "an http check"},
	"ssl keyword":         {"ssl", "example.com:443", `"keyword":"ok","keyword_mode":"must_contain"`, "keyword", "an http check"},
	"tcp headers":         {"tcp", "example.com:443", `"headers":{"X-Token":"t"}`, "headers", "an http check"},
	"ping body":           {"ping", "example.com", `"body":"{}"`, "body", "an http check"},
	"tcp method":          {"tcp", "example.com:443", `"method":"POST"`, "method", "an http check"},
	"ssl expected status": {"ssl", "example.com:443", `"expected_status":"404"`, "expected_status", "an http check"},
	"ping tls floor":      {"ping", "example.com", `"min_tls_version":"1.3"`, "min_tls_version", "an http or ssl check"},
	"tcp warning days":    {"tcp", "example.com:443", `"ssl_warn_days":30`, "ssl_warn_days", "an http or ssl check"},
	"dns tls floor": {"dns", "example.com", `"dns":{"record_type":"A"},"min_tls_version":"1.2"`,
		"min_tls_version", "an http or ssl check"},
}

func TestCreateMonitorRefusesASettingItsCheckNeverReads(t *testing.T) {
	for name, tc := range unreadSettings {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"A","type":"`+tc.typ+`","target":"`+tc.target+`",`+tc.fields+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.field+" is read only by "+tc.readBy)
			if !strings.Contains(rec.Body.String(), "this "+tc.typ+" monitor never uses it") {
				t.Errorf("refusal does not name the type: %s", rec.Body.String())
			}
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

// A value that changes nothing is accepted on every type: a client that sends
// each field with its default, or empty, gets the monitor it asked for.
func TestCreateMonitorAcceptsTheDefaultOfASettingItsCheckNeverReads(t *testing.T) {
	for name, fields := range map[string]string{
		"defaults spelled out": `"method":"get","expected_status":"200-299","keyword_mode":"absent_ok",` +
			`"follow_redirects":true,"ssl_warn_days":14,"capture_response":true`,
		"empty values":      `"method":"","expected_status":"","keyword":"","headers":{},"body":""`,
		"word left ignore":  `"keyword":"ok","keyword_mode":"absent_ok"`,
		"mode without word": `"keyword_mode":"must_contain"`,
	} {
		for _, typ := range []string{"tcp", "ping"} {
			t.Run(name+"/"+typ, func(t *testing.T) {
				srv, _ := testServerWithDB(t)
				target := "example.com"
				if typ == "tcp" {
					target = "example.com:443"
				}
				rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
					`{"name":"A","type":"`+typ+`","target":"`+target+`",`+fields+`}`)
				if rec.Code != http.StatusCreated {
					t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// The types that read a setting keep it.
func TestCreateMonitorAcceptsASettingOnATypeThatReadsIt(t *testing.T) {
	for name, body := range map[string]string{
		"http request settings": `"type":"http","target":"https://example.com","method":"POST","expected_status":"404",` +
			`"keyword":"ok","keyword_mode":"must_contain","headers":{"X-Token":"t"},"body":"{}"`,
		"http tls": `"type":"http","target":"https://example.com","min_tls_version":"1.3","ssl_warn_days":30`,
		"ssl tls":  `"type":"ssl","target":"example.com:443","min_tls_version":"1.3","ssl_warn_days":30`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", `{"name":"A",`+body+`}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A preview judges the type it resolved, before the prober runs: an address
// worked out as tcp is refused a keyword here, not first on save.
func TestPreviewRefusesASettingItsCheckNeverReads(t *testing.T) {
	cases := map[string]struct{ body, field string }{
		"inferred tcp keyword": {`{"target":"db.example.com:5432","keyword":"ok","keyword_mode":"must_contain"}`, "keyword"},
		"ping tls floor":       {`{"type":"ping","target":"example.com","min_tls_version":"1.3"}`, "min_tls_version"},
		"tcp headers":          {`{"type":"tcp","target":"example.com:443","headers":{"X-Token":"t"}}`, "headers"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			prober := &fakeProber{result: checker.Result{OK: true}}
			srv.WithProber(prober)
			rec := preview(t, srv, tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, "never uses it")
			if prober.callCount() != 0 {
				t.Errorf("prober ran %d times, want 0", prober.callCount())
			}
		})
	}
	srv, _ := testServerWithDB(t)
	srv.WithProber(&fakeProber{result: checker.Result{OK: true}})
	if rec := preview(t, srv, `{"target":"https://example.com","keyword":"ok","keyword_mode":"must_contain"}`); rec.Code != http.StatusOK {
		t.Fatalf("http keyword preview: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// tcpMonitor is a tcp monitor as the store holds one with no settings of its
// own, plus over.
func tcpMonitor(over func(*store.Monitor)) store.Monitor {
	m := store.Monitor{Name: "DB", Type: "tcp", Target: "db.example.com:5432", IntervalS: 60, TimeoutS: 10, Enabled: true}
	store.ApplyMonitorDefaults(&m)
	if over != nil {
		over(&m)
	}
	return m
}

func TestPatchMonitorRefusesANewSettingItsCheckNeverReads(t *testing.T) {
	cases := map[string]struct{ body, field string }{
		"keyword":          {`{"keyword":"ok","keyword_mode":"must_contain"}`, "keyword"},
		"mode onto a word": {`{"keyword_mode":"must_contain"}`, "keyword"},
		"headers":          {`{"headers":{"X-Token":"t"}}`, "headers"},
		"body":             {`{"body":"{}"}`, "body"},
		"method":           {`{"method":"POST"}`, "method"},
		"expected status":  {`{"expected_status":"404"}`, "expected_status"},
		"tls floor":        {`{"min_tls_version":"1.3"}`, "min_tls_version"},
		"warning days":     {`{"ssl_warn_days":30}`, "ssl_warn_days"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, tcpMonitor(func(m *store.Monitor) { m.Keyword = "ok" }))
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, "this tcp monitor never uses it")
			got, err := db.GetMonitor(context.Background(), m.ID)
			must(t, err)
			if got.KeywordMode != m.KeywordMode || len(got.Headers) != 0 || got.Method != m.Method ||
				got.MinTLSVersion != 0 || got.SSLWarnDays != m.SSLWarnDays {
				t.Errorf("a refused patch changed the monitor: %+v", got)
			}
		})
	}
}

// legacyTCP is a tcp monitor saved before the rule, holding a setting of
// every judged kind.
func legacyTCP(m *store.Monitor) {
	m.Keyword, m.KeywordMode = "ok", "must_contain"
	m.Headers = map[string]string{"X-Token": "t"}
	m.Body, m.Method, m.ExpectedStatus = "{}", "POST", "404"
	m.SSLWarnDays, m.MinTLSVersion = 30, 0x0304
}

// A monitor saved before the rule keeps working: an edit that leaves the
// settings out, or sends back what is stored, is not refused for them.
func TestPatchMonitorKeepsAStoredSettingItsCheckNeverReads(t *testing.T) {
	for name, body := range map[string]string{
		"rename and pause": `{"name":"DB v2","enabled":false}`,
		"stored values sent back": `{"keyword":"ok","keyword_mode":"must_contain","headers":{"X-Token":"t"},"body":"{}",` +
			`"method":"post","expected_status":"404","ssl_warn_days":30,"min_tls_version":"1.3"}`,
		"cleared":  `{"keyword":"","headers":{},"body":"","method":"GET","expected_status":"200-299","min_tls_version":""}`,
		"retarget": `{"target":"db2.example.com:5432"}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, tcpMonitor(legacyTCP))
			rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A new type that does not read a setting drops it, as a change away from dns
// drops the dns settings: an http monitor's headers, often a credential, are
// not kept on a tcp monitor that never sends them.
func TestPatchMonitorTypeChangeDropsTheSettingsTheNewTypeNeverReads(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, Method: "POST", ExpectedStatus: "404",
		Keyword: "ok", KeywordMode: "must_contain", Headers: map[string]string{"Authorization": "Bearer t"},
		Body: "{}", SSLWarnDays: 30, MinTLSVersion: 0x0304})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"type":"tcp","target":"example.com:443"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	want := tcpMonitor(nil)
	if got.Method != want.Method || got.ExpectedStatus != want.ExpectedStatus || got.Keyword != "" ||
		got.KeywordMode != want.KeywordMode || len(got.Headers) != 0 || got.Body != "" ||
		got.SSLWarnDays != want.SSLWarnDays || got.MinTLSVersion != 0 {
		t.Errorf("tcp monitor kept http settings: %+v", got)
	}

	// To ssl, the TLS pair is read and kept; the request settings are not.
	m = seedMonitor(t, db, store.Monitor{Name: "Cert", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, Keyword: "ok", KeywordMode: "must_contain",
		SSLWarnDays: 30, MinTLSVersion: 0x0304})
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"type":"ssl","target":"example.com:443"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("to ssl: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, err = db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Keyword != "" || got.SSLWarnDays != 30 || got.MinTLSVersion != 0x0304 {
		t.Errorf("ssl monitor: keyword %q, warn days %d, floor %#x", got.Keyword, got.SSLWarnDays, got.MinTLSVersion)
	}

	// A setting sent with the new type is judged, not dropped, even when it
	// is the value the http monitor already held.
	m = seedMonitor(t, db, store.Monitor{Name: "API 2", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true, Keyword: "ok", KeywordMode: "must_contain"})
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(m.ID),
		`{"type":"tcp","target":"example.com:443","keyword":"ok","keyword_mode":"must_contain"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "keyword", "this tcp monitor never uses it")
}

// A file is checked as a create for a new key and as a patch for every key.
// Headers and a body are judged on the entry too, though they are not part
// of the patch.
func TestImportRefusesASettingItsCheckNeverReads(t *testing.T) {
	entry := "version: 1\nmonitors:\n  - key: db\n    name: DB\n    type: tcp\n    target: db.example.com:5432\n"
	for name, tc := range map[string]struct{ lines, field string }{
		"keyword": {"    keyword: ok\n    keyword_mode: must_contain\n", "monitors[0].keyword"},
		"headers": {"    headers:\n      X-Token: t\n", "monitors[0].headers"},
		"body":    {"    body: '{}'\n", "monitors[0].body"},
		"floor":   {"    min_tls_version: \"1.3\"\n", "monitors[0].min_tls_version"},
	} {
		for _, existing := range []bool{false, true} {
			label := name + "/new"
			if existing {
				label = name + "/existing"
			}
			t.Run(label, func(t *testing.T) {
				srv, db := testServerWithDB(t)
				if existing {
					if code, _, body := importYAML(t, srv, entry, false); code != http.StatusOK {
						t.Fatalf("seeding import: status = %d: %s", code, body)
					}
				}
				code, _, body := importYAML(t, srv, entry+tc.lines, false)
				wantStatusProblem(t, code, body, tc.field, "this tcp monitor never uses it")
				mons, err := db.ListMonitors(context.Background())
				must(t, err)
				for _, m := range mons {
					if m.Keyword != "" || len(m.Headers) != 0 || m.Body != "" || m.MinTLSVersion != 0 {
						t.Errorf("a refused file changed the monitor: %+v", m)
					}
				}
			})
		}
	}
}

// An export of a monitor saved before the rule imports back unchanged: the
// export writes no setting a tcp monitor does not read, and an import of an
// edited copy of an http monitor's own file keeps working.
func TestImportOfItsOwnExportKeepsAStoredSettingItsCheckNeverReads(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, tcpMonitor(legacyTCP))
	doc := exportYAML(t, srv)
	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import of own export: status = %d: %s", code, body)
	}
	if len(rep.Monitors) != 1 || rep.Monitors[0].Action != actionUnchanged {
		t.Errorf("report = %+v, want one unchanged monitor", rep.Monitors)
	}
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Keyword != "ok" || got.Headers["X-Token"] != "t" {
		t.Errorf("import of own export changed the stored settings: %+v", got)
	}
}
