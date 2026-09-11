package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// patchWithHeaders sends a PATCH carrying extra request headers, which is how
// a conditional update is expressed.
func patchWithHeaders(t *testing.T, srv *Server, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

func getMonitorRaw(t *testing.T, srv *Server, id int64) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	return rec
}

func monitorPath(id int64) string {
	return "/api/v1/monitors/" + strconv.FormatInt(id, 10)
}

func TestGetMonitorSetsWeakETag(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})

	rec := getMonitorRaw(t, srv, m.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got := rec.Header().Get("ETag")
	want := `W/"` + strconv.FormatInt(m.UpdatedAt.Unix(), 10) + `"`
	if got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
}

// The acceptance test from the ticket: two editors holding the same ETag, and
// only one of them may win.
func TestPatchMonitorSecondWriterWithStaleETagIsRejected(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	path := monitorPath(m.ID)

	etag := getMonitorRaw(t, srv, m.ID).Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET returned no ETag, so there is nothing to condition on")
	}

	first := patchWithHeaders(t, srv, path, `{"name": "first writer"}`,
		map[string]string{"If-Match": etag})
	if first.Code != http.StatusOK {
		t.Fatalf("first patch status = %d, want 200: %s", first.Code, first.Body.String())
	}

	second := patchWithHeaders(t, srv, path, `{"name": "second writer"}`,
		map[string]string{"If-Match": etag})
	if second.Code != http.StatusPreconditionFailed {
		t.Fatalf("second patch status = %d, want 412: %s", second.Code, second.Body.String())
	}
	if body := second.Body.String(); !strings.Contains(body, "modified by someone else") {
		t.Errorf("error body = %q, want it to say someone else modified the monitor", body)
	}

	// The whole point: the losing write left no trace.
	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != "first writer" {
		t.Errorf("name = %q, want %q — the rejected patch still wrote", got.Name, "first writer")
	}
}

// A successful conditional PATCH must hand back the next validator, or a
// client editing twice in a row has to re-GET purely to continue.
func TestPatchMonitorReturnsFreshETag(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	path := monitorPath(m.ID)

	etag := getMonitorRaw(t, srv, m.ID).Header().Get("ETag")

	first := patchWithHeaders(t, srv, path, `{"name": "one"}`,
		map[string]string{"If-Match": etag})
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", first.Code, first.Body.String())
	}

	next := first.Header().Get("ETag")
	if next == "" {
		t.Fatal("PATCH returned no ETag")
	}
	if next == etag {
		t.Errorf("ETag = %q, want it to differ from the pre-edit %q", next, etag)
	}

	// The returned tag must actually work for the following edit.
	second := patchWithHeaders(t, srv, path, `{"name": "two"}`,
		map[string]string{"If-Match": next})
	if second.Code != http.StatusOK {
		t.Fatalf("chained patch status = %d, want 200: %s", second.Code, second.Body.String())
	}
}

// Existing scripts send no If-Match at all, and must keep the behaviour they
// were written against.
func TestPatchMonitorWithoutIfMatchIsLastWriteWins(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	path := monitorPath(m.ID)

	for _, name := range []string{"first", "second"} {
		rec := patch(t, srv, path, `{"name": "`+name+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %q status = %d, want 200: %s", name, rec.Code, rec.Body.String())
		}
	}

	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != "second" {
		t.Errorf("name = %q, want %q — unconditional patches must not start failing", got.Name, "second")
	}
}

func TestPatchMonitorIfMatchValues(t *testing.T) {
	tests := []struct {
		name string
		// header is rendered with %s replaced by the monitor's current tag,
		// so a case can mix a live validator with literal text.
		header string
		want   int
	}{
		{name: "current weak tag", header: `W/"%s"`, want: http.StatusOK},
		{
			// RFC 9110 mandates strong comparison, under which a strong tag
			// never matches a weak one. Our tags are weak version stamps, so
			// the opaque part is compared and a client that dropped the W/
			// prefix is still served.
			name:   "strong form of the same tag",
			header: `"%s"`,
			want:   http.StatusOK,
		},
		{name: "star matches any existing monitor", header: `*`, want: http.StatusOK},
		{name: "list containing the current tag", header: `"nope", W/"%s"`, want: http.StatusOK},
		{name: "stale tag", header: `W/"1"`, want: http.StatusPreconditionFailed},
		{name: "list of stale tags", header: `W/"1", W/"2"`, want: http.StatusPreconditionFailed},
		{name: "unquoted value", header: `%s`, want: http.StatusBadRequest},
		{name: "unterminated quote", header: `W/"%s`, want: http.StatusBadRequest},
		{name: "star mixed into a list", header: `*, W/"%s"`, want: http.StatusBadRequest},
		{name: "trailing comma", header: `W/"%s",`, want: http.StatusBadRequest},
		{name: "empty value", header: ` `, want: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			m := seedMonitor(t, db, store.Monitor{
				Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
			})

			tag := strconv.FormatInt(m.UpdatedAt.Unix(), 10)
			header := strings.ReplaceAll(tc.header, "%s", tag)

			rec := patchWithHeaders(t, srv, monitorPath(m.ID), `{"name": "edited"}`,
				map[string]string{"If-Match": header})
			if rec.Code != tc.want {
				t.Fatalf("If-Match %q: status = %d, want %d: %s",
					header, rec.Code, tc.want, rec.Body.String())
			}

			got, err := db.GetMonitor(t.Context(), m.ID)
			if err != nil {
				t.Fatalf("GetMonitor: %v", err)
			}
			if tc.want == http.StatusOK {
				if got.Name != "edited" {
					t.Errorf("name = %q, want %q — an accepted patch did not write", got.Name, "edited")
				}
			} else if got.Name != "site" {
				t.Errorf("name = %q, want it untouched — a refused patch wrote anyway", got.Name)
			}
		})
	}
}

