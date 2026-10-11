package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// absentFixture is the seeded instance with the on-call channel's quiet hours
// and the HTTP monitor's request headers and body removed: the state a file
// exported from it describes.
type absentFixture struct {
	srv     *Server
	db      *store.DB
	channel store.Channel
	monitor store.Monitor
}

func newAbsentFixture(t *testing.T) absentFixture {
	t.Helper()
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	ctx := context.Background()
	f := absentFixture{srv: srv, db: db}
	chans, err := db.ListChannels(ctx)
	must(t, err)
	for _, c := range chans {
		if c.Type == store.ChannelTelegram {
			f.channel = c
		}
	}
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	for _, m := range mons {
		if m.Name == "API (prod)" {
			f.monitor = m
		}
	}
	must(t, db.ClearQuietHours(ctx, f.channel.ID))
	f.monitor.Headers, f.monitor.Body = nil, ""
	f.monitor, err = db.UpdateMonitor(ctx, f.monitor)
	must(t, err)
	return f
}

// drift gives the channel and the monitor back what the file says they lack.
func (f absentFixture) drift(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must(t, f.db.SetQuietHours(ctx, store.QuietHours{ChannelID: f.channel.ID, Start: "23:00", End: "07:00",
		Timezone: "Europe/Amsterdam", During: store.QuietHold}))
	m := f.monitor
	m.Headers = map[string]string{"Authorization": "Bearer drifted"}
	m.Body = `{"drifted":true}`
	_, err := f.db.UpdateMonitor(ctx, m)
	must(t, err)
}

func changesOf(items []importItem, key string) []string {
	for _, it := range items {
		if it.Key == key {
			return it.Changes
		}
	}
	return nil
}

