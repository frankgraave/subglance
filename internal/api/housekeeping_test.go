package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/housekeeping"
	"github.com/frankgraave/subglance/internal/store"
)

// fakePasses stands in for the scheduler.
type fakePasses struct {
	mu      sync.Mutex
	err     error
	running bool
	begun   []string
	ctx     context.Context
}

func (f *fakePasses) Begin(ctx context.Context, trigger string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.begun = append(f.begun, trigger)
	f.ctx = ctx
	return nil
}

func (f *fakePasses) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// fakeCompactor plans what it is told and holds a compaction open until
// release is closed.
type fakeCompactor struct {
	plan    store.CompactPlan
	result  store.CompactResult
	err     error
	entered chan struct{}
	release chan struct{}
}

func (f *fakeCompactor) PlanCompact(context.Context) (store.CompactPlan, error) { return f.plan, nil }

func (f *fakeCompactor) Compact(context.Context) (store.CompactResult, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return f.result, f.err
}

func postAction(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

// waitFor polls cond until it holds, failing the test after five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRetentionSettingsReportRunTimeLimitAndLastPass(t *testing.T) {
	srv, db := testServerWithDB(t)

	got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
	if got.RunAt.Value != "03:30" || got.RunAt.Source != "default" || got.RunAt.PinnedBy != nil {
		t.Errorf("run_at = %+v, want the 03:30 default", got.RunAt)
	}
	if got.MaxDatabaseSize.Bytes != 0 || got.MaxDatabaseSize.Source != "default" || got.MaxDatabaseSize.MinimumBytes != store.MinMaxDatabaseSize {
		t.Errorf("max_database_size = %+v, want none by default", got.MaxDatabaseSize)
	}
	if got.LastPass != nil || got.Running {
		t.Errorf("last_pass = %+v, running %v; want no pass yet", got.LastPass, got.Running)
	}
	if got.Compact == nil || got.Compact.SizeBytes <= 0 || got.Compact.AutoVacuum == "" {
		t.Errorf("compact = %+v, want the plan for this database", got.Compact)
	}

	// A recorded pass comes back, size limit and error included, so the
	// card can show it after a restart.
	started := time.Date(2026, 9, 30, 3, 30, 0, 0, time.UTC)
	if err := db.SaveRetentionPass(t.Context(), store.RetentionPass{
		StartedAt: started, Duration: 1500 * time.Millisecond, Trigger: housekeeping.TriggerSchedule,
		Heartbeats: 10, FreedBytes: 4096, Error: "disk I/O error",
		SizeCap: &store.SizeCapResult{Limit: 2 << 30, Before: 3 << 30, After: 2 << 30, Heartbeats: 7, RawSince: started.AddDate(0, 0, -12)},
	}); err != nil {
		t.Fatal(err)
	}
	got = decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
	p := got.LastPass
	if p == nil || !p.StartedAt.Equal(started) || p.DurationMS != 1500 || p.Trigger != "schedule" || p.Heartbeats != 10 ||
		p.FreedBytes != 4096 || p.Error == nil || *p.Error != "disk I/O error" {
		t.Fatalf("last_pass = %+v", p)
	}
	if c := p.SizeCap; c == nil || c.LimitBytes != 2<<30 || c.Heartbeats != 7 || c.RawSince == nil || c.HourlySince != nil {
		t.Errorf("size_cap = %+v, want the limit's work with only raw_since set", c)
	}
}

func TestRetentionSettingsSaveRunTimeAndLimit(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"run_at":"01:45","max_database_bytes":2147483648}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeRetention(t, rec)
	if got.RunAt.Value != "01:45" || got.RunAt.Source != "database" || got.MaxDatabaseSize.Bytes != 2<<30 {
		t.Errorf("after PUT: run_at %+v, limit %+v", got.RunAt, got.MaxDatabaseSize)
	}
	// Stored where the scheduler reads them.
	if at, err := db.ResolveRetentionRunAt(t.Context(), nil); err != nil || at.Value != (store.ClockTime{Hour: 1, Minute: 45}) {
		t.Errorf("stored run time = %+v, %v", at, err)
	}
	if n, err := db.ResolveMaxDatabaseSize(t.Context(), nil); err != nil || n.Value != 2<<30 {
		t.Errorf("stored limit = %+v, %v", n, err)
	}

	// Zero removes the limit.
	rec = doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", `{"max_database_bytes":0}`)
	if got := decodeRetention(t, rec); rec.Code != http.StatusOK || got.MaxDatabaseSize.Bytes != 0 {
		t.Errorf("clearing the limit = %d %+v", rec.Code, got.MaxDatabaseSize)
	}
}

