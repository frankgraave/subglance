package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestPushFloodFromOneSourceDoesNotStarveRealReports reproduces the push-ingest
// flood.
//
// The pre-lookup limit exists so junk cannot reach the database, but a
// process-wide bucket means junk and real reports draw on the same budget.
// Emptying it turns every push monitor overdue, which is a dead man's switch
// inverted into an alarm generator.
func TestPushFloodFromOneSourceDoesNotStarveRealReports(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithPushRecorder(&fakePusher{})
	created := createPushMonitor(t, srv, pushMonitorBody)
	h := srv.Handler()

	for i := range 300 {
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/push/sgu_"+strconv.Itoa(i)+"0000000000000000000000000000", nil)
		req.RemoteAddr = "203.0.113.9:40000"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	// A real job, on a different host, reporting in right after the flood.
	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = "198.51.100.20:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("a valid push report got %d after a junk flood from another host, want 200: %s",
			rec.Code, rec.Body.String())
	}
}
