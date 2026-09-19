package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

type resolvedPageBody struct {
	Incidents  []incidentResponse `json:"incidents"`
	HasMore    bool               `json:"has_more"`
	NextCursor string             `json:"next_cursor"`
	Days       int                `json:"days"`
}

func getResolved(t *testing.T, srv *Server, query url.Values) resolvedPageBody {
	t.Helper()

	path := "/api/v1/incidents/resolved"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body = %s", path, rec.Code, rec.Body.String())
	}
	var body resolvedPageBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// seedResolved lays down one closed incident on a fresh monitor.
func seedResolved(t *testing.T, db *store.DB, name string, started, resolved time.Time) store.Monitor {
	t.Helper()
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: name, Type: "http", Target: "https://" + name + ".example.com",
		IntervalS: 60, TimeoutS: 10, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor(%s): %v", name, err)
	}
	if _, err := db.OpenIncident(ctx, m.ID, started, "status", "500"); err != nil {
		t.Fatalf("OpenIncident(%s): %v", name, err)
	}
	if _, err := db.ResolveIncident(ctx, m.ID, resolved); err != nil {
		t.Fatalf("ResolveIncident(%s): %v", name, err)
	}
	return m
}

// The endpoint answers for the whole instance, past the 24 monitors the
// browser's fan-out could reach.
func TestResolvedIncidentsCoverEveryMonitor(t *testing.T) {
	srv, db := testServerWithDB(t)

	now := time.Now().Truncate(time.Second)
	const monitors = 30
	for i := range monitors {
		at := now.Add(-time.Duration(i+1) * time.Hour)
		seedResolved(t, db, "m"+strconv.Itoa(i), at, at.Add(time.Minute))
	}

	body := getResolved(t, srv, url.Values{"limit": {"200"}})
	if len(body.Incidents) != monitors {
		t.Errorf("got %d incidents, want %d", len(body.Incidents), monitors)
	}
	if body.HasMore {
		t.Error("the whole history fits in the page but has_more says otherwise")
	}
	if body.NextCursor != "" {
		t.Error("a final page handed out a cursor")
	}
	if body.Days != 30 {
		t.Errorf("days = %d, want the default 30 echoed back", body.Days)
	}
}

// A client that pages to the end must be able to know it reached the end,
// and the rows must arrive exactly once.
//
// This is the whole contract that replaces the old `truncated` guess: paging
// terminates on has_more, and nothing is repeated or skipped on the way.
func TestResolvedIncidentsPaginateWithoutGapsOrRepeats(t *testing.T) {
	srv, db := testServerWithDB(t)

	now := time.Now().Truncate(time.Second)
	const total = 11
	for i := range total {
		at := now.Add(-time.Duration(i+1) * time.Minute)
		seedResolved(t, db, "m"+strconv.Itoa(i), at.Add(-time.Minute), at)
	}

	seen := make(map[int64]int, total)
	query := url.Values{"limit": {"4"}}
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatal("paging did not terminate")
		}
		body := getResolved(t, srv, query)
		for _, inc := range body.Incidents {
			seen[inc.ID]++
		}
		if !body.HasMore {
			if body.NextCursor != "" {
				t.Error("the last page still offered a cursor")
			}
			break
		}
		if body.NextCursor == "" {
			t.Fatal("has_more is true but no cursor was given to continue with")
		}
		query = url.Values{"limit": {"4"}, "cursor": {body.NextCursor}}
	}

	if len(seen) != total {
		t.Errorf("paging visited %d of %d incidents", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("incident %d appeared %d times", id, count)
		}
	}
}

// A cursor may pin the window, but it may not widen it past the cap.
//
// The bound moved onto the cursor so a walk could not have the floor rise
// under it — and a cursor is client-supplied text, so that same field is a way
// to ask for a window `days` would have refused. `days=99999` is rejected;
// `cursor=1.1.1` claimed a lower bound of 1970 and read the whole table, which
// is the cap removed by the mechanism that was meant to make paging honest.
//
// The fabricated bound is refused rather than clamped: clamping would answer a
// question nobody asked and call it the same walk.
func TestResolvedIncidentsRejectCursorsOlderThanTheCap(t *testing.T) {
	srv, _ := testServerWithDB(t)

	tooOld := time.Now().AddDate(0, 0, -(resolvedHistoryMaxDays + 1)).Unix()
	cursor := strconv.FormatInt(tooOld, 10) + ".1758024000.412"

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet,
			"/api/v1/incidents/resolved?cursor="+cursor, nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a cursor claiming a window past the cap was accepted: status = %d, body = %s",
			rec.Code, rec.Body.String())
	}
}