func TestRetentionSettingsRefuseABadRunTimeOrLimit(t *testing.T) {
	srv, _ := testServerWithDB(t)
	for _, tc := range []struct{ body, field string }{
		{`{"run_at":"3:30"}`, "run_at"},
		{`{"run_at":"24:00"}`, "run_at"},
		{`{"max_database_bytes":1024}`, "max_database_bytes"},
		{`{"max_database_bytes":-1}`, "max_database_bytes"},
		// Refused as a whole: a valid window beside a bad limit is not saved.
		{`{"raw_seconds":604800,"max_database_bytes":1}`, "max_database_bytes"},
	} {
		rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", tc.body)
		var e errorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || e.Field != tc.field {
			t.Errorf("%s = %d field %q, want 400 on %s: %s", tc.body, rec.Code, e.Field, tc.field, e.Error)
		}
	}
	if got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "")); got.Raw.Source != "default" {
		t.Errorf("raw = %+v after a refused save, want nothing stored", got.Raw)
	}
}

func TestRetentionSettingsPinnedRunTimeAndLimitAreReadOnly(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithRetentionPins(store.RetentionPins{
		RunAt:   &store.RetentionRunAtPin{Value: store.ClockTime{Hour: 2}, By: "SUBGLANCE_RETENTION_RUN_AT"},
		MaxSize: &store.MaxDatabaseSizePin{Value: 1 << 30, By: "--max-database-size"},
	})
	for body, by := range map[string]string{
		`{"run_at":"04:00"}`:                "SUBGLANCE_RETENTION_RUN_AT",
		`{"max_database_bytes":2147483648}`: "--max-database-size",
	} {
		rec := doJSON(t, srv, http.MethodPut, "/api/v1/settings/retention", body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), by) {
			t.Errorf("%s over a pin = %d %s, want 409 naming %s", body, rec.Code, rec.Body.String(), by)
		}
	}
	got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
	if got.RunAt.Value != "02:00" || got.RunAt.Source != "pinned" || got.RunAt.PinnedBy == nil ||
		got.MaxDatabaseSize.Bytes != 1<<30 || got.MaxDatabaseSize.PinnedBy == nil || *got.MaxDatabaseSize.PinnedBy != "--max-database-size" {
		t.Errorf("run_at %+v, limit %+v; want both pinned", got.RunAt, got.MaxDatabaseSize)
	}
}

func TestRunRetentionStartsAPassInTheBackground(t *testing.T) {
	srv, _ := testServerWithDB(t)
	if rec := postAction(t, srv, "/api/v1/settings/retention/run"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without housekeeping = %d, want 503", rec.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	passes := &fakePasses{}
	srv.WithHousekeeping(ctx, passes)
	rec := postAction(t, srv, "/api/v1/settings/retention/run")
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/api/v1/settings/retention" {
		t.Fatalf("run = %d (Location %q): %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if len(passes.begun) != 1 || passes.begun[0] != housekeeping.TriggerManual {
		t.Errorf("begun = %v, want one manual pass", passes.begun)
	}
	// The pass must outlive the request, so it is not handed the request's
	// context, which ends with the response.
	if passes.ctx != ctx {
		t.Error("the pass was started under a context other than the server's")
	}

	passes.err = housekeeping.ErrPassRunning
	if rec := postAction(t, srv, "/api/v1/settings/retention/run"); rec.Code != http.StatusConflict {
		t.Errorf("run during a pass = %d, want 409", rec.Code)
	}
	passes.running = true
	if got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "")); !got.Running {
		t.Error("GET does not say a pass is running")
	}
}

func TestRunRetentionIsRecordedAsTheLastPass(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithHousekeeping(t.Context(), housekeeping.New(housekeeping.Options{Store: db}))

	if rec := postAction(t, srv, "/api/v1/settings/retention/run"); rec.Code != http.StatusAccepted {
		t.Fatalf("run = %d: %s", rec.Code, rec.Body.String())
	}
	var got retentionResponse
	waitFor(t, "the manual pass to be recorded", func() bool {
		got = decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
		return got.LastPass != nil && !got.Running
	})
	if got.LastPass.Trigger != "manual" || got.LastPass.Error != nil {
		t.Errorf("last_pass = %+v, want a successful manual pass", got.LastPass)
	}
}

