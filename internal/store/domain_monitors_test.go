package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDomainMigrationPreservesMonitors covers the rebuild in 0030 the way
// TestDNSMigrationPreservesMonitors covers 0029, including the dns columns
// that 0029 added and 0030 has to carry across.
func TestDomainMigrationPreservesMonitors(t *testing.T) {
	ctx := context.Background()
	db := openUnmigratedDB(t)

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if err := db.prepareMigrationTable(ctx); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	var rest []migration
	for _, m := range all {
		if m.name >= "0030" {
			rest = append(rest, m)
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}
	if len(rest) == 0 || !strings.HasPrefix(rest[0].name, "0030") {
		t.Fatal("no 0030 migration found")
	}

	now := time.Now().Unix()
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := db.Writer.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	if _, err := db.Writer.ExecContext(ctx, `INSERT INTO monitors (name, type, target, interval_s, created_at, updated_at)
		VALUES ('reg', 'domain', 'example.com', 86400, ?, ?)`, now, now); err == nil {
		t.Fatal("the pre-0030 schema accepted a domain monitor; the test is not testing a rebuild")
	}

	full := exec(`INSERT INTO monitors (name, type, target, interval_s, timeout_s, retries,
		method, expected_status, keyword, keyword_mode, follow_redirects, headers_json, body,
		ssl_warn_days, enabled, created_at, updated_at, capture_response, repeat_after_s,
		min_tls_version, recovery_threshold, json_path, json_operator, json_expected, resumed_at)
		VALUES ('api', 'http', 'https://example.com/health', 30, 5, 3,
		'POST', '200', 'ok', 'must_contain', 0, '{"X-Key":"k"}', '{"q":1}',
		21, 0, ?, ?, 0, 600, 772, 4, 'status', 'equals', '"up"', ?)`, now-100, now-50, now-10)
	zone := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at,
		dns_record_type, dns_expected_json, dns_resolver)
		VALUES ('zone', 'dns', 'example.com', ?, ?, 'MX', '["10 mx.example.com"]', '1.1.1.1')`, now, now)
	exec(`INSERT INTO monitors (name, type, target, created_at, updated_at,
		push_token_hash, push_token_prefix, push_interval_s, push_grace_s)
		VALUES ('backup', 'push', '', ?, ?, 'hash', 'pfx', 86400, 600)`, now, now)
	gone := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('old', 'tcp', 'db.example.com:5432', ?, ?)`, now, now)

	exec(`INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms) VALUES (?, ?, 1, 12)`, zone, now)
	exec(`INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)`, full, now)
	exec(`INSERT INTO monitor_tags (monitor_id, key, value) VALUES (?, 'env', 'prod')`, full)
	exec(`DELETE FROM monitors WHERE id = ?`, gone)

	before := rawMonitorRowsWithDNS(t, db)
	for _, m := range rest {
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}
	if after := rawMonitorRowsWithDNS(t, db); !reflect.DeepEqual(before, after) {
		t.Errorf("monitors changed in the rebuild:\nbefore %v\nafter  %v", before, after)
	}
	if m, err := db.GetMonitor(ctx, zone); err != nil || m.DNS == nil || m.DNS.RecordType != "MX" || m.DomainWarnDays != 0 {
		t.Errorf("dns monitor reads back as %+v (%v)", m, err)
	}
	for table, id := range map[string]int64{"heartbeats": zone, "incidents": full, "monitor_tags": full} {
		var n int
		if err := db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE monitor_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s rows = %d, want 1: the rebuild cascaded them away", table, n)
		}
	}

	created, err := db.CreateMonitor(ctx, Monitor{Name: "reg", Type: TypeDomain, Target: "example.com",
		Enabled: true, DomainWarnDays: 30})
	if err != nil {
		t.Fatalf("create domain monitor after 0030: %v", err)
	}
	if created.ID <= gone {
		t.Errorf("domain monitor got id %d, want above the removed id %d", created.ID, gone)
	}
}