// TestImportRemovesWhatTheExportSaysIsAbsent is the convergence promise of a
// file kept in a repository: applying it makes the instance match it. A
// channel without quiet hours and an HTTP monitor without headers or a body
// used to export no key at all, which an import reads as "keep", so the
// target kept holding alerts overnight and kept sending the old header while
// the report said nothing had changed.
func TestImportRemovesWhatTheExportSaysIsAbsent(t *testing.T) {
	f := newAbsentFixture(t)
	exported := exportYAML(t, f.srv)
	for _, want := range []string{"quiet_hours: null", "headers: {}", `body: ""`} {
		if !strings.Contains(exported, want) {
			t.Fatalf("export lacks %q:\n%s", want, exported)
		}
	}
	f.drift(t)

	code, rep, body := importYAML(t, f.srv, exported, true)
	if code != http.StatusOK {
		t.Fatalf("dry run = %d: %s", code, body)
	}
	if got := changesOf(rep.Channels, "on-call"); !slices.Equal(got, []string{"quiet_hours"}) {
		t.Errorf("dry run channel changes = %v, want [quiet_hours]", got)
	}
	if got := changesOf(rep.Monitors, "api-prod"); !slices.Equal(got, []string{"body", "headers"}) {
		t.Errorf("dry run monitor changes = %v, want [body headers]", got)
	}

	code, _, body = importYAML(t, f.srv, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	ctx := context.Background()
	if _, has, err := f.db.GetQuietHours(ctx, f.channel.ID); err != nil || has {
		t.Errorf("quiet hours after import: present=%v err=%v, want removed", has, err)
	}
	after, err := f.db.GetMonitor(ctx, f.monitor.ID)
	must(t, err)
	if len(after.Headers) != 0 || after.Body != "" {
		t.Errorf("monitor after import sends headers %v and body %q, want neither", after.Headers, after.Body)
	}

	// Converged: the instance exports the same file, and a second import
	// changes nothing.
	if again := exportYAML(t, f.srv); again != exported {
		t.Errorf("export after import differs:\nbefore:\n%s\nafter:\n%s", exported, again)
	}
	_, rep, _ = importYAML(t, f.srv, exported, false)
	if rep.Summary.Update != 0 || rep.Summary.Create != 0 {
		t.Errorf("second import = %+v, want nothing to do", rep.Summary)
	}
}

// TestImportOmittedQuietHoursAndHeadersKeepTheirValues pins the other half:
// a hand-written file that does not mention them leaves them alone.
func TestImportOmittedQuietHoursAndHeadersKeepTheirValues(t *testing.T) {
	f := newAbsentFixture(t)
	exportYAML(t, f.srv) // assigns keys
	f.drift(t)

	doc := "version: 1\nchannels:\n  - key: on-call\n    name: On call duty\nmonitors:\n  - key: api-prod\n    name: API\n"
	code, rep, body := importYAML(t, f.srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if got := changesOf(rep.Channels, "on-call"); !slices.Equal(got, []string{"name"}) {
		t.Errorf("channel changes = %v, want [name]", got)
	}
	ctx := context.Background()
	if _, has, err := f.db.GetQuietHours(ctx, f.channel.ID); err != nil || !has {
		t.Errorf("quiet hours after an import that omitted them: present=%v err=%v", has, err)
	}
	after, err := f.db.GetMonitor(ctx, f.monitor.ID)
	must(t, err)
	if after.Headers["Authorization"] != "Bearer drifted" || after.Body != `{"drifted":true}` {
		t.Errorf("monitor after an import that omitted them sends %v / %q", after.Headers, after.Body)
	}
}

// TestImportQuietHoursNullOnANewChannel creates the channel without quiet
// hours and reports no change for them: there was nothing to remove.
func TestImportQuietHoursNullOnANewChannel(t *testing.T) {
	srv, db := testServerWithDB(t)
	doc := "version: 1\nchannels:\n  - key: hook\n    name: Hook\n    type: webhook\n" +
		"    config:\n      url: https://hook.example.com/x\n    quiet_hours: null\n"
	code, rep, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	if rep.Summary.Create != 1 || len(rep.Channels[0].Changes) != 0 {
		t.Errorf("report = %+v", rep)
	}
	q, err := db.ListQuietHours(context.Background())
	must(t, err)
	if len(q) != 0 {
		t.Errorf("quiet hours after creating a channel with quiet_hours: null = %v", q)
	}
}

func TestImportRefusesAMisspeltQuietHoursField(t *testing.T) {
	srv, db := testServerWithDB(t)
	doc := "version: 1\nchannels:\n  - key: hook\n    name: Hook\n    type: webhook\n" +
		"    config:\n      url: https://hook.example.com/x\n    quiet_hours:\n      start: \"23:00\"\n" +
		"      ends: \"07:00\"\n      timezone: UTC\n      during: hold\n"
	code, _, body := importYAML(t, srv, doc, false)
	if code != http.StatusBadRequest || !strings.Contains(body, "channels[0].quiet_hours") {
		t.Fatalf("import = %d: %s, want a 400 at channels[0].quiet_hours", code, body)
	}
	chans, err := db.ListChannels(context.Background())
	must(t, err)
	if len(chans) != 0 {
		t.Errorf("a refused file created %d channels", len(chans))
	}
}

func TestExportWritesHeadersAndBodyForHTTPMonitorsOnly(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedConfig(t, db)
	exported := exportYAML(t, srv)
	// Five monitors in the fixture, three of them HTTP.
	if n := strings.Count(exported, "    headers:"); n != 3 {
		t.Errorf("headers keys = %d, want one per HTTP monitor (3):\n%s", n, exported)
	}
	if n := strings.Count(exported, "    body:"); n != 3 {
		t.Errorf("body keys = %d, want one per HTTP monitor (3):\n%s", n, exported)
	}
	if n := strings.Count(exported, "    quiet_hours:"); n != 3 {
		t.Errorf("quiet_hours keys = %d, want one per channel (3):\n%s", n, exported)
	}
}
