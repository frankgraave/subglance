package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A quiet-hours refusal names the field it is about, on the PUT route and in
// an import, so a form or an import report can point at the value that is
// wrong instead of at the whole window.
var quietRefusals = []struct {
	name, body, yaml, field, want string
}{
	{"start", `{"start":"23","end":"07:00","timezone":"UTC"}`,
		"      start: \"23\"\n      end: \"07:00\"\n      timezone: UTC\n", "start", "start must be HH:MM"},
	{"end", `{"start":"23:00","end":"7:00","timezone":"UTC"}`,
		"      start: \"23:00\"\n      end: \"7:00\"\n      timezone: UTC\n", "end", "end must be HH:MM"},
	{"equal bounds", `{"start":"23:00","end":"23:00","timezone":"UTC"}`,
		"      start: \"23:00\"\n      end: \"23:00\"\n      timezone: UTC\n", "end", "start and end must differ"},
	{"no timezone", `{"start":"23:00","end":"07:00","timezone":" "}`,
		"      start: \"23:00\"\n      end: \"07:00\"\n      timezone: \" \"\n", "timezone", "explicit IANA name"},
	{"unknown timezone", `{"start":"23:00","end":"07:00","timezone":"Mars/Base"}`,
		"      start: \"23:00\"\n      end: \"07:00\"\n      timezone: Mars/Base\n", "timezone", "unknown IANA timezone"},
	{"during", `{"start":"23:00","end":"07:00","timezone":"UTC","during":"later"}`,
		"      start: \"23:00\"\n      end: \"07:00\"\n      timezone: UTC\n      during: later\n", "during", "during must be hold or drop"},
}

const quietImportHead = "version: 1\nchannels:\n  - key: hook\n    name: Hook\n    type: webhook\n" +
	"    config:\n      url: https://hook.example.com/x\n    quiet_hours:\n"

func TestSetQuietHoursRefusalNamesTheField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	ch := createSlackChannel(t, srv, "phone", "https://hooks.slack.com/services/T/B/secret")
	path := fmt.Sprintf("/api/v1/channels/%d/quiet-hours", ch.ID)
	for _, tc := range quietRefusals {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPut, path, tc.body)
			wantStatusProblem(t, rec.Code, rec.Body.String(), tc.field, tc.want)
		})
	}
}

func TestImportQuietHoursRefusalNamesTheField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	for _, tc := range quietRefusals {
		t.Run(tc.name, func(t *testing.T) {
			code, _, body := importYAML(t, srv, quietImportHead+tc.yaml, true)
			wantStatusProblem(t, code, body, "channels[0].quiet_hours."+tc.field, tc.want)
		})
	}
}

// The PUT route stores a window without `during` as hold; a file that says
// the same thing must import, not be refused for a value the API defaults.
// The values are padded the way a hand-edited file can be, and must be
// stored the way the PUT route stores them.
func TestImportQuietHoursFollowsThePutRouteRules(t *testing.T) {
	srv, db := testServerWithDB(t)
	doc := quietImportHead + "      start: \" 23:00\"\n      end: \"07:00 \"\n      timezone: \" Europe/Amsterdam\"\n"
	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if rep.Summary.Create != 1 {
		t.Fatalf("report = %+v", rep)
	}
	all, err := db.ListQuietHours(context.Background())
	must(t, err)
	if len(all) != 1 {
		t.Fatalf("quiet hours after import = %v, want one window", all)
	}
	for _, q := range all {
		want := store.QuietHours{ChannelID: q.ChannelID, Start: "23:00", End: "07:00",
			Timezone: "Europe/Amsterdam", During: store.QuietHold}
		if q != want {
			t.Errorf("stored %+v, want %+v", q, want)
		}
	}

	// Importing the same file again changes nothing: the stored window and
	// the file's, normalised, are the same window.
	code, rep, body = importYAML(t, srv, doc, true)
	if code != http.StatusOK {
		t.Fatalf("second import = %d: %s", code, body)
	}
	if got := changesOf(rep.Channels, "hook"); len(got) != 0 {
		t.Errorf("second import reports changes %v, want none", got)
	}
}
