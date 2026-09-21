package main

import (
	"context"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// The same synthetic history must mean the same thing on either side of the
// raw/rollup boundary. Warnings remain evidence, not confirmed downtime.
func TestSeedAssessmentAcrossRollupBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	from := now.Add(-4 * time.Hour)
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "seed.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "assessed", Type: "http", Target: "https://example.invalid", IntervalS: 60, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	m.CreatedAt = from
	outages := []outage{
		{start: from.Add(10 * time.Minute), end: from.Add(20 * time.Minute), cause: "status", message: "503", status: 503},
		{start: from.Add(30 * time.Minute), end: from.Add(31 * time.Minute), cause: "timeout", message: "timeout", unconfirmed: true},
	}
	pl := plan{now: now, rawSince: from, bucketSince: from}
	beats := buildBeats(m.ID, m, profile{}, from, from.Add(time.Hour), time.Minute, outages, pl, rand.New(rand.NewPCG(1, 2)))
	wantUp, wantDown, wantWarning := 0, 0, 0
	for _, hb := range beats {
		want, cause := "up", ""
		for _, o := range outages {
			if !o.covers(hb.TS) {
				continue
			}
			cause = o.cause
			inc := incidentFor(m.ID, o, m, time.Minute)
			want = "warning"
			if inc.Confirmed() && !hb.TS.Before(inc.ConfirmedAt) {
				want = "down"
			}
		}
		if hb.Assessment != want || hb.FailureKind != cause {
			t.Fatalf("at %s: assessment=%q kind=%q, want %q %q", hb.TS, hb.Assessment, hb.FailureKind, want, cause)
		}
		switch want {
		case "up":
			wantUp++
		case "down":
			wantDown++
		case "warning":
			wantWarning++
		}
	}
	if wantDown == 0 || wantWarning == 0 {
		t.Fatal("fixture must cover confirmation and unconfirmed failure")
	}
	if err := db.SeedHeartbeats(ctx, beats); err != nil {
		t.Fatal(err)
	}
	check := func(multiplier int) {
		t.Helper()
		stats, err := db.Uptime(ctx, m.ID, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Up != multiplier*wantUp || stats.Down != multiplier*wantDown || stats.Warning != multiplier*wantWarning || stats.Legacy != 0 {
			t.Fatalf("uptime=%+v; want up=%d down=%d warning=%d legacy=0", stats, multiplier*wantUp, multiplier*wantDown, multiplier*wantWarning)
		}
	}
	check(1)
	stored, err := db.ListHeartbeats(ctx, m.ID, len(beats))
	if err != nil {
		t.Fatal(err)
	}
	for i, hb := range stored {
		orig := beats[len(beats)-1-i]
		if hb.Assessment != orig.Assessment || hb.FailureKind != orig.FailureKind {
			t.Fatalf("lost assessment/kind: %+v vs %+v", hb, orig)
		}
	}
	// Generate the very same hour as an older rollup instead of raw rows.
	if _, err := db.Writer.ExecContext(ctx, "DELETE FROM heartbeats WHERE monitor_id=?", m.ID); err != nil {
		t.Fatal(err)
	}
	h := buildHistory(m, profile{outages: []outageSpec{
		{ago: 50 * time.Minute, dur: 10 * time.Minute, cause: "status", message: "503", status: 503},
		{ago: 30 * time.Minute, dur: time.Minute, cause: "timeout", message: "timeout", unconfirmed: true},
	}}, m.ID, plan{now: from.Add(time.Hour), rawSince: from.Add(time.Hour), bucketSince: from}, rand.New(rand.NewPCG(1, 2)))
	// Monitor creation is normally backdated by the command before building.
	if len(h.buckets) == 0 {
		t.Fatal("missing hourly fixture")
	}
	if err := db.SeedHourlyBuckets(ctx, h.buckets); err != nil {
		t.Fatal(err)
	}
	check(1)
	if err := db.SeedHourlyBuckets(ctx, h.buckets); err != nil {
		t.Fatal(err)
	}
	check(2)
}
