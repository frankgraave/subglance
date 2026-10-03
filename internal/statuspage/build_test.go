package statuspage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// fakeRecovery reports the listed monitors as recovering.
type fakeRecovery map[int64]bool

func (f fakeRecovery) Recovery(id int64) (int, int, bool) { return 1, 2, f[id] }

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(t.TempDir(), "sp.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// secret strings a monitor carries that no visitor may ever see. Each is
// distinct and unlikely to occur by chance in a rendered page.
const (
	secretName    = "internal-billing-db-7f3a"
	secretTarget  = "https://billing.internal.example/health?key=zz9plural"
	secretHeader  = "X-Secret-Header-q81"
	secretBody    = "stacktrace at billing.go:42 qx7"
	secretError   = "dial tcp: connection refused by db-primary-3"
	secretTagKey  = "customer"
	secretTagVal  = "acme-corp-hidden"
	secretMaint   = "Rotate the billing master key"
	secretCause   = "cause-db-primary-failover"
	publicTitle   = "Acme services"
	publicDisplay = "Billing"
)

type fixture struct {
	db      *store.DB
	page    store.StatusPage
	monitor store.Monitor
	key     string
}

// seed builds one enabled monitor carrying every secret above, puts it on a
// page under a public name, and records a passing check.
func seed(t *testing.T, now time.Time) fixture {
	t.Helper()
	ctx := t.Context()
	db := openDB(t)
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: secretName, Type: "http", Target: secretTarget, Enabled: true,
		Headers: map[string]string{secretHeader: "v"},
		Tags:    map[string]string{secretTagKey: secretTagVal},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.CreateStatusPage(ctx, store.StatusPage{Slug: "acme", Title: publicTitle, Selection: store.StatusPageSelectMonitors, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := db.SetStatusPageEntries(ctx, p.ID, []store.StatusPageEntryInput{{MonitorID: m.ID, DisplayName: publicDisplay}})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{db: db, page: p, monitor: m, key: entries[0].PublicKey}
	f.beat(t, now.Add(-2*time.Minute), true, "up")
	return f
}

func (f fixture) beat(t *testing.T, at time.Time, ok bool, assessment string) {
	t.Helper()
	hb := store.Heartbeat{MonitorID: f.monitor.ID, TS: at, OK: ok, Assessment: assessment, StatusCode: 200}
	if !ok {
		hb.StatusCode, hb.Error = 503, secretError
		hb.Response = &store.ResponseSnapshot{Body: secretBody, Headers: map[string]string{secretHeader: secretBody}}
	}
	if err := f.db.RecordHeartbeat(t.Context(), hb); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) build(t *testing.T, rec RecoverySource, now time.Time) Page {
	t.Helper()
	page, err := Builder{DB: f.db, Recovery: rec}.Build(t.Context(), f.page, now)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestBuildPublishesNoInternalDetail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := seed(t, now)
	ctx := t.Context()
	if _, err := f.db.CreateMaintenance(ctx, store.MaintenanceWindow{
		Name: secretMaint, TagKey: secretTagKey, TagValue: secretTagVal,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	stages := []struct {
		name     string
		prepare  func()
		rec      RecoverySource
		want     Status
		outages  int
		maintain int
	}{
		{name: "up", want: StatusUp, maintain: 1},
		{name: "down", prepare: func() {
			f.beat(t, now.Add(-time.Minute), false, "down")
			if _, err := f.db.OpenIncident(ctx, f.monitor.ID, now.Add(-90*time.Second), secretCause, secretError); err != nil {
				t.Fatal(err)
			}
			if err := f.db.ConfirmIncident(ctx, f.monitor.ID, now.Add(-time.Minute), secretCause, secretError); err != nil {
				t.Fatal(err)
			}
		}, want: StatusDown, outages: 1, maintain: 1},
		{name: "degraded", rec: fakeRecovery{f.monitor.ID: true}, want: StatusDegraded, outages: 1, maintain: 1},
	}
	for _, st := range stages {
		t.Run(st.name, func(t *testing.T) {
			if st.prepare != nil {
				st.prepare()
			}
			page := f.build(t, st.rec, now)
			if len(page.Entries) != 1 || page.Entries[0].Status != st.want {
				t.Fatalf("entries = %+v, want one %s", page.Entries, st.want)
			}
			if len(page.Outages) != st.outages || len(page.Maintenance) != st.maintain {
				t.Fatalf("outages, maintenance = %d, %d; want %d, %d", len(page.Outages), len(page.Maintenance), st.outages, st.maintain)
			}
			b, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			out := string(b)
			for _, s := range []string{secretName, secretTarget, secretHeader, secretBody, secretError,
				secretTagKey, secretTagVal, secretMaint, secretCause, "billing.internal"} {
				if strings.Contains(out, s) {
					t.Errorf("public JSON contains %q:\n%s", s, out)
				}
			}
			if !strings.Contains(out, publicDisplay) || !strings.Contains(out, publicTitle) || !strings.Contains(out, f.key) {
				t.Errorf("public JSON lacks the public name, title or key:\n%s", out)
			}
		})
	}
}

func TestBuildStatesFollowTheDesign(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx := t.Context()

	t.Run("an unconfirmed failure stays up", func(t *testing.T) {
		f := seed(t, now)
		f.beat(t, now.Add(-time.Minute), false, "warning")
		if _, err := f.db.OpenIncident(ctx, f.monitor.ID, now.Add(-time.Minute), "", ""); err != nil {
			t.Fatal(err)
		}
		page := f.build(t, fakeRecovery{f.monitor.ID: true}, now)
		if got := page.Entries[0].Status; got != StatusUp {
			t.Fatalf("status = %s, want up", got)
		}
		if len(page.Outages) != 0 {
			t.Fatalf("an unconfirmed incident is listed as an outage: %+v", page.Outages)
		}
	})

	t.Run("never checked is no data", func(t *testing.T) {
		db := openDB(t)
		m, err := db.CreateMonitor(ctx, store.Monitor{Name: "n", Type: "http", Target: "https://example.com", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := db.CreateStatusPage(ctx, store.StatusPage{Slug: "s", Title: "S", Selection: store.StatusPageSelectMonitors})
		if _, err := db.SetStatusPageEntries(ctx, p.ID, []store.StatusPageEntryInput{{MonitorID: m.ID, DisplayName: "N"}}); err != nil {
			t.Fatal(err)
		}
		page, err := Builder{DB: db}.Build(ctx, p, now)
		if err != nil {
			t.Fatal(err)
		}
		if got := page.Entries[0].Status; got != StatusNoData {
			t.Fatalf("status = %s, want no_data", got)
		}
		if page.Entries[0].Uptime90d != nil {
			t.Fatalf("uptime = %v, want null without checks", *page.Entries[0].Uptime90d)
		}
		if page.Summary.Unmonitored != 1 {
			t.Fatalf("summary = %+v, want one unmonitored", page.Summary)
		}
	})

	t.Run("paused is not monitored", func(t *testing.T) {
		f := seed(t, now)
		if err := f.db.SetMonitorEnabled(ctx, f.monitor.ID, false); err != nil {
			t.Fatal(err)
		}
		if got := f.build(t, nil, now).Entries[0].Status; got != StatusNotMonitored {
			t.Fatalf("status = %s, want not_monitored", got)
		}
	})

	t.Run("without a recovery source a confirmed incident is down", func(t *testing.T) {
		f := seed(t, now)
		if _, err := f.db.OpenIncident(ctx, f.monitor.ID, now.Add(-time.Minute), "", ""); err != nil {
			t.Fatal(err)
		}
		if err := f.db.ConfirmIncident(ctx, f.monitor.ID, now, "", ""); err != nil {
			t.Fatal(err)
		}
		if got := f.build(t, nil, now).Entries[0].Status; got != StatusDown {
			t.Fatalf("status = %s, want down", got)
		}
	})
}

func TestBuildHistoryAndOutages(t *testing.T) {
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	ctx := t.Context()
	f := seed(t, now)

	// A resolved confirmed outage three days ago, 30 minutes long. It starts
	// at noon so it never straddles midnight: each day rounds its down
	// minutes up, and a split outage would count 31.
	start := time.Date(now.Year(), now.Month(), now.Day()-3, 12, 0, 0, 0, time.UTC)
	if _, err := f.db.OpenIncident(ctx, f.monitor.ID, start, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ConfirmIncident(ctx, f.monitor.ID, start.Add(time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ResolveIncident(ctx, f.monitor.ID, start.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	page := f.build(t, nil, now)
	e := page.Entries[0]
	if len(e.Days) != HistoryDays {
		t.Fatalf("got %d days, want %d", len(e.Days), HistoryDays)
	}
	down := 0
	for _, d := range e.Days {
		down += d.DownMinutes
	}
	if down != 30 {
		t.Fatalf("down minutes over the history = %d, want 30", down)
	}
	if e.Days[HistoryDays-1].State != DayUp {
		t.Fatalf("today = %s, want up (one passing check)", e.Days[HistoryDays-1].State)
	}
	if len(page.Outages) != 1 || page.Outages[0].Key != f.key || page.Outages[0].DurationS != 1800 {
		t.Fatalf("outages = %+v, want one 1800 s outage under the entry key", page.Outages)
	}
	if e.Uptime90d == nil || *e.Uptime90d != 100 {
		t.Fatalf("uptime = %v, want 100", e.Uptime90d)
	}
	if page.Summary.Up != 1 || page.GeneratedAt != now || page.Timezone != "UTC" {
		t.Fatalf("page header = %+v / %v / %s", page.Summary, page.GeneratedAt, page.Timezone)
	}
}

// A confirmed-down check 40 days ago is in the 90-day figure and not in the
// 30-day one a phone prints under its 30 bars.
func TestBuildUptimeOverBothPeriods(t *testing.T) {
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	f := seed(t, now)
	f.beat(t, now.Add(-40*24*time.Hour), false, "down")

	e := f.build(t, nil, now).Entries[0]
	if e.Uptime90d == nil || *e.Uptime90d != 50 {
		t.Errorf("uptime_90d = %v, want 50 (one up, one down)", fmtUptime(e.Uptime90d))
	}
	if e.Uptime30d == nil || *e.Uptime30d != 100 {
		t.Errorf("uptime_30d = %v, want 100 (the down check is older)", fmtUptime(e.Uptime30d))
	}
}

func TestBuildMaintenance(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	ctx := t.Context()
	f := seed(t, now)
	other, err := f.db.CreateMonitor(ctx, store.Monitor{Name: "o", Type: "http", Target: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	off, err := f.db.CreateMonitor(ctx, store.Monitor{Name: "not on the page", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.db.SetStatusPageEntries(ctx, f.page.ID, []store.StatusPageEntryInput{
		{MonitorID: f.monitor.ID, DisplayName: publicDisplay}, {MonitorID: other.ID, DisplayName: "Other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyA, keyB := entries[0].PublicKey, entries[1].PublicKey

	windows := []store.MaintenanceWindow{
		// Running now, by tag: covers the first entry only.
		{Name: "a", TagKey: secretTagKey, TagValue: secretTagVal, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)},
		// In three days, for the second entry, and the same stretch again for
		// the first: one item listing both keys.
		{Name: "b", MonitorID: other.ID, StartsAt: now.Add(72 * time.Hour), EndsAt: now.Add(73 * time.Hour)},
		{Name: "c", MonitorID: f.monitor.ID, StartsAt: now.Add(72 * time.Hour), EndsAt: now.Add(73 * time.Hour)},
		// Beyond the seven-day horizon: not announced.
		{Name: "d", MonitorID: other.ID, StartsAt: now.Add(8 * 24 * time.Hour), EndsAt: now.Add(8*24*time.Hour + time.Hour)},
		// Over, and for a monitor not on the page: neither is listed.
		{Name: "e", MonitorID: other.ID, StartsAt: now.Add(-5 * time.Hour), EndsAt: now.Add(-4 * time.Hour)},
		{Name: "f", MonitorID: off.ID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)},
	}
	for _, w := range windows {
		if _, err := f.db.CreateMaintenance(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	// CreateMaintenance stamps the wall clock as the creation time, and a
	// window is never active before it was created. This test runs at a
	// fixed now, so the stored windows are backdated to before it.
	if _, err := f.db.Writer.ExecContext(ctx,
		`UPDATE maintenance_windows SET spec = json_set(spec, '$.created_at', ?)`,
		now.Add(-30*24*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	// A confirmed outage entirely inside the running window is not downtime.
	if _, err := f.db.OpenIncident(ctx, f.monitor.ID, now.Add(-30*time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ConfirmIncident(ctx, f.monitor.ID, now.Add(-29*time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ResolveIncident(ctx, f.monitor.ID, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	page := f.build(t, nil, now)
	if got := page.Entries[0].Days[HistoryDays-1].DownMinutes; got != 0 {
		t.Errorf("down minutes today = %d, want 0: the outage was inside maintenance", got)
	}
	if !page.Entries[0].InMaintenance || page.Entries[1].InMaintenance {
		t.Fatalf("in_maintenance = %v, %v; want true, false", page.Entries[0].InMaintenance, page.Entries[1].InMaintenance)
	}
	want := []Maintenance{
		{StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Keys: []string{keyA}},
		{StartsAt: now.Add(72 * time.Hour), EndsAt: now.Add(73 * time.Hour), Keys: []string{keyA, keyB}},
	}
	if len(page.Maintenance) != len(want) {
		t.Fatalf("maintenance = %+v, want %+v", page.Maintenance, want)
	}
	for i, w := range want {
		g := page.Maintenance[i]
		if !g.StartsAt.Equal(w.StartsAt) || !g.EndsAt.Equal(w.EndsAt) || strings.Join(g.Keys, ",") != strings.Join(w.Keys, ",") {
			t.Errorf("maintenance[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestBuildTagPageShowsOnlyNamedTaggedMonitors(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx := t.Context()
	f := seed(t, now)
	_, err := f.db.CreateMonitor(ctx, store.Monitor{Name: "tagged-but-unnamed-x9", Type: "http", Target: "https://example.com",
		Enabled: true, Tags: map[string]string{secretTagKey: secretTagVal}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.db.CreateStatusPage(ctx, store.StatusPage{Slug: "tagged", Title: "T", Selection: store.StatusPageSelectTag,
		TagKey: secretTagKey, TagValue: secretTagVal, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SetStatusPageEntries(ctx, p.ID, []store.StatusPageEntryInput{{MonitorID: f.monitor.ID, DisplayName: publicDisplay}}); err != nil {
		t.Fatal(err)
	}
	page, err := Builder{DB: f.db}.Build(ctx, p, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Name != publicDisplay {
		t.Fatalf("entries = %+v, want only the named monitor", page.Entries)
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), "tagged-but-unnamed-x9") {
		t.Fatalf("an unnamed tagged monitor leaked: %s", b)
	}
}

func TestBuildEmptyPageAndBadZone(t *testing.T) {
	ctx := t.Context()
	db := openDB(t)
	p, err := db.CreateStatusPage(ctx, store.StatusPage{Slug: "empty", Title: "E", Selection: store.StatusPageSelectMonitors})
	if err != nil {
		t.Fatal(err)
	}
	page, err := Builder{DB: db}.Build(ctx, p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(page)
	// Empty lists are [] rather than null, so a client can iterate them.
	for _, k := range []string{`"entries":[]`, `"maintenance":[]`, `"outages":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("empty page JSON lacks %s: %s", k, b)
		}
	}
	p.Timezone = "Not/AZone"
	if _, err := (Builder{DB: db}).Build(ctx, p, time.Now()); err == nil {
		t.Error("a page with an unknown time zone built without error")
	}
}
