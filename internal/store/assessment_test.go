package store

import (
	"context"
	"testing"
	"time"
)

func TestAssessmentUptimeSurvivesRollup(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "assessed")
	now := time.Now()
	old := now.Add(-48 * time.Hour).Truncate(time.Hour)
	for i, a := range []string{"", "up", "warning", "down"} {
		hb := Heartbeat{MonitorID: id, TS: old.Add(time.Duration(i) * time.Second), OK: a == "up", Assessment: a, FailureKind: "status"}
		if err := db.RecordHeartbeat(ctx, hb); err != nil {
			t.Fatal(err)
		}
	}
	assert := func() {
		t.Helper()
		stats, err := db.Uptime(ctx, id, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Total != 2 || stats.Up != 1 || stats.Down != 1 || stats.Warning != 1 || stats.Legacy != 1 || stats.Percentage != 50 {
			t.Fatalf("assessment uptime = %+v", stats)
		}
	}
	assert()
	hbs, err := db.ListHeartbeats(ctx, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if hbs[1].Assessment != "warning" || hbs[1].OK || hbs[1].FailureKind != "status" {
		t.Fatalf("lost warning evidence: %+v", hbs[1])
	}
	if _, err := db.rollupAt(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	assert()
	// Late arrivals merge with the same semantics; raw observations are not rewritten.
	if err := db.RecordHeartbeat(ctx, Heartbeat{MonitorID: id, TS: old.Add(5 * time.Second), Assessment: "warning"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.rollupAt(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Uptime(ctx, id, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Warning != 2 || stats.Legacy != 1 {
		t.Fatalf("merged %+v", stats)
	}
	var rawUp, rawDown int
	if err := db.Reader.QueryRowContext(ctx, `SELECT up_count,down_count FROM heartbeat_hourly WHERE monitor_id=?`, id).Scan(&rawUp, &rawDown); err != nil {
		t.Fatal(err)
	}
	if rawUp != 1 || rawDown != 4 {
		t.Fatalf("raw history rewritten: %d/%d", rawUp, rawDown)
	}
}

func TestLegacyRollupIsNotManufacturedUptime(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := seedMonitor(t, db, "legacy")
	_, err := db.Writer.ExecContext(ctx, `INSERT INTO heartbeat_hourly(monitor_id,bucket,up_count,down_count,latency_count) VALUES(?,?,8,2,0)`, id, time.Now().Add(-2*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	stats, err := db.Uptime(ctx, id, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 || stats.Legacy != 10 {
		t.Fatalf("legacy counted as assessed: %+v", stats)
	}
}

func TestAssessmentMigrationPreservesOldHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "pre-assessment")
	for _, stmt := range []string{
		"ALTER TABLE heartbeats DROP COLUMN assessment",
		"ALTER TABLE heartbeats DROP COLUMN failure_kind",
		"ALTER TABLE heartbeat_hourly DROP COLUMN assessed_up",
		"ALTER TABLE heartbeat_hourly DROP COLUMN assessed_down",
		"ALTER TABLE heartbeat_hourly DROP COLUMN warning_count",
		"DELETE FROM schema_migrations WHERE name = '0012_heartbeat_assessment.sql'",
	} {
		if _, err := db.Writer.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer.ExecContext(ctx, "INSERT INTO heartbeats(monitor_id,ts,ok,error) VALUES(?,?,0,'original failure')", id, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.ExecContext(ctx, "INSERT INTO heartbeat_hourly(monitor_id,bucket,up_count,down_count,latency_count) VALUES(?,?,8,2,0)", id, time.Now().Add(-2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Uptime(ctx, id, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 0 || stats.Legacy != 11 {
		t.Fatalf("migration manufactured uptime: %+v", stats)
	}
	hb, err := db.LatestHeartbeat(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if hb.Assessment != "" || hb.OK || hb.Error != "original failure" {
		t.Fatalf("migration rewrote evidence: %+v", hb)
	}
}
