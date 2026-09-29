package store

import (
	"testing"
	"time"
)

func TestStatusHistoryHoursMergesRawAndRollup(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "api")
	other := seedMonitor(t, db, "other")

	base := time.Now().UTC().Truncate(time.Hour).Add(-48 * time.Hour)
	if err := db.SeedHourlyBuckets(ctx, []HourlyBucket{
		// One legacy check: counted in Up by the raw counts, never assessed.
		{MonitorID: id, Bucket: base, Up: 10, Down: 3, AssessedUp: 8, AssessedDown: 1, Warning: 1, Maintenance: 2},
		// Before the window: must not be returned.
		{MonitorID: id, Bucket: base.Add(-2 * time.Hour), Up: 1, AssessedUp: 1},
		{MonitorID: other, Bucket: base, Up: 5, AssessedUp: 5},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedHeartbeats(ctx, []Heartbeat{
		// Same hour as the bucket: the two halves add up.
		{MonitorID: id, TS: base.Add(10 * time.Minute), OK: true, Assessment: "up"},
		{MonitorID: id, TS: base.Add(70 * time.Minute), OK: false, Assessment: "down"},
		{MonitorID: id, TS: base.Add(75 * time.Minute), OK: false, Assessment: "warning"},
		{MonitorID: id, TS: base.Add(80 * time.Minute), OK: false, Assessment: "down", Maintenance: true},
		{MonitorID: id, TS: base.Add(82 * time.Minute), OK: true, Assessment: "up", Maintenance: true},
		{MonitorID: id, TS: base.Add(85 * time.Minute), OK: true},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.StatusHistoryHours(ctx, id, base.Add(-time.Hour+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := []StatusHistoryHour{
		{Hour: base, Up: 9, Down: 1, Warning: 1, Unassessed: 1, Maintenance: 2},
		{Hour: base.Add(time.Hour), Down: 1, Warning: 1, Unassessed: 1, Maintenance: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hours, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Hour.Equal(want[i].Hour) || got[i].Up != want[i].Up || got[i].Down != want[i].Down ||
			got[i].Warning != want[i].Warning || got[i].Unassessed != want[i].Unassessed || got[i].Maintenance != want[i].Maintenance {
			t.Errorf("hour %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestConfirmedIncidentSpans(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	id := seedMonitor(t, db, "api")
	since := time.Now().Add(-24 * time.Hour).Truncate(time.Second)

	// Resolved before the window: left out.
	mustIncident(t, db, id, since.Add(-3*time.Hour), since.Add(-2*time.Hour), true)
	// Straddles the start of the window: kept whole.
	straddle := mustIncident(t, db, id, since.Add(-time.Hour), since.Add(time.Hour), true)
	// Never confirmed: a blip, not an outage.
	mustIncident(t, db, id, since.Add(2*time.Hour), since.Add(3*time.Hour), false)
	// Still open.
	open := mustIncident(t, db, id, since.Add(4*time.Hour), time.Time{}, true)

	got, err := db.ConfirmedIncidentSpans(ctx, id, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(got), got)
	}
	if !got[0].Start.Equal(straddle.Start) || !got[0].End.Equal(straddle.End) {
		t.Errorf("first span = %+v, want %+v", got[0], straddle)
	}
	if !got[1].Start.Equal(open.Start) || !got[1].End.IsZero() {
		t.Errorf("second span = %+v, want open from %v", got[1], open.Start)
	}
}

// mustIncident opens an incident, optionally confirms it, and resolves it
// at end unless end is zero.
func mustIncident(t *testing.T, db *DB, monitorID int64, start, end time.Time, confirm bool) IncidentSpan {
	t.Helper()
	ctx := t.Context()
	if _, err := db.OpenIncident(ctx, monitorID, start, "", ""); err != nil {
		t.Fatal(err)
	}
	if confirm {
		if err := db.ConfirmIncident(ctx, monitorID, start.Add(time.Minute), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if !end.IsZero() {
		if _, err := db.ResolveIncident(ctx, monitorID, end); err != nil {
			t.Fatal(err)
		}
	}
	return IncidentSpan{Start: start.UTC(), End: end.UTC()}
}
