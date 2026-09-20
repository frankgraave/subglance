package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// resolveAt opens and immediately closes an incident on a monitor, so a test
// can lay out a resolution history without repeating six lines each time.
func resolveAt(t *testing.T, db *DB, monitorID int64, started, resolved time.Time) Incident {
	t.Helper()
	ctx := context.Background()

	if _, err := db.OpenIncident(ctx, monitorID, started, "status", "500"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	inc, err := db.ResolveIncident(ctx, monitorID, resolved)
	if err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
	return inc
}

// The whole point of the endpoint: one query, every monitor.
//
// The previous answer to this question was assembled in the browser, one
// request per monitor, capped at 24 — so the twenty-fifth monitor's outages
// simply were not in the history. Nothing here caps by monitor, and the
// assertion is deliberately about monitors rather than rows.
func TestListResolvedIncidentsSpansEveryMonitor(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	const monitors = 40

	want := make(map[int64]bool, monitors)
	for i := range monitors {
		m := mustCreateMonitor(t, db, "monitor-"+strconv.Itoa(i))
		want[m.ID] = true
		at := now.Add(-time.Duration(i+1) * time.Hour)
		resolveAt(t, db, m.ID, at, at.Add(time.Minute))
	}

	page, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), ResolvedIncidentCursor{}, monitors)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if len(page.Incidents) != monitors {
		t.Fatalf("got %d incidents, want %d", len(page.Incidents), monitors)
	}
	for _, inc := range page.Incidents {
		delete(want, inc.MonitorID)
	}
	if len(want) != 0 {
		t.Errorf("%d monitors missing from an instance-wide history", len(want))
	}
}

// Filtered on resolution, not on start.
//
// An outage that began five weeks ago and recovered yesterday is the row a
// reader most wants to find in a 30-day history, and it is the first one a
// started_at filter deletes. This is the bug the client had already corrected
// once; the server must not reintroduce it.
func TestListResolvedIncidentsFiltersOnResolution(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	long := mustCreateMonitor(t, db, "long")
	old := mustCreateMonitor(t, db, "old")

	// Began well outside the window, came back inside it.
	resolveAt(t, db, long.ID, now.AddDate(0, 0, -40), now.AddDate(0, 0, -1))
	// Began and ended outside it.
	resolveAt(t, db, old.ID, now.AddDate(0, 0, -50), now.AddDate(0, 0, -45))

	page, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), ResolvedIncidentCursor{}, 50)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if len(page.Incidents) != 1 {
		t.Fatalf("got %d incidents, want 1", len(page.Incidents))
	}
	if page.Incidents[0].MonitorID != long.ID {
		t.Errorf("kept monitor %d, want the long outage on %d",
			page.Incidents[0].MonitorID, long.ID)
	}
}

// Newest resolution first, and still open incidents never appear.
func TestListResolvedIncidentsOrderAndExclusions(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	a := mustCreateMonitor(t, db, "a")
	b := mustCreateMonitor(t, db, "b")
	stillDown := mustCreateMonitor(t, db, "still-down")

	resolveAt(t, db, a.ID, now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	resolveAt(t, db, b.ID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if _, err := db.OpenIncident(ctx, stillDown.ID, now.Add(-time.Minute), "status", "500"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	page, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), ResolvedIncidentCursor{}, 50)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if len(page.Incidents) != 2 {
		t.Fatalf("got %d incidents, want 2", len(page.Incidents))
	}
	if page.Incidents[0].MonitorID != b.ID {
		t.Error("newest resolution is not first")
	}
	for _, inc := range page.Incidents {
		if inc.MonitorID == stillDown.ID {
			t.Error("an open incident appeared in the resolved history")
		}
	}
}

// has_more is observed, not inferred — the distinction the old client could
// not make.
//
// A page that is exactly full and a page that is full because there is more
// behind it are the same list of rows. Reading one row past the page is what
// separates them, and it is why the store asks for limit+1 rather than
// comparing len() to the limit.
func TestListResolvedIncidentsExactlyFullPageIsNotTruncated(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	m := mustCreateMonitor(t, db, "chatty")
	for i := range 5 {
		at := now.Add(-time.Duration(i+1) * time.Hour)
		resolveAt(t, db, m.ID, at, at.Add(time.Minute))
	}

	full, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), ResolvedIncidentCursor{}, 5)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if len(full.Incidents) != 5 {
		t.Fatalf("got %d incidents, want 5", len(full.Incidents))
	}
	if full.HasMore {
		t.Error("a page holding the whole history reports more behind it")
	}
	if full.Next != (ResolvedIncidentCursor{}) {
		t.Error("a final page handed out a cursor to page past the end")
	}

	short, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), ResolvedIncidentCursor{}, 4)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if !short.HasMore {
		t.Error("a genuinely truncated page reports itself complete")
	}
	if short.Next.ID != short.Incidents[3].ID {
		t.Errorf("cursor points at incident %d, want the last row %d",
			short.Next.ID, short.Incidents[3].ID)
	}
}

// Walking the cursor visits every incident exactly once.
//
// Every resolution here lands in the same second, which is the case a
// timestamp-only cursor cannot survive: it would either loop on that second
// forever or step over the whole of it. Second granularity is what the schema
// stores, and a recovery sweep closing several monitors at once produces
// exactly this.
func TestListResolvedIncidentsCursorWalksTiesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	sameSecond := now.Add(-time.Hour)

	const total = 12
	for i := range total {
		m := mustCreateMonitor(t, db, "tied-"+strconv.Itoa(i))
		resolveAt(t, db, m.ID, sameSecond.Add(-time.Minute), sameSecond)
	}

	seen := make(map[int64]int, total)
	var cursor ResolvedIncidentCursor
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatal("paging did not terminate")
		}
		page, err := db.ListResolvedIncidents(ctx, now.AddDate(0, 0, -30), cursor, 5)
		if err != nil {
			t.Fatalf("ListResolvedIncidents: %v", err)
		}
		for _, inc := range page.Incidents {
			seen[inc.ID]++
		}
		if !page.HasMore {
			break
		}
		cursor = page.Next
	}

	if len(seen) != total {
		t.Errorf("paging visited %d of %d incidents", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("incident %d appeared %d times across pages", id, count)
		}
	}
}

// The window's lower bound is inclusive.
//
// A "last 30 days" card that drops the incident which recovered exactly 30
// days ago is wrong in the direction that matters: a missing outage reads as
// good news.
func TestListResolvedIncidentsWindowIsInclusive(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second)
	since := now.AddDate(0, 0, -30)
	m := mustCreateMonitor(t, db, "boundary")
	resolveAt(t, db, m.ID, since.Add(-time.Minute), since)

	page, err := db.ListResolvedIncidents(ctx, since, ResolvedIncidentCursor{}, 50)
	if err != nil {
		t.Fatalf("ListResolvedIncidents: %v", err)
	}
	if len(page.Incidents) != 1 {
		t.Fatalf("got %d incidents, want the one on the boundary", len(page.Incidents))
	}
}
