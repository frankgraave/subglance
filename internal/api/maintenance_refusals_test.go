package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// postMaintenance sends one create request and returns the recorder.
func postMaintenance(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	authedHandler(s).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/maintenance", body))
	return rec
}

const oneOff = `"starts_at":"2026-10-01T10:00:00Z","ends_at":"2026-10-01T11:00:00Z"`

// A tag written the way a monitor accepts it names the same group: the
// window is stored in the monitor's form, not refused for not being in it.
func TestCreateMaintenanceStoresATagTheWayAMonitorDoes(t *testing.T) {
	s, _ := testServerWithDB(t)
	rec := postMaintenance(t, s, `{"name":"  Deploy  ","tag_key":" Env ","tag_value":" prod ",`+oneOff+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", rec.Code, rec.Body)
	}
	windows, err := s.db.ListMaintenance(t.Context())
	must(t, err)
	if len(windows) != 1 {
		t.Fatalf("stored %d windows, want 1", len(windows))
	}
	got := windows[0]
	if got.Name != "Deploy" || got.TagKey != "env" || got.TagValue != "prod" {
		t.Errorf("stored name %q tag %q=%q, want \"Deploy\" env=prod", got.Name, got.TagKey, got.TagValue)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("stored window does not validate: %v", err)
	}
}

// The name limit is the one the schema and the form state: characters.
func TestCreateMaintenanceCountsTheNameInCharacters(t *testing.T) {
	s, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	name := strings.Repeat("é", 120)
	rec := postMaintenance(t, s, fmt.Sprintf(`{"name":%q,"monitor_id":%d,%s}`, name, id, oneOff))
	if rec.Code != http.StatusCreated {
		t.Fatalf("120 characters = %d, want 201: %s", rec.Code, rec.Body)
	}
	rec = postMaintenance(t, s, fmt.Sprintf(`{"name":%q,"monitor_id":%d,%s}`, name+"é", id, oneOff))
	wantStatusProblem(t, rec.Code, rec.Body.String(), "name", "1–120 characters")
}

// Each refusal names the setting at fault, so a form can put it beside the
// input; one that spans settings names none.
func TestCreateMaintenanceNamesTheFieldOfARefusal(t *testing.T) {
	s, db := testServerWithDB(t)
	id := seedUptimeMonitor(t, db)
	mon := fmt.Sprintf(`"name":"Deploy","monitor_id":%d`, id)
	weekly := func(extra string) string {
		return `{` + mon + `,"timezone":"UTC","weekdays":[0],"local_time":"02:00","duration_minutes":30` + extra + `}`
	}
	cases := map[string]struct{ body, field, want string }{
		"blank name":           {`{"name":"   ","monitor_id":` + fmt.Sprint(id) + `,` + oneOff + `}`, "name", "name must contain"},
		"negative monitor":     {`{"name":"Deploy","monitor_id":-1,` + oneOff + `}`, "monitor_id", "positive"},
		"no target":            {`{"name":"Deploy",` + oneOff + `}`, "", "exactly one monitor or tag pair"},
		"monitor and tag":      {`{` + mon + `,"tag_key":"env","tag_value":"prod",` + oneOff + `}`, "", "exactly one monitor or tag pair"},
		"value without key":    {`{"name":"Deploy","tag_value":"prod",` + oneOff + `}`, "tag_key", "tag_value requires tag_key"},
		"bad tag key":          {`{"name":"Deploy","tag_key":"-env","tag_value":"prod",` + oneOff + `}`, "tag_key", "tag key"},
		"tag without value":    {`{"name":"Deploy","tag_key":"env","tag_value":"  ",` + oneOff + `}`, "tag_value", "must have a value"},
		"no start":             {`{` + mon + `,"ends_at":"2026-10-01T11:00:00Z"}`, "starts_at", "starts_at before ends_at"},
		"end before start":     {`{` + mon + `,"starts_at":"2026-10-01T10:00:00Z","ends_at":"2026-10-01T10:00:00Z"}`, "ends_at", "starts_at before ends_at"},
		"recurrence, no zone":  {`{` + mon + `,` + oneOff + `,"local_time":"02:00"}`, "timezone", "recurrence requires timezone"},
		"unknown zone":         {`{` + mon + `,"timezone":"Mars/Olympus","weekdays":[0],"local_time":"02:00","duration_minutes":30}`, "timezone", "unknown IANA timezone"},
		"Local zone":           {`{` + mon + `,"timezone":"Local","weekdays":[0],"local_time":"02:00","duration_minutes":30}`, "timezone", "explicit IANA timezone"},
		"weekly with start":    {weekly(`,"starts_at":"2026-10-01T10:00:00Z"`), "starts_at", "absolute start/end"},
		"weekly with end":      {weekly(`,"ends_at":"2026-10-01T10:00:00Z"`), "ends_at", "absolute start/end"},
		"bad local time":       {`{` + mon + `,"timezone":"UTC","weekdays":[0],"local_time":"2:00","duration_minutes":30}`, "local_time", "HH:MM"},
		"no weekdays":          {`{` + mon + `,"timezone":"UTC","weekdays":[],"local_time":"02:00","duration_minutes":30}`, "weekdays", "choose weekdays"},
		"repeated weekday":     {`{` + mon + `,"timezone":"UTC","weekdays":[1,1],"local_time":"02:00","duration_minutes":30}`, "weekdays", "unique integers"},
		"weekday out of range": {`{` + mon + `,"timezone":"UTC","weekdays":[7],"local_time":"02:00","duration_minutes":30}`, "weekdays", "unique integers"},
		"long duration":        {`{` + mon + `,"timezone":"UTC","weekdays":[0],"local_time":"02:00","duration_minutes":1441}`, "duration_minutes", "1–1440 minutes"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postMaintenance(t, s, tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
		})
	}
	windows, err := db.ListMaintenance(t.Context())
	must(t, err)
	if len(windows) != 0 {
		t.Errorf("refused requests stored %d windows", len(windows))
	}
}

