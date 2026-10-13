package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// TestCreatePushMonitorKeepsItsTimezone: a daily job's zone is stored, read
// back and returned, so the watchdog counts the window on that clock.
func TestCreatePushMonitorKeepsItsTimezone(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv,
		`{"name":"nightly","type":"push","push_interval_s":86400,"push_timezone":" Europe/Amsterdam "}`)

	if created.PushTimezone != "Europe/Amsterdam" {
		t.Errorf("response push_timezone = %q, want Europe/Amsterdam", created.PushTimezone)
	}
	m, err := db.GetMonitor(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if m.PushTimezone != "Europe/Amsterdam" {
		t.Errorf("stored push_timezone = %q", m.PushTimezone)
	}

	// Elapsed time is the default and is not spelled out.
	plain := createPushMonitor(t, srv, pushMonitorBody)
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+itoa(plain.ID), "")
	if strings.Contains(rec.Body.String(), "push_timezone") {
		t.Errorf("a monitor without a zone carries push_timezone: %s", rec.Body.String())
	}
}

func TestCreatePushMonitorRefusesABadTimezone(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"not whole days": {
			`{"name":"j","type":"push","push_interval_s":3600,"push_timezone":"Europe/Amsterdam"}`, "whole days",
		},
		"a day and an hour": {
			`{"name":"j","type":"push","push_interval_s":90000,"push_timezone":"Europe/Amsterdam"}`, "whole days",
		},
		"unknown zone": {
			`{"name":"j","type":"push","push_interval_s":86400,"push_timezone":"Mars/Olympus_Mons"}`, "unknown IANA timezone",
		},
		"the server's zone": {
			`{"name":"j","type":"push","push_interval_s":86400,"push_timezone":"Local"}`, "explicit IANA timezone",
		},
		"on an http monitor": {
			`{"name":"j","type":"http","target":"https://example.com","push_timezone":"Europe/Amsterdam"}`, "only to push monitors",
		},
		"empty on an http monitor": {
			`{"name":"j","type":"http","target":"https://example.com","push_timezone":""}`, "only to push monitors",
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors", tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "push_timezone", tc.want)
		})
	}
}

// TestPatchPushTimezone sets, refuses and clears the zone, and refuses an
// interval change that would leave a stored zone on an hourly window.
func TestPatchPushTimezone(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv, `{"name":"nightly","type":"push","push_interval_s":86400}`)
	path := "/api/v1/monitors/" + itoa(created.ID)
	stored := func() store.Monitor {
		t.Helper()
		m, err := db.GetMonitor(context.Background(), created.ID)
		if err != nil {
			t.Fatalf("GetMonitor: %v", err)
		}
		return m
	}

	mustStatus(t, doJSON(t, srv, http.MethodPatch, path, `{"push_timezone":"America/New_York"}`), http.StatusOK, "set zone")
	if got := stored().PushTimezone; got != "America/New_York" {
		t.Fatalf("zone = %q after PATCH", got)
	}

	rec := doJSON(t, srv, http.MethodPatch, path, `{"push_interval_s":3600}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "push_timezone", "whole days")
	if got := stored(); got.PushIntervalS != 86400 {
		t.Errorf("a refused PATCH changed the interval to %d", got.PushIntervalS)
	}

	rec = doJSON(t, srv, http.MethodPatch, path, `{"push_timezone":"Nowhere/Land"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "push_timezone", "unknown IANA timezone")

	// Clearing the zone and shortening the interval in one request is fine.
	mustStatus(t, doJSON(t, srv, http.MethodPatch, path, `{"push_timezone":"","push_interval_s":3600}`), http.StatusOK, "clear zone")
	if got := stored(); got.PushTimezone != "" || got.PushIntervalS != 3600 {
		t.Errorf("after clearing: zone %q, interval %d", got.PushTimezone, got.PushIntervalS)
	}

	site := seedMonitor(t, db, store.Monitor{Name: "site", Type: "http", Target: "https://example.com", Enabled: true})
	rec = doJSON(t, srv, http.MethodPatch, "/api/v1/monitors/"+itoa(site.ID), `{"push_timezone":"Europe/Amsterdam"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "push_timezone", "only to push monitors")
}

// TestConfigFileCarriesPushTimezone: export writes the zone, an import of the
// export changes nothing, and an import can set and refuse it like the API.
func TestConfigFileCarriesPushTimezone(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedMonitor(t, db, store.Monitor{Name: "Nightly backup", Type: store.TypePush,
		PushIntervalS: 86400, PushGraceS: 60, PushTimezone: "Europe/Amsterdam", Enabled: true})

	doc := exportYAML(t, srv)
	if !strings.Contains(doc, "push_timezone: Europe/Amsterdam") {
		t.Fatalf("export does not carry the zone:\n%s", doc)
	}
	code, rep, body := importYAML(t, srv, doc, true)
	if code != http.StatusOK {
		t.Fatalf("re-import = %d: %s", code, body)
	}
	for _, m := range rep.Monitors {
		if m.Action != actionUnchanged {
			t.Errorf("re-import of the export: %s is %s %v", m.Key, m.Action, m.Changes)
		}
	}

	cleared := strings.Replace(doc, "push_timezone: Europe/Amsterdam", `push_timezone: ""`, 1)
	code, rep, body = importYAML(t, srv, cleared, false)
	if code != http.StatusOK {
		t.Fatalf("import clearing the zone = %d: %s", code, body)
	}
	if len(rep.Monitors) != 1 || len(rep.Monitors[0].Changes) != 1 || rep.Monitors[0].Changes[0] != "push_timezone" {
		t.Errorf("clearing the zone reports %+v", rep.Monitors)
	}
	monitors, err := db.ListMonitors(context.Background())
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if monitors[0].PushTimezone != "" {
		t.Errorf("zone after an import cleared it = %q", monitors[0].PushTimezone)
	}

	hourly := strings.Replace(doc, "push_interval_s: 86400", "push_interval_s: 3600", 1)
	code, _, body = importYAML(t, srv, hourly, true)
	if code != http.StatusBadRequest || !strings.Contains(body, "push_timezone") || !strings.Contains(body, "whole days") {
		t.Errorf("import of a zone on an hourly window = %d: %s", code, body)
	}
}