func TestRetentionActionsAreForAdministratorsOnly(t *testing.T) {
	srv, db := testServerWithDB(t)
	passes := &fakePasses{}
	srv.WithHousekeeping(t.Context(), passes)
	srv.compactStore = &fakeCompactor{}
	for _, role := range []store.Role{store.RoleViewer, store.RoleEditor} {
		token := seedUser(t, srv, db, string(role)+"@actions.test", role)
		for _, path := range []string{"/api/v1/settings/retention/run", "/api/v1/settings/retention/compact"} {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s POST %s = %d, want 403", role, path, rec.Code)
			}
		}
	}
	if len(passes.begun) != 0 {
		t.Errorf("a refused request started %v", passes.begun)
	}
}

func TestCompactRefusesOnAFullDiskBeforeStarting(t *testing.T) {
	srv, _ := testServerWithDB(t)
	fake := &fakeCompactor{
		plan:    store.CompactPlan{SizeBytes: 3 << 30, Disk: &store.CompactDiskError{Dir: "/data", NeedBytes: 6 << 30, FreeBytes: 1 << 30}},
		entered: make(chan struct{}, 1),
	}
	srv.compactStore = fake

	rec := postAction(t, srv, "/api/v1/settings/retention/compact")
	if rec.Code != http.StatusInsufficientStorage || !strings.Contains(rec.Body.String(), "/data") {
		t.Fatalf("compact on a full disk = %d %s, want 507 naming the directory", rec.Code, rec.Body.String())
	}
	select {
	case <-fake.entered:
		t.Fatal("a compaction started on a disk that cannot hold it")
	default:
	}
	// The refusal released the claim: with room on the disk it starts.
	fake.plan.Disk = nil
	fake.plan.Estimate = 3 * time.Second
	rec = postAction(t, srv, "/api/v1/settings/retention/compact")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"estimate_seconds":3`) {
		t.Fatalf("compact with room = %d %s, want 202 with the estimate", rec.Code, rec.Body.String())
	}
	<-fake.entered
	waitFor(t, "the compaction to finish", func() bool { return !srv.compacting.Load() })
}

func TestCompactRefusesASecondCompactionAndReportsTheFirst(t *testing.T) {
	srv, _ := testServerWithDB(t)
	fake := &fakeCompactor{
		result:  store.CompactResult{BeforeBytes: 300 << 20, AfterBytes: 100 << 20, Duration: 2 * time.Second},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	srv.compactStore = fake

	if rec := postAction(t, srv, "/api/v1/settings/retention/compact"); rec.Code != http.StatusAccepted {
		t.Fatalf("first compact = %d: %s", rec.Code, rec.Body.String())
	}
	<-fake.entered
	if rec := postAction(t, srv, "/api/v1/settings/retention/compact"); rec.Code != http.StatusConflict {
		t.Errorf("second compact = %d, want 409", rec.Code)
	}
	if got := decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", "")); got.Compact == nil || !got.Compact.Running {
		t.Errorf("compact = %+v, want running", got.Compact)
	}
	close(fake.release)

	var got retentionResponse
	waitFor(t, "the compaction to be reported", func() bool {
		got = decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
		return got.Compact != nil && got.Compact.Last != nil
	})
	if l := got.Compact.Last; l.BeforeBytes != 300<<20 || l.AfterBytes != 100<<20 || l.DurationMS != 2000 || l.Error != nil || got.Compact.Running {
		t.Errorf("compact = %+v, last %+v", got.Compact, l)
	}
}

func TestCompactRunsAgainstTheRealDatabase(t *testing.T) {
	srv, _ := testServerWithDB(t)
	if rec := postAction(t, srv, "/api/v1/settings/retention/compact"); rec.Code != http.StatusAccepted {
		t.Skipf("compact = %d %s: this filesystem cannot report free space or is full", rec.Code, rec.Body.String())
	}
	var got retentionResponse
	waitFor(t, "the compaction to finish", func() bool {
		got = decodeRetention(t, doJSON(t, srv, http.MethodGet, "/api/v1/settings/retention", ""))
		return got.Compact != nil && got.Compact.Last != nil
	})
	if l := got.Compact.Last; l.Error != nil || l.AfterBytes <= 0 {
		t.Errorf("last compaction = %+v", l)
	}
	if got.Compact.AutoVacuum != "incremental" {
		t.Errorf("auto_vacuum after compacting = %q, want incremental", got.Compact.AutoVacuum)
	}
}
