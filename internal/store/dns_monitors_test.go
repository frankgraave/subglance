package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDNSMigrationPreservesMonitors covers the rebuild in 0029, the way
// TestSMSMigrationPreservesChannels covers 0024: migrate to just before it,
// write rows the old schema allowed, with every column set and rows in the
// tables that point at a monitor, then apply it.
func TestDNSMigrationPreservesMonitors(t *testing.T) {
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
		if m.name >= "0029" {
			rest = append(rest, m)
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}
	if len(rest) == 0 || !strings.HasPrefix(rest[0].name, "0029") {
		t.Fatal("no 0029 migration found")
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

	if _, err := db.Writer.ExecContext(ctx, `INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('zone', 'dns', 'example.com', ?, ?)`, now, now); err == nil {
		t.Fatal("the pre-0029 schema accepted a dns monitor; the test is not testing a rebuild")
	}

	full := exec(`INSERT INTO monitors (name, type, target, interval_s, timeout_s, retries,
		method, expected_status, keyword, keyword_mode, follow_redirects, headers_json, body,
		ssl_warn_days, enabled, created_at, updated_at, capture_response, repeat_after_s,
		min_tls_version, recovery_threshold, json_path, json_operator, json_expected, resumed_at)
		VALUES ('api', 'http', 'https://example.com/health', 30, 5, 3,
		'POST', '200', 'ok', 'must_contain', 0, '{"X-Key":"k"}', '{"q":1}',
		21, 0, ?, ?, 0, 600, 772, 4, 'status', 'equals', '"up"', ?)`, now-100, now-50, now-10)
	push := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at,
		push_token_hash, push_token_prefix, push_interval_s, push_grace_s)
		VALUES ('backup', 'push', '', ?, ?, 'hash', 'pfx', 86400, 600)`, now, now)
	gone := exec(`INSERT INTO monitors (name, type, target, created_at, updated_at)
		VALUES ('old', 'tcp', 'db.example.com:5432', ?, ?)`, now, now)

	exec(`INSERT INTO heartbeats (monitor_id, ts, ok, latency_ms) VALUES (?, ?, 1, 12)`, full, now)
	exec(`INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)`, full, now)
	exec(`INSERT INTO monitor_tags (monitor_id, key, value) VALUES (?, 'env', 'prod')`, full)
	exec(`INSERT INTO monitor_config_keys (monitor_id, key) VALUES (?, 'api')`, full)
	exec(`DELETE FROM monitors WHERE id = ?`, gone)

	// The rows are compared column by column as SQLite holds them, because
	// the Go reader already expects the new columns.
	before := rawMonitorRows(t, db)
	for _, m := range rest {
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %s: %v", m.name, err)
		}
	}

	if after := rawMonitorRows(t, db); !reflect.DeepEqual(before, after) {
		t.Errorf("monitors changed in the rebuild:\nbefore %v\nafter  %v", before, after)
	}
	if m, err := db.GetMonitor(ctx, push); err != nil || m.PushTokenPrefix != "pfx" {
		t.Errorf("push monitor reads back as %+v (%v)", m, err)
	}
	if m, err := db.GetMonitor(ctx, full); err != nil || m.DNS != nil {
		t.Errorf("existing monitor reads back with dns settings %+v (%v), want none", m.DNS, err)
	}

	for table, want := range map[string]int{"heartbeats": 1, "incidents": 1, "monitor_tags": 1, "monitor_config_keys": 1} {
		var n int
		if err := db.Reader.QueryRowContext(ctx,
			`SELECT count(*) FROM `+table+` WHERE monitor_id = ?`, full).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s rows = %d, want %d: the rebuild cascaded them away", table, n, want)
		}
	}

	created, err := db.CreateMonitor(ctx, Monitor{Name: "zone", Type: TypeDNS, Target: "example.com",
		Enabled: true, DNS: &DNSCheck{RecordType: "A", Expected: []string{"192.0.2.1"}}})
	if err != nil {
		t.Fatalf("create dns monitor after 0029: %v", err)
	}
	if created.ID <= gone {
		t.Errorf("dns monitor got id %d, want above the removed id %d", created.ID, gone)
	}
}

// rawMonitorRows reads every column 0029 copies, for every monitor, as text.
func rawMonitorRows(t *testing.T, db *DB) [][]string {
	t.Helper()
	rows, err := db.Reader.QueryContext(context.Background(), `SELECT
		id, name, type, target, interval_s, timeout_s, retries,
		method, expected_status, keyword, keyword_mode, follow_redirects,
		headers_json, body, ssl_warn_days, enabled,
		push_token_hash, push_token_prefix, push_interval_s, push_grace_s,
		created_at, updated_at, capture_response, repeat_after_s, min_tls_version,
		recovery_threshold, json_path, json_operator, json_expected, resumed_at
		FROM monitors ORDER BY id`)
	if err != nil {
		t.Fatalf("read monitors: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	var out [][]string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDNSMonitorRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "mail", Type: TypeDNS, Target: "example.com", Enabled: true,
		DNS: &DNSCheck{RecordType: "MX", Expected: []string{"10 mx1.example.com", "20 mx2.example.com"}, Resolver: "1.1.1.1"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := DNSCheck{RecordType: "MX", Expected: []string{"10 mx1.example.com", "20 mx2.example.com"}, Resolver: "1.1.1.1"}
	if got.DNS == nil || !reflect.DeepEqual(*got.DNS, want) {
		t.Fatalf("read back %+v, want %+v", got.DNS, want)
	}

	got.DNS = &DNSCheck{RecordType: "A"}
	if _, err := db.UpdateMonitor(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, err := db.ListMonitors(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (%d monitors)", err, len(list))
	}
	if d := list[0].DNS; d == nil || d.RecordType != "A" || len(d.Expected) != 0 || d.Resolver != "" {
		t.Errorf("after update %+v, want an A check with no values and the system resolver", d)
	}
	// "Any record" has one spelling in the database: NULL, not "[]".
	var raw *string
	if err := db.Reader.QueryRowContext(ctx, `SELECT dns_expected_json FROM monitors WHERE id = ?`, m.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Errorf("dns_expected_json = %q, want NULL", *raw)
	}
}

// The schema keeps the type and its settings together, so a bulk import or a
// later migration cannot store a dns monitor that asks nothing, or settings
// on a monitor that never reads them.
func TestSchemaKeepsDNSSettingsWithTheirType(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.CreateMonitor(ctx, Monitor{Name: "zone", Type: TypeDNS, Target: "example.com"}); err == nil {
		t.Error("a dns monitor without a record type was stored")
	}
	if _, err := db.CreateMonitor(ctx, Monitor{Name: "site", Type: "http", Target: "https://example.com",
		DNS: &DNSCheck{RecordType: "A"}}); err == nil {
		t.Error("dns settings were stored on an http monitor")
	}
	if _, err := db.CreateMonitor(ctx, Monitor{Name: "zone", Type: TypeDNS, Target: "example.com",
		DNS: &DNSCheck{RecordType: "SOA"}}); err == nil {
		t.Error("a record type outside the list was stored")
	}
}
