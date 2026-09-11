package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Every sub-resource of a monitor must answer "that monitor does not exist"
// with a 404. Two of them used to return 200 with an empty list, which is the
// same answer they give for "this monitor exists but has no data yet". A client
// polling a deleted monitor would keep receiving cheerful 200s and never learn
// it was querying a ghost — a monitoring tool quietly lying about a thing it
// cannot see at all.
//
// The list is deliberately exhaustive rather than a spot check: the bug was an
// inconsistency between endpoints, so the test only has value if adding a fifth
// sub-resource without a check shows up here.
func monitorSubresourcePaths(id int64) []string {
	return []string{
		fmt.Sprintf("/api/v1/monitors/%d/heartbeats", id),
		fmt.Sprintf("/api/v1/monitors/%d/incidents", id),
		fmt.Sprintf("/api/v1/monitors/%d/uptime", id),
		fmt.Sprintf("/api/v1/monitors/%d/channels", id),
	}
}

func TestMonitorSubresourcesReturn404ForUnknownMonitor(t *testing.T) {
	srv, _ := testServerWithDB(t)

	const unknownID = 424242
	for _, path := range monitorSubresourcePaths(unknownID) {
		t.Run(path, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodGet, path, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); !contains(got, "monitor not found") {
				t.Fatalf("body = %s, want it to say the monitor was not found", got)
			}
		})
	}
}

// The counterpart: a monitor that exists but has never been checked must still
// answer 200. Turning "no data" into a 404 would trade one confident lie for
// another, so the existence check has to be narrower than "the result is empty".
func TestMonitorSubresourcesReturn200ForKnownMonitorWithoutData(t *testing.T) {
	srv, db := testServerWithDB(t)

	m := seedMonitor(t, db, store.Monitor{
		Name: "fresh", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Retries: 3,
		Method: "GET", ExpectedStatus: "200-299", Enabled: true,
	})

	for _, path := range monitorSubresourcePaths(m.ID) {
		t.Run(path, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