// A conditional PATCH on a monitor that no longer exists must not read as
// success.
func TestPatchMonitorIfMatchUnknownID(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := patchWithHeaders(t, srv, "/api/v1/monitors/9999", `{"name": "x"}`,
		map[string]string{"If-Match": `W/"1"`})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestParseIfMatch(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantTags []string
		wantStar bool
		wantOK   bool
	}{
		{name: "weak tag", value: `W/"abc"`, wantTags: []string{"abc"}, wantOK: true},
		{name: "strong tag", value: `"abc"`, wantTags: []string{"abc"}, wantOK: true},
		{name: "star", value: `*`, wantStar: true, wantOK: true},
		{
			name:     "list",
			value:    `W/"a", "b" ,W/"c"`,
			wantTags: []string{"a", "b", "c"},
			wantOK:   true,
		},
		{
			// A comma is legal inside a quoted tag, so a naive split would
			// tear this one in half and match neither piece.
			name:     "comma inside a tag",
			value:    `"a,b"`,
			wantTags: []string{"a,b"},
			wantOK:   true,
		},
		{name: "empty", value: ``, wantOK: false},
		{name: "whitespace only", value: `  `, wantOK: false},
		{name: "unquoted", value: `abc`, wantOK: false},
		{name: "unterminated", value: `"abc`, wantOK: false},
		{name: "star in a list", value: `*, "a"`, wantOK: false},
		{name: "trailing comma", value: `"a",`, wantOK: false},
		{name: "missing separator", value: `"a" "b"`, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tags, star, ok := parseIfMatch(tc.value)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v for %q", ok, tc.wantOK, tc.value)
			}
			if star != tc.wantStar {
				t.Errorf("star = %v, want %v for %q", star, tc.wantStar, tc.value)
			}
			if !tc.wantOK {
				return
			}
			if !reflect.DeepEqual(tags, tc.wantTags) {
				t.Errorf("tags = %v, want %v for %q", tags, tc.wantTags, tc.value)
			}
		})
	}
}

func TestIfMatchVersions(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []time.Time
	}{
		{name: "none", tags: nil, want: []time.Time{}},
		{
			name: "stamps",
			tags: []string{"0", "1757606400"},
			want: []time.Time{time.Unix(0, 0).UTC(), time.Unix(1757606400, 0).UTC()},
		},
		{
			// A tag this API never issued cannot match any row, so carrying it
			// into the SQL predicate would only widen the condition.
			name: "tags we never issued are dropped",
			tags: []string{"deadbeef", "1757606400", ""},
			want: []time.Time{time.Unix(1757606400, 0).UTC()},
		},
		{
			// An entity tag is opaque: a client may only return exactly what it
			// received. ParseInt would happily normalise these into a version we
			// did issue, granting meaning to spellings we never minted.
			name: "non-canonical spellings of a real stamp are dropped",
			tags: []string{"+1757606400", "01757606400", " 1757606400", "1757606400 "},
			want: []time.Time{},
		},
		{
			name: "negative and canonical are told apart",
			tags: []string{"-0", "0"},
			want: []time.Time{time.Unix(0, 0).UTC()},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ifMatchVersions(tc.tags)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("versions = %v, want %v", got, tc.want)
			}
		})
	}
}

// The real scenario the ticket describes is two editors racing, not two
// requests neatly ordered. Exactly one may win, and the loser must be told.
func TestPatchMonitorConcurrentWritersWithSameETag(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	path := monitorPath(m.ID)
	etag := getMonitorRaw(t, srv, m.ID).Header().Get("ETag")

	const writers = 8
	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		mu      sync.Mutex
		codes   []int
		winners []string
	)
	start.Add(1)
	for i := range writers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()

			name := "writer" + strconv.Itoa(i)
			rec := patchWithHeaders(t, srv, path, `{"name": "`+name+`"}`,
				map[string]string{"If-Match": etag})

			mu.Lock()
			defer mu.Unlock()
			codes = append(codes, rec.Code)
			if rec.Code == http.StatusOK {
				winners = append(winners, name)
			}
		}()
	}
	start.Done()
	done.Wait()

	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly 1 — the version check did not serialise the writers", winners)
	}
	for _, code := range codes {
		if code != http.StatusOK && code != http.StatusPreconditionFailed {
			t.Errorf("status = %d, want 200 or 412", code)
		}
	}

	got, err := db.GetMonitor(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != winners[0] {
		t.Errorf("name = %q, want %q — a losing writer still reached the row", got.Name, winners[0])
	}
}