// A file that writes a tag as a monitor would accept it matches the window
// the API stored, and a bad window is reported at the field that is wrong.
func TestImportMaintenanceNormalisesTheTagAndNamesTheField(t *testing.T) {
	s, db := testServerWithDB(t)
	if rec := postMaintenance(t, s, `{"name":"Deploy","tag_key":"env","tag_value":"prod",`+oneOff+`}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed window = %d: %s", rec.Code, rec.Body)
	}
	doc := "version: 1\nmaintenance:\n  - name: ' Deploy '\n    tag_key: Env\n    tag_value: ' prod'\n" +
		"    starts_at: 2026-10-01T10:00:00Z\n    ends_at: 2026-10-01T11:00:00Z\n"
	code, report, body := importYAML(t, s, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if len(report.Maintenance) != 1 || report.Maintenance[0].Action != actionUnchanged {
		t.Errorf("maintenance report = %+v, want one unchanged window", report.Maintenance)
	}
	windows, err := db.ListMaintenance(t.Context())
	must(t, err)
	if len(windows) != 1 {
		t.Errorf("import added a window: %d stored, want 1", len(windows))
	}

	for doc, field := range map[string]string{
		"version: 1\nmaintenance:\n  - name: W\n    tag_key: env\n    tag_value: prod\n    timezone: UTC\n" +
			"    weekdays: [0]\n    local_time: '2:00'\n    duration_minutes: 30\n": "maintenance[0].local_time",
		"version: 1\nmaintenance:\n  - name: W\n" +
			"    starts_at: 2026-10-01T10:00:00Z\n    ends_at: 2026-10-01T11:00:00Z\n": "maintenance[0]",
	} {
		code, _, body := importYAML(t, s, doc, false)
		wantStatusProblem(t, code, body, field, "")
	}
}

// A stored window that is not in normal form is refused by Validate rather
// than read as some other group.
func TestMaintenanceValidateRefusesAStoredTagNotInNormalForm(t *testing.T) {
	w := store.MaintenanceWindow{Name: "Deploy", TagKey: "Env", TagValue: "prod",
		Timezone: "UTC", Weekdays: []int{0}, LocalTime: "02:00", DurationMinutes: 30}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate accepted tag key \"Env\"")
	}
}
