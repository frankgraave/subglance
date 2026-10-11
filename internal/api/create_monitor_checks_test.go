package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"
)

// Create and PATCH judge a monitor's name and retries the same way. Create
// used to skip both: an out-of-range retries reached the database and came
// back as a constraint error naming no field, and a name of spaces was stored
// as a row nobody can read.

func TestCreateMonitorRefusesRetriesOutOfRange(t *testing.T) {
	for _, n := range []int{-1, 11, 1000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"API","type":"http","target":"https://example.com","retries":`+strconv.Itoa(n)+`}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "retries", "retries must be between 0 and 10")
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

// A push monitor is created down a different branch of the handler; the
// range applies to it all the same.
func TestCreatePushMonitorRefusesRetriesOutOfRange(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"Backup","type":"push","push_interval_s":3600,"retries":11}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "retries", "retries must be between 0 and 10")
}

func TestCreateMonitorAcceptsTheRetriesBounds(t *testing.T) {
	for _, n := range []int{0, 10} {
		srv, db := testServerWithDB(t)
		rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
			`{"name":"API","type":"http","target":"https://example.com","retries":`+strconv.Itoa(n)+`}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("retries %d: status = %d, want 201: %s", n, rec.Code, rec.Body.String())
		}
		mons, err := db.ListMonitors(context.Background())
		must(t, err)
		if len(mons) != 1 || mons[0].Retries != n {
			t.Errorf("retries %d: stored %+v", n, mons)
		}
	}
}

func TestCreateMonitorRefusesABlankName(t *testing.T) {
	for name, value := range map[string]string{"spaces": `   `, "tab": `\t`, "newline": `\n `} {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
				`{"name":"`+value+`","type":"http","target":"https://example.com"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", "name is required")
			if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
				t.Errorf("a refused create stored %d monitors", len(mons))
			}
		})
	}
}

// The blank check trims; the stored name does not. A name keeps the spaces
// it was given, as it does under PATCH.
func TestCreateMonitorAcceptsANameWithSurroundingSpaces(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":" Public site ","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	mons, err := db.ListMonitors(context.Background())
	must(t, err)
	if len(mons) != 1 || mons[0].Name != " Public site " {
		t.Errorf("stored %+v, want one monitor named %q", mons, " Public site ")
	}
}

// PATCH already refused both; it now reads the same rule for retries, so the
// two can no longer drift apart.
func TestPatchMonitorRefusesRetriesOutOfRange(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"API","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = patch(t, srv, "/api/v1/monitors/1", `{"retries":11}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "retries", "retries must be between 0 and 10")
}