// The window a walk started with is the window every page of it uses.
//
// The lower bound moves — it is "now minus N days" — while paging descends
// toward it, so a bound recomputed per request rises under the walk. A row
// just inside the window when page one was served can be below the floor by
// the time the continuation arrives, and the walk then ends normally, reporting
// itself complete, one incident short. That is the failure this endpoint
// exists to remove, and it would have reappeared one level up.
func TestResolvedIncidentsPinTheWindowAcrossPages(t *testing.T) {
	srv, db := testServerWithDB(t)

	now := time.Now().Truncate(time.Second)

	// Two rows a day apart, both inside a 2-day window right now. The older
	// one sits close enough to the edge that a floor recomputed a day later
	// would exclude it.
	seedResolved(t, db, "recent", now.Add(-2*time.Hour), now.Add(-time.Hour))
	seedResolved(t, db, "edge", now.AddDate(0, 0, -2).Add(time.Hour),
		now.AddDate(0, 0, -2).Add(2*time.Hour))

	first := getResolved(t, srv, url.Values{"days": {"3"}, "limit": {"1"}})
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expected a cursor to continue with, got %+v", first)
	}

	/*
	 * The cursor carries the bound, so the continuation is asked with a
	 * *narrower* days than the walk began with. If the handler honoured
	 * `days` over the cursor, the older row would fall outside and the walk
	 * would end a row short — which is exactly the assertion.
	 */
	second := getResolved(t, srv, url.Values{
		"days":   {"1"},
		"limit":  {"1"},
		"cursor": {first.NextCursor},
	})
	if len(second.Incidents) != 1 {
		t.Fatalf("the walk lost a row when the window was recomputed: got %d incidents",
			len(second.Incidents))
	}
}

// The window is measured on resolution, so a long outage that recovered
// inside it is kept rather than dropped for having started too long ago.
func TestResolvedIncidentsKeepLongOutagesThatRecoveredInWindow(t *testing.T) {
	srv, db := testServerWithDB(t)

	now := time.Now().Truncate(time.Second)
	long := seedResolved(t, db, "long", now.AddDate(0, 0, -40), now.AddDate(0, 0, -2))
	seedResolved(t, db, "ancient", now.AddDate(0, 0, -60), now.AddDate(0, 0, -55))

	body := getResolved(t, srv, url.Values{"days": {"30"}})
	if len(body.Incidents) != 1 {
		t.Fatalf("got %d incidents, want 1", len(body.Incidents))
	}
	if body.Incidents[0].MonitorID != long.ID {
		t.Errorf("kept monitor %d, want the long outage on %d",
			body.Incidents[0].MonitorID, long.ID)
	}
}

// A bad parameter is refused rather than quietly reinterpreted.
//
// A malformed cursor silently treated as "start from the top" is how a client
// walks page one forever while believing it is reading a month of history —
// the same class of silent wrongness this endpoint exists to end.
func TestResolvedIncidentsRejectBadParameters(t *testing.T) {
	srv, _ := testServerWithDB(t)

	for _, q := range []string{
		"days=0", "days=9999", "days=soon",
		"limit=0", "limit=201", "limit=lots",
		"cursor=nonsense", "cursor=1758024000", "cursor=1758024000.412",
		"cursor=-1.2.3", "cursor=1755432000.1758024000.0",
		"cursor=0.1758024000.412", "cursor=1.2.3.4",
	} {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec,
			httptest.NewRequest(http.MethodGet, "/api/v1/incidents/resolved?"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("?%s: status = %d, want 400", q, rec.Code)
		}
	}
}

// Open incidents belong to the other endpoint, and an unresolved outage
// appearing under "resolved" would be the screen claiming a recovery that
// has not happened.
func TestResolvedIncidentsExcludeOpenOnes(t *testing.T) {
	ctx := context.Background()
	srv, db := testServerWithDB(t)

	now := time.Now().Truncate(time.Second)
	seedResolved(t, db, "recovered", now.Add(-2*time.Hour), now.Add(-time.Hour))

	broken, err := db.CreateMonitor(ctx, store.Monitor{
		Name: "broken", Type: "http", Target: "https://broken.example.com",
		IntervalS: 60, TimeoutS: 10, Retries: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if _, err := db.OpenIncident(ctx, broken.ID, now.Add(-time.Minute), "status", "500"); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	body := getResolved(t, srv, nil)
	for _, inc := range body.Incidents {
		if inc.MonitorID == broken.ID {
			t.Error("an open incident was listed as resolved")
		}
		if !inc.Resolved || inc.ResolvedAt == nil {
			t.Errorf("incident %d is in the resolved list without a resolution time", inc.ID)
		}
	}
}
