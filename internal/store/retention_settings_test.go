package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

const day = 24 * time.Hour

func dur(d time.Duration) *time.Duration { return &d }

func TestRetentionDefaultsKeepMoreThanBefore(t *testing.T) {
	// The defaults are a product decision recorded in SUB-28: thirty days of
	// raw heartbeats, and summaries kept forever.
	if DefaultRawRetention != 30*day {
		t.Errorf("DefaultRawRetention = %s, want 720h", DefaultRawRetention)
	}
	if DefaultRollupRetention != 0 {
		t.Errorf("DefaultRollupRetention = %s, want 0 (forever)", DefaultRollupRetention)
	}
	if err := (RetentionPolicy{Raw: DefaultRawRetention, Rollup: DefaultRollupRetention}).Validate(); err != nil {
		t.Errorf("the default policy is invalid: %v", err)
	}
}

func TestRetentionPolicyValidate(t *testing.T) {
	tests := []struct {
		name   string
		p      RetentionPolicy
		window string // "" means valid
	}{
		{"defaults", RetentionPolicy{Raw: 30 * day}, ""},
		{"everything forever", RetentionPolicy{}, ""},
		{"minimum raw", RetentionPolicy{Raw: day, Rollup: day}, ""},
		{"raw under a day", RetentionPolicy{Raw: 23 * time.Hour}, "raw"},
		{"raw negative", RetentionPolicy{Raw: -time.Hour}, "raw"},
		{"rollup negative", RetentionPolicy{Raw: day, Rollup: -time.Hour}, "rollup"},
		{"rollup inside raw", RetentionPolicy{Raw: 7 * day, Rollup: day}, "rollup"},
		// Raw beats kept forever never become buckets, so a bounded rollup
		// window would delete summaries of hours that still have raw rows.
		{"raw forever, rollup bounded", RetentionPolicy{Raw: 0, Rollup: 365 * day}, "rollup"},
		{"a century", RetentionPolicy{Raw: 36500 * day, Rollup: 36500 * day}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Validate()
			if tt.window == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want valid", err)
				}
				return
			}
			var re *RetentionError
			if !errors.As(err, &re) || !errors.Is(err, ErrRetentionPolicy) {
				t.Fatalf("Validate() = %v, want a RetentionError", err)
			}
			if re.Window != tt.window {
				t.Errorf("blamed window %q, want %q", re.Window, tt.window)
			}
		})
	}
}

func TestResolveRetentionPrecedence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	eff, err := db.ResolveRetention(ctx, RetentionPins{})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Raw.Source != RetentionSourceDefault || eff.Raw.Value != DefaultRawRetention ||
		eff.Rollup.Source != RetentionSourceDefault || eff.Rollup.Value != DefaultRollupRetention {
		t.Fatalf("empty database resolved to %+v, want the defaults", eff)
	}

	if _, err := db.SetRetention(ctx, dur(14*day), dur(90*day), RetentionPins{}); err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	eff, err = db.ResolveRetention(ctx, RetentionPins{})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Raw.Source != RetentionSourceDatabase || eff.Raw.Value != 14*day ||
		eff.Rollup.Source != RetentionSourceDatabase || eff.Rollup.Value != 90*day {
		t.Fatalf("stored windows resolved to %+v", eff)
	}

	// A pin beats the stored value, so a restart never silently overrides
	// the page, and the page can say which flag is in charge.
	pins := RetentionPins{Raw: &RetentionPin{Value: 3 * day, By: "--raw-retention"}}
	eff, err = db.ResolveRetention(ctx, pins)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Raw.Source != RetentionSourcePinned || eff.Raw.Value != 3*day || eff.Raw.PinnedBy != "--raw-retention" {
		t.Errorf("pinned raw resolved to %+v", eff.Raw)
	}
	if eff.Rollup.Source != RetentionSourceDatabase || eff.Rollup.Value != 90*day {
		t.Errorf("unpinned rollup resolved to %+v, want the stored 90 days", eff.Rollup)
	}
}