// rawMonitorRowsWithDNS is rawMonitorRows plus the columns 0029 added.
func rawMonitorRowsWithDNS(t *testing.T, db *DB) [][]string {
	t.Helper()
	rows := rawMonitorRows(t, db)
	r, err := db.Reader.QueryContext(context.Background(),
		`SELECT coalesce(dns_record_type, ''), coalesce(dns_expected_json, ''), coalesce(dns_resolver, '') FROM monitors ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for i := 0; r.Next(); i++ {
		var a, b, c string
		if err := r.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		rows[i] = append(rows[i], a, b, c)
	}
	return rows
}

func TestDomainMonitorRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "reg", Type: TypeDomain, Target: "example.com",
		Enabled: true, DomainWarnDays: 45})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.IntervalS != DefaultDomainIntervalS {
		t.Errorf("interval = %d, want the domain default %d", m.IntervalS, DefaultDomainIntervalS)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil || got.DomainWarnDays != 45 {
		t.Fatalf("read back %+v (%v), want 45 warn days", got, err)
	}

	// Zero is a value: never warn.
	got.DomainWarnDays = 0
	if _, err := db.UpdateMonitor(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	var raw *int
	if err := db.Reader.QueryRowContext(ctx, `SELECT domain_warn_days FROM monitors WHERE id = ?`, m.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == nil || *raw != 0 {
		t.Errorf("domain_warn_days = %v, want 0 rather than NULL", raw)
	}

	// Another type stores NULL whatever the field holds.
	http, err := db.CreateMonitor(ctx, Monitor{Name: "api", Type: "http", Target: "https://example.com", DomainWarnDays: 9})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRowContext(ctx, `SELECT domain_warn_days FROM monitors WHERE id = ?`, http.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Errorf("http monitor stores domain_warn_days %d, want NULL", *raw)
	}
}

// The schema keeps a domain monitor at six hours or slower, and its setting
// with its type, so an import or a direct write cannot get around the API.
func TestSchemaKeepsDomainMonitorsSlow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Now().Unix()

	for name, q := range map[string]string{
		"interval under 6 h": `INSERT INTO monitors (name, type, target, interval_s, domain_warn_days, created_at, updated_at)
			VALUES ('a', 'domain', 'example.com', 21599, 30, ?, ?)`,
		"no threshold": `INSERT INTO monitors (name, type, target, interval_s, created_at, updated_at)
			VALUES ('a', 'domain', 'example.com', 86400, ?, ?)`,
		"threshold on http": `INSERT INTO monitors (name, type, target, domain_warn_days, created_at, updated_at)
			VALUES ('a', 'http', 'https://example.com', 30, ?, ?)`,
	} {
		if _, err := db.Writer.ExecContext(ctx, q, now, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := db.Writer.ExecContext(ctx, `INSERT INTO monitors (name, type, target, interval_s, domain_warn_days, created_at, updated_at)
		VALUES ('a', 'domain', 'example.com', 21600, 0, ?, ?)`, now, now); err != nil {
		t.Errorf("6 h and no warning: %v", err)
	}
}

func TestUnknownChecks(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m, err := db.CreateMonitor(ctx, Monitor{Name: "reg", Type: TypeDomain, Target: "example.nl", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, found, err := db.LatestUnknownCheck(ctx, m.ID); err != nil || found {
		t.Fatalf("fresh monitor has an unknown check (%v)", err)
	}
	first := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.RecordUnknownCheck(ctx, UnknownCheck{MonitorID: m.ID, At: first, Reason: "no RDAP"}))
	must(db.RecordUnknownCheck(ctx, UnknownCheck{MonitorID: m.ID, At: first.Add(6 * time.Hour), Reason: "HTTP 429"}))
	c, found, err := db.LatestUnknownCheck(ctx, m.ID)
	if err != nil || !found || c.Reason != "HTTP 429" || !c.At.Equal(first.Add(6*time.Hour)) {
		t.Fatalf("latest = %+v %v %v, want the second one", c, found, err)
	}

	must(db.ClearUnknownCheck(ctx, m.ID))
	if _, found, _ := db.LatestUnknownCheck(ctx, m.ID); found {
		t.Error("unknown check still there after clearing")
	}
	must(db.ClearUnknownCheck(ctx, m.ID))

	must(db.RecordUnknownCheck(ctx, UnknownCheck{MonitorID: m.ID, At: first, Reason: "no RDAP"}))
	must(db.DeleteMonitor(ctx, m.ID))
	var n int
	must(db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM monitor_unknown_checks`).Scan(&n))
	if n != 0 {
		t.Errorf("%d unknown checks left after the monitor was removed", n)
	}
}
