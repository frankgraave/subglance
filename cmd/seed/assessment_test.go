package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
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
	engine := state.New(state.Options{})
	for _, hb := range beats {
		tr := engine.Observe(state.Observation{MonitorID: m.ID, At: hb.TS, OK: hb.OK, FailureThreshold: m.Retries})
		want, cause := string(tr.To), ""
		for _, o := range outages {
			if o.covers(hb.TS) {
				cause = o.cause
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

// Replay the actual sampled checks through the engine, independently of the
// seed's timestamp arithmetic, including failures between cadence boundaries.
func TestSeedConfirmationMatchesEngine(t *testing.T) {
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, retries := range []int{1, 2, 3} {
		for _, offset := range []time.Duration{0, 15 * time.Second} {
			for _, duration := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute} {
				t.Run(fmt.Sprintf("retries=%d/offset=%s/duration=%s", retries, offset, duration), func(t *testing.T) {
					m := store.Monitor{ID: 1, Retries: retries, IntervalS: 60}
					o := outage{start: base.Add(offset), end: base.Add(offset + duration)}
					beats := buildBeats(1, m, profile{}, base, base.Add(10*time.Minute), time.Minute, []outage{o}, plan{}, nil)
					engine := state.New(state.Options{})
					var firstFailure, confirmed, recovered time.Time
					for _, hb := range beats {
						tr := engine.Observe(state.Observation{MonitorID: 1, At: hb.TS, OK: hb.OK, FailureThreshold: retries})
						if hb.Assessment != string(tr.To) {
							t.Errorf("at %s: seed=%s engine=%s", hb.TS, hb.Assessment, tr.To)
						}
						if !hb.OK && firstFailure.IsZero() {
							firstFailure = hb.TS
						}
						if tr.Event == state.EventIncidentConfirmed {
							confirmed = hb.TS
						}
						if hb.OK && !firstFailure.IsZero() && recovered.IsZero() {
							recovered = hb.TS
						}
					}
					inc := incidentFor(1, o, m, time.Minute)
					if !inc.StartedAt.Equal(firstFailure) || !inc.ConfirmedAt.Equal(confirmed) || !inc.ResolvedAt.Equal(recovered) {
						t.Errorf("incident=%+v; engine start=%s confirmed=%s recovered=%s", inc, firstFailure, confirmed, recovered)
					}
				})
			}
		}
	}
}

func TestSeedCatalogueAssessmentsMatchEngine(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 15, 0, time.UTC)
	from := now.Add(-4 * 24 * time.Hour)
	for _, spec := range monitors() {
		t.Run(spec.monitor.Name, func(t *testing.T) {
			m := spec.monitor
			store.ApplyMonitorDefaults(&m)
			m.CreatedAt = now.Add(-spec.createdAgo)
			h := buildHistory(m, spec.profile, 1, plan{now: now, rawSince: from, bucketSince: from}, rand.New(rand.NewPCG(5, 6)))
			engine := state.New(state.Options{})
			confirmations := map[time.Time]bool{}
			for _, hb := range h.beats {
				tr := engine.Observe(state.Observation{MonitorID: 1, At: hb.TS, OK: hb.OK, FailureThreshold: m.Retries})
				if hb.Assessment != string(tr.To) {
					t.Fatalf("at %s: seed=%s engine=%s", hb.TS, hb.Assessment, tr.To)
				}
				if tr.Event == state.EventIncidentConfirmed {
					confirmations[hb.TS] = true
				}
			}
			for _, inc := range h.incidents {
				if inc.Confirmed() && !confirmations[inc.ConfirmedAt] {
					t.Fatalf("confirmation without an engine-confirmed sample: %+v", inc)
				}
				delete(confirmations, inc.ConfirmedAt)
			}
			if len(confirmations) != 0 {
				t.Fatalf("missing incidents for confirmed samples: %v", confirmations)
			}
		})
	}
}

func TestSeedBucketsUseActualSamples(t *testing.T) {
	from := time.Date(2026, 9, 20, 10, 0, 15, 0, time.UTC)
	to := from.Add(3*time.Hour + 15*time.Minute)
	for _, interval := range []time.Duration{time.Minute, 70 * time.Second, 90 * time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			m := store.Monitor{ID: 1, Retries: 2}
			outages := []outage{{start: from.Add(time.Minute), end: to.Add(-time.Minute)}}
			beats := buildBeats(1, m, profile{}, from, to, interval, outages, plan{}, nil)
			want := map[time.Time][3]int{}
			engine := state.New(state.Options{})
			for _, hb := range beats {
				tr := engine.Observe(state.Observation{MonitorID: 1, At: hb.TS, OK: hb.OK, FailureThreshold: m.Retries})
				hour := hb.TS.Truncate(time.Hour)
				counts := want[hour]
				switch tr.To {
				case state.StatusUp:
					counts[0]++
				case state.StatusDown:
					counts[1]++
				case state.StatusWarning:
					counts[2]++
				}
				want[hour] = counts
			}
			buckets := buildBuckets(1, m, profile{}, from, to, interval, outages, plan{})
			for _, b := range buckets {
				if got := ([3]int{b.AssessedUp, b.AssessedDown, b.Warning}); got != want[b.Bucket] {
					t.Errorf("%s: bucket %v, samples %v", b.Bucket, got, want[b.Bucket])
				}
				delete(want, b.Bucket)
			}
			if len(want) > 0 {
				t.Errorf("missing buckets: %v", want)
			}
		})
	}
}

func TestSeedDoesNotConfirmBeyondHistory(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 15, 0, time.UTC)
	from := now.Add(-time.Hour)
	m := store.Monitor{ID: 1, IntervalS: 60, Retries: 3}
	h := buildHistory(m, profile{outages: []outageSpec{{ago: 30 * time.Second, acked: true, reminders: 2}}}, 1, plan{now: now, rawSince: from, bucketSince: from}, rand.New(rand.NewPCG(1, 2)))
	if len(h.incidents) != 1 {
		t.Fatalf("missing sampled incident: %+v", h)
	}
	inc := h.incidents[0]
	if inc.Confirmed() || inc.Acked() || inc.ReminderCount != 0 || !inc.RemindedAt.IsZero() {
		t.Fatalf("future confirmation leaked lifecycle metadata: %+v", inc)
	}
}

func TestSeedUnconfirmedScenarioStaysBelowThreshold(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 15, 0, time.UTC)
	from := now.Add(-time.Hour)
	for _, retries := range []int{1, 3} {
		t.Run(fmt.Sprint(retries), func(t *testing.T) {
			m := store.Monitor{ID: 1, IntervalS: 60, Retries: retries}
			h := buildHistory(m, profile{outages: []outageSpec{{ago: 30 * time.Minute, dur: 6 * time.Minute, unconfirmed: true}}}, 1, plan{now: now, rawSince: from, bucketSince: from}, rand.New(rand.NewPCG(1, 2)))
			failures := 0
			for _, hb := range h.beats {
				if !hb.OK {
					failures++
				}
			}
			if failures >= retries || (retries > 1 && failures == 0) {
				t.Fatalf("unconfirmed scenario has %d failures at threshold %d", failures, retries)
			}
			for _, inc := range h.incidents {
				if inc.Confirmed() {
					t.Fatalf("unconfirmed scenario confirmed: %+v", inc)
				}
			}
		})
	}
}