func TestResolveRetentionLengthensAStoredWindowAPinInvalidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.SetRetention(ctx, dur(7*day), dur(30*day), RetentionPins{}); err != nil {
		t.Fatal(err)
	}

	// The flag raises raw past the stored rollup window. The stored window
	// rises to meet it rather than the pass deleting summaries of hours
	// whose raw rows still exist.
	eff, err := db.ResolveRetention(ctx, RetentionPins{Raw: &RetentionPin{Value: 60 * day, By: "SUBGLANCE_RAW_RETENTION"}})
	if err != nil {
		t.Fatalf("ResolveRetention: %v", err)
	}
	if eff.Rollup.Value != 60*day || eff.Rollup.Raised == nil || *eff.Rollup.Raised != 30*day {
		t.Errorf("rollup = %+v, want raised from 30 to 60 days", eff.Rollup)
	}

	// Raw forever by flag forces summaries forever too.
	eff, err = db.ResolveRetention(ctx, RetentionPins{Raw: &RetentionPin{Value: 0, By: "--raw-retention"}})
	if err != nil {
		t.Fatalf("ResolveRetention: %v", err)
	}
	if eff.Rollup.Value != 0 || eff.Rollup.Raised == nil {
		t.Errorf("rollup = %+v, want raised to forever", eff.Rollup)
	}
}

func TestSetRetentionRefusesAnInvalidPairAndKeepsTheOldValue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.SetRetention(ctx, dur(14*day), nil, RetentionPins{}); err != nil {
		t.Fatal(err)
	}

	_, err := db.SetRetention(ctx, nil, dur(7*day), RetentionPins{})
	var re *RetentionError
	if !errors.As(err, &re) || re.Window != "rollup" {
		t.Fatalf("SetRetention(rollup inside raw) = %v, want a rollup RetentionError", err)
	}
	raw, rollup, err := db.StoredRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil || *raw != 14*day || rollup != nil {
		t.Errorf("a refused update changed the stored windows: raw %v rollup %v", raw, rollup)
	}

	// Checked against the pin, since that is what the stored value has to
	// live next to.
	_, err = db.SetRetention(ctx, nil, dur(10*day), RetentionPins{Raw: &RetentionPin{Value: 20 * day, By: "--raw-retention"}})
	if !errors.As(err, &re) {
		t.Fatalf("SetRetention under a longer pinned raw window = %v, want refused", err)
	}
}

