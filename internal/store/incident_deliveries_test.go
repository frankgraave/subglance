package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// An incident's delivery list is read from the outbox by what each message
// says, not by its incident_id column alone: a grouped message or a digest
// carries one id in that column and names the rest in its members.

func seedIncidentAt(t *testing.T, db *DB, monitor int64, started time.Time) Incident {
	t.Helper()
	inc, err := db.SeedIncident(context.Background(), Incident{
		MonitorID: monitor, StartedAt: started, ConfirmedAt: started.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SeedIncident: %v", err)
	}
	inc.StartedAt = started.UTC().Truncate(time.Second)
	return inc
}

func TestIncidentDeliveriesMatchesMembersAsWellAsTheColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := seedMonitor(t, db, "api")
	web := seedMonitor(t, db, "web")
	ch := healthChannel(t, db, "ops")

	start := time.Now().Add(-time.Hour)
	first := seedIncidentAt(t, db, api, start)
	second := seedIncidentAt(t, db, web, start)

	enqueue := func(incidentColumn int64, payload string) Delivery {
		t.Helper()
		d, err := db.SeedDelivery(ctx, Delivery{
			ChannelID: ch, MonitorID: api, IncidentID: incidentColumn,
			Event: "incident_confirmed", Payload: payload, CreatedAt: start.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatalf("SeedDelivery: %v", err)
		}
		return d
	}

	own := enqueue(first.ID, fmt.Sprintf(`{"incident_id":%d}`, first.ID))
	// A grouped message: the column holds the first incident only.
	grouped := enqueue(first.ID, fmt.Sprintf(
		`{"incident_id":%d,"members":[{"incident_id":%d},{"incident_id":%d}]}`,
		first.ID, first.ID, second.ID))
	// A digest names its incidents in its members and none at the top.
	digest := enqueue(first.ID, fmt.Sprintf(`{"members":[{"incident_id":%d}]}`, second.ID))
	enqueue(0, `{"incident_id":999}`) // another incident entirely
	// A payload that is not JSON falls back to the column, so it is listed
	// rather than hidden.
	corrupt := enqueue(second.ID, `not json`)

	got, err := db.IncidentDeliveries(ctx, second)
	if err != nil {
		t.Fatalf("IncidentDeliveries: %v", err)
	}
	var ids []int64
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	want := []int64{grouped.ID, digest.ID, corrupt.ID}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("deliveries for the second incident = %v, want %v (own row %d belongs to the first)", ids, want, own.ID)
	}

	got, err = db.IncidentDeliveries(ctx, first)
	if err != nil {
		t.Fatalf("IncidentDeliveries: %v", err)
	}
	if len(got) != 2 || got[0].ID != own.ID || got[1].ID != grouped.ID {
		t.Fatalf("deliveries for the first incident = %+v, want its own row and the grouped one", got)
	}
}

func TestIncidentDeliveriesSkipsRowsQueuedBeforeTheIncident(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "ops")
	start := time.Now().Add(-time.Hour)
	inc := seedIncidentAt(t, db, m, start)

	payload := fmt.Sprintf(`{"incident_id":%d}`, inc.ID)
	if _, err := db.SeedDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: "incident_confirmed", Payload: payload, CreatedAt: start.Add(-time.Second)}); err != nil {
		t.Fatalf("SeedDelivery: %v", err)
	}
	at, err := db.SeedDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: "incident_confirmed", Payload: payload, CreatedAt: start})
	if err != nil {
		t.Fatalf("SeedDelivery: %v", err)
	}
	got, err := db.IncidentDeliveries(ctx, inc)
	if err != nil {
		t.Fatalf("IncidentDeliveries: %v", err)
	}
	if len(got) != 1 || got[0].ID != at.ID {
		t.Fatalf("got %+v, want only the row queued at the incident's start", got)
	}
}

func TestMergedIntoReadsWhatTheFoldingWritesWrote(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	ch := healthChannel(t, db, "ops")

	enqueue := func(event string) Delivery {
		t.Helper()
		d, err := db.EnqueueDelivery(ctx, Delivery{ChannelID: ch, MonitorID: m, Event: event, Payload: "{}"})
		if err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
		return d
	}
	read := func(id int64) Delivery {
		t.Helper()
		d, err := db.GetDelivery(ctx, id)
		if err != nil {
			t.Fatalf("GetDelivery: %v", err)
		}
		return d
	}

	// Quiet hours fold a night's alerts into the first one to come due.
	keep, fold := enqueue("incident_confirmed"), enqueue("incident_resolved")
	if err := db.FoldDigest(ctx, keep.ID, "quiet_hours_digest", "{}", []int64{fold.ID}, time.Now()); err != nil {
		t.Fatalf("FoldDigest: %v", err)
	}
	if d := read(fold.ID); d.MergedInto() != MergedIntoDigest || d.Carrier() != keep.ID {
		t.Errorf("folded row: merged into %q, carried by %d; want %q and %d", d.MergedInto(), d.Carrier(), MergedIntoDigest, keep.ID)
	}
	if d := read(keep.ID); d.MergedInto() != "" || d.Carrier() != 0 {
		t.Errorf("the digest itself reads as merged: %q, %d", d.MergedInto(), d.Carrier())
	}

	// A recovery that arrives before its alert went out replaces it.
	alert, recovery := enqueue("incident_confirmed"), enqueue("incident_resolved")
	if err := db.ReplaceWithRecovery(ctx, recovery.ID, "{}", alert, []Replaced{{ID: alert.ID}}); err != nil {
		t.Fatalf("ReplaceWithRecovery: %v", err)
	}
	if d := read(alert.ID); d.MergedInto() != MergedIntoRecovery || d.Carrier() != recovery.ID {
		t.Errorf("replaced alert: merged into %q, carried by %d; want %q and %d", d.MergedInto(), d.Carrier(), MergedIntoRecovery, recovery.ID)
	}

	// Any other closed row is not merged, whatever its reason says.
	dropped := enqueue("incident_confirmed")
	if err := db.DropDelivery(ctx, dropped.ID); err != nil {
		t.Fatalf("DropDelivery: %v", err)
	}
	if d := read(dropped.ID); !d.Suppressed || d.MergedInto() != "" {
		t.Errorf("dropped row: suppressed %v, merged into %q; want suppressed and not merged", d.Suppressed, d.MergedInto())
	}
	// A pending row whose last error happens to start like a merge is not
	// merged: only a closed row is.
	retrying := enqueue("incident_confirmed")
	if err := db.MarkRetry(ctx, retrying.ID, foldedIntoDigest+"7", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	if d := read(retrying.ID); d.MergedInto() != "" {
		t.Errorf("a pending row reads as merged: %q", d.MergedInto())
	}
}

func TestGetIncident(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := seedMonitor(t, db, "api")
	inc := seedIncidentAt(t, db, m, time.Now().Add(-time.Hour))
	got, err := db.GetIncident(ctx, inc.ID)
	if err != nil || got.ID != inc.ID || !got.StartedAt.Equal(inc.StartedAt) {
		t.Fatalf("GetIncident = %+v, %v; want incident %d", got, err, inc.ID)
	}
	if _, err := db.GetIncident(ctx, inc.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetIncident(missing) error = %v, want ErrNotFound", err)
	}
}
