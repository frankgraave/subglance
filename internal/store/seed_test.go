package store

import (
	"context"
	"testing"
	"time"
)

func TestSeedHeartbeatsWritesSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	beats := []Heartbeat{
		{MonitorID: m.ID, TS: base, OK: true, LatencyMS: 120, StatusCode: 200},
		{MonitorID: m.ID, TS: base.Add(time.Minute), OK: false, StatusCode: 503,
			Error: "unexpected status 503",
			Response: &ResponseSnapshot{
				Body:    `{"error":"nope"}`,
				Headers: map[string]string{"Content-Type": "application/json"},
			}},
	}
	if err := db.SeedHeartbeats(ctx, beats); err != nil {
		t.Fatalf("seed heartbeats: %v", err)
	}

	got, err := db.ListHeartbeats(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("list heartbeats: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("stored %d heartbeats, want 2", len(got))
	}
	// Newest first, so the failure with the snapshot is first.
	if got[0].Response == nil {
		t.Fatal("the failing heartbeat lost its response snapshot")
	}
	if got[0].Response.Headers["Content-Type"] != "application/json" {
		t.Errorf("snapshot headers = %v", got[0].Response.Headers)
	}
	if got[1].Response != nil {
		t.Error("a passing heartbeat should not carry a snapshot")
	}
}

func TestSeedHourlyBucketsPreserveUnassessedHistory(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	// Well outside the raw window, which is the whole reason buckets are
	// written directly rather than rolled up from beats.
	hour := time.Now().Add(-30 * 24 * time.Hour).Truncate(time.Hour)
	buckets := []HourlyBucket{
		{MonitorID: m.ID, Bucket: hour, Up: 50, Down: 10,
			LatencyMin: 10, LatencyMax: 90, LatencyAvg: 40, LatencyCount: 60},
	}
	if err := db.SeedHourlyBuckets(ctx, buckets); err != nil {
		t.Fatalf("seed buckets: %v", err)
	}

	stats, err := db.Uptime(ctx, m.ID, 60*24*time.Hour)
	if err != nil {
		t.Fatalf("uptime: %v", err)
	}
	if stats.Total != 0 || stats.Legacy != 60 {
		t.Fatalf("uptime counts = %+v, want 60 legacy samples excluded from assessed uptime", stats)
	}
	if stats.AvgLatency != 40 {
		t.Errorf("average latency = %d, want 40", stats.AvgLatency)
	}

	// Seeding the same hour again must add to it, exactly as a second
	// rollup pass over late-arriving beats would.
	if err := db.SeedHourlyBuckets(ctx, buckets); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	stats, err = db.Uptime(ctx, m.ID, 60*24*time.Hour)
	if err != nil {
		t.Fatalf("uptime: %v", err)
	}
	if stats.Legacy != 120 {
		t.Errorf("total after a second pass = %d, want 120", stats.Legacy)
	}
}

func TestSeedIncidentKeepsEveryTimestamp(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	start := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
	in := Incident{
		MonitorID:   m.ID,
		StartedAt:   start,
		ConfirmedAt: start.Add(2 * time.Minute),
		AckedAt:     start.Add(9 * time.Minute),
		ResolvedAt:  start.Add(time.Hour),
		Cause:       "status",
		LastError:   "unexpected status 503",
	}
	if _, err := db.SeedIncident(ctx, in); err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	got, err := db.ListIncidents(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("stored %d incidents, want 1", len(got))
	}
	inc := got[0]
	if !inc.Confirmed() || !inc.Acked() || !inc.Resolved() {
		t.Fatalf("lifecycle flags lost: %+v", inc)
	}
	if !inc.ConfirmedAt.Equal(in.ConfirmedAt.UTC()) {
		t.Errorf("confirmed_at = %s, want %s", inc.ConfirmedAt, in.ConfirmedAt.UTC())
	}
	if inc.Duration() != time.Hour {
		t.Errorf("duration = %s, want 1h", inc.Duration())
	}
}

func TestSeedIncidentRefusesASecondOpenOne(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	now := time.Now()
	if _, err := db.SeedIncident(ctx, Incident{MonitorID: m.ID, StartedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("first incident: %v", err)
	}
	_, err = db.SeedIncident(ctx, Incident{MonitorID: m.ID, StartedAt: now.Add(-time.Minute)})
	if err == nil {
		t.Fatal("a second unresolved incident was accepted")
	}
}

func TestSeedDeliveryCarriesItsStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	ch, err := db.CreateChannel(ctx, Channel{
		Name: "webhook", Type: ChannelWebhook, Enabled: true,
		Config: map[string]string{"url": "https://hooks.example.com/x"},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	at := time.Now().Add(-2 * time.Hour)
	if _, err := db.SeedDelivery(ctx, Delivery{
		ChannelID: ch.ID, MonitorID: m.ID,
		Event: "incident_confirmed", Payload: "{}",
		Status: OutboxFailed, Attempts: 5, LastError: "no such host",
		CreatedAt: at, UpdatedAt: at.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	health, err := db.ChannelHealthSince(ctx, at.Add(-time.Hour))
	if err != nil {
		t.Fatalf("channel health: %v", err)
	}
	h, ok := health[ch.ID]
	if !ok {
		t.Fatal("the seeded delivery does not show up in channel health")
	}
	if h.Failed != 1 || h.LastError != "no such host" {
		t.Errorf("health = %+v, want one failure with its error", h)
	}
}

func TestSeedMonitorTimestampsBackdates(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m, err := db.CreateMonitor(ctx, Monitor{Name: "seeded", Type: "http", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	then := time.Now().Add(-200 * 24 * time.Hour).Truncate(time.Second)
	if err := db.SeedMonitorTimestamps(ctx, m.ID, then, then); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if !got.CreatedAt.Equal(then.UTC()) {
		t.Errorf("created_at = %s, want %s", got.CreatedAt, then.UTC())
	}

	if err := db.SeedMonitorTimestamps(ctx, m.ID+999, then, then); err == nil {
		t.Error("backdating a monitor that does not exist was allowed")
	}
}