func TestStoredRetentionRefusesARowItCannotRead(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Writer.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, 'a week', 0)`, settingRawRetention); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResolveRetention(ctx, RetentionPins{}); err == nil {
		t.Fatal("an unparseable stored window resolved to something; guessing a window can delete data")
	}
}

func TestApplyRetentionWithRawForeverRollsNothingUp(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "raw-forever")
	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	beat(t, db, id, now.Add(-400*day), true, 10)
	beat(t, db, id, now.Add(-time.Hour), true, 10)

	res, err := db.applyRetentionAt(context.Background(), now, RetentionPolicy{})
	if err != nil {
		t.Fatalf("applyRetentionAt: %v", err)
	}
	if res.Rollup.Heartbeats != 0 || countRaw(t, db, id) != 2 {
		t.Errorf("raw forever rolled up %d beats, %d left; want none touched", res.Rollup.Heartbeats, countRaw(t, db, id))
	}
}

func TestRetentionTablesMeasureSizeAndGrowth(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "growth")
	now := time.Now()
	// Twelve beats in the last day across three hours, and two older ones
	// that must not count as today's growth.
	for i := range 12 {
		beat(t, db, id, now.Add(-time.Duration(i*15)*time.Minute), true, 10)
	}
	beat(t, db, id, now.Add(-3*day), true, 10)
	beat(t, db, id, now.Add(-4*day), true, 10)

	tables, err := db.retentionTablesAt(context.Background(), now)
	if err != nil {
		t.Fatalf("retentionTablesAt: %v", err)
	}
	byName := map[string]TableUsage{}
	for _, u := range tables {
		byName[u.Name] = u
	}
	hb := byName["heartbeats"]
	if hb.Rows != 14 || hb.RowsPerDay != 12 {
		t.Errorf("heartbeats = %+v, want 14 rows growing 12 a day", hb)
	}
	if hb.Bytes <= 0 {
		t.Errorf("heartbeats bytes = %d, want a measured size", hb.Bytes)
	}
	// Twelve beats fifteen minutes apart touch three or four clock hours;
	// each becomes one hourly bucket once rolled up.
	if h := byName["heartbeat_hourly"].RowsPerDay; h < 3 || h > 4 {
		t.Errorf("hourly growth = %v a day, want 3 or 4", h)
	}
	for _, name := range []string{"heartbeats", "heartbeat_responses", "heartbeat_hourly", "incidents"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("table %s missing from the report", name)
		}
	}
}

func TestPreviewRetentionCountsWithoutDeleting(t *testing.T) {
	db := openTestDB(t)
	id := seedMonitor(t, db, "preview")
	now := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	beat(t, db, id, now.Add(-10*day), true, 10)
	beat(t, db, id, now.Add(-9*day), true, 10)
	beat(t, db, id, now.Add(-time.Hour), true, 10)
	insertBucket(t, db, id, now.Add(-400*day).Truncate(time.Hour))
	insertBucket(t, db, id, now.Add(-20*day).Truncate(time.Hour))

	impact, err := db.previewRetentionAt(context.Background(), now, RetentionPolicy{Raw: 7 * day, Rollup: 365 * day})
	if err != nil {
		t.Fatalf("previewRetentionAt: %v", err)
	}
	if impact.Heartbeats != 2 || impact.HourlyBuckets != 1 {
		t.Errorf("impact = %+v, want 2 heartbeats and 1 bucket", impact)
	}
	if countRaw(t, db, id) != 3 || countHourly(t, db, id) != 2 {
		t.Error("a preview deleted rows")
	}

	impact, err = db.previewRetentionAt(context.Background(), now, RetentionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if impact != (RetentionImpact{}) {
		t.Errorf("keep-forever preview = %+v, want nothing removed", impact)
	}
}

func TestRetentionVersionCountsSaves(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if v, err := db.RetentionVersion(ctx); err != nil || v != 0 {
		t.Fatalf("RetentionVersion before any save = %d, %v; want 0", v, err)
	}
	v1, err := db.SetRetention(ctx, dur(14*day), nil, RetentionPins{})
	if err != nil || v1 != 1 {
		t.Fatalf("first save = %d, %v; want version 1", v1, err)
	}
	// A refused save is not a save: the version an editor holds must stay
	// good after somebody else's invalid attempt.
	if _, err := db.SetRetention(ctx, nil, dur(7*day), RetentionPins{}); err == nil {
		t.Fatal("rollup inside raw was accepted")
	}
	if v, err := db.RetentionVersion(ctx); err != nil || v != 1 {
		t.Fatalf("version after a refused save = %d, %v; want 1", v, err)
	}
	if v2, err := db.SetRetention(ctx, dur(14*day), nil, RetentionPins{}); err != nil || v2 != 2 {
		t.Fatalf("an identical save = %d, %v; want version 2", v2, err)
	}
}

func TestSetRetentionIfVersionRefusesAStaleEditor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Two editors read version 0. The first saves.
	v, err := db.SetRetentionIfVersion(ctx, dur(60*day), nil, RetentionPins{}, []int64{0})
	if err != nil || v != 1 {
		t.Fatalf("first conditional save = %d, %v; want version 1", v, err)
	}
	// The second still holds version 0, so its save describes a policy it
	// never saw and is refused without writing.
	if _, err := db.SetRetentionIfVersion(ctx, dur(20*day), nil, RetentionPins{}, []int64{0}); !errors.Is(err, ErrRetentionVersion) {
		t.Fatalf("stale save = %v, want ErrRetentionVersion", err)
	}
	raw, _, err := db.StoredRetention(ctx)
	if err != nil || raw == nil || *raw != 60*day {
		t.Fatalf("stored raw after a refused save = %v, %v; want the first editor's 60 days", raw, err)
	}
	if v, _ := db.RetentionVersion(ctx); v != 1 {
		t.Errorf("a refused save moved the version to %d", v)
	}
	if _, err := db.SetRetentionIfVersion(ctx, dur(20*day), nil, RetentionPins{}, nil); !errors.Is(err, ErrRetentionVersion) {
		t.Errorf("an empty version list = %v, want it to match nothing", err)
	}

	// Having re-read, the second editor can save.
	if v, err := db.SetRetentionIfVersion(ctx, dur(20*day), nil, RetentionPins{}, []int64{7, 1}); err != nil || v != 2 {
		t.Fatalf("save with the current version = %d, %v; want version 2", v, err)
	}
}
