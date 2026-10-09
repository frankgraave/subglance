package kumaimport

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"
)

// The fixtures were written by Uptime Kuma itself, 1.23.16 and 2.5.5, through
// its own socket API: eight channels, sixteen monitors (fifteen on 1.23, which
// has no numeric JSON operators), tags, a group, two status pages, the server
// time zone Europe/Amsterdam, and fourteen maintenance windows covering every
// schedule Kuma offers. The public page has two sections, five supported
// monitors, page branding, analytics, a custom domain, an incident and a
// maintenance announcement; the other page uses the reserved slug "api".
// Every credential in them contains "kuma-secret".
// Heartbeats, statistics and the user row were removed afterwards; nothing
// the converter reads was edited.
var fixtures = []string{"kuma-1.23.16.db", "kuma-2.5.5.db"}

func convertFixture(t *testing.T, name string) Result {
	t.Helper()
	res, err := Convert(context.Background(), filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func monitorByName(res Result, name string) (configfile.Monitor, bool) {
	for _, m := range res.Document.Monitors {
		if m.Name == name {
			return m, true
		}
	}
	return configfile.Monitor{}, false
}

func channelByName(res Result, name string) (configfile.Channel, bool) {
	for _, c := range res.Document.Channels {
		if c.Name == name {
			return c, true
		}
	}
	return configfile.Channel{}, false
}

func TestConvertBothSchemas(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			res := convertFixture(t, fixture)
			wantSchema := "1.x"
			if strings.HasPrefix(fixture, "kuma-2") {
				wantSchema = "2.x"
			}
			if res.Schema != wantSchema {
				t.Errorf("schema = %s, want %s", res.Schema, wantSchema)
			}

			shop, ok := monitorByName(res, "Shop (prod)")
			if !ok {
				t.Fatal("Shop (prod) missing")
			}
			if shop.Key != "shop-prod" || shop.Type != "http" || shop.Target != "https://shop.example.com/health" ||
				deref(shop.IntervalS) != 30 || deref(shop.Retries) != 4 || deref(shop.TimeoutS) != 24 ||
				!deref(shop.Enabled) || deref(shop.ExpectedStatus) != "200-299" {
				t.Errorf("Shop (prod) = %+v", shop)
			}
			if shop.Tags["env"] != "prod" || shop.Tags["critical"] != "yes" || len(shop.Tags) != 2 {
				t.Errorf("Shop tags = %v", shop.Tags)
			}
			if !slices.Equal(shop.Channels, []string{"discord-ops", "mail-admins"}) {
				t.Errorf("Shop channels = %v", shop.Channels)
			}

			post, _ := monitorByName(res, "API POST")
			if deref(post.Method) != "POST" || deref(post.ExpectedStatus) != "200-299,301" || deref(post.FollowRedirects) ||
				post.Headers["X-Api-Key"] != configfile.Placeholder || deref(post.Body) != configfile.Placeholder ||
				post.Headers["Content-Type"] != "application/json" || len(post.Headers) != 2 {
				t.Errorf("API POST = %+v", post)
			}
			intranet, _ := monitorByName(res, "Intranet")
			if intranet.Headers["Authorization"] != configfile.Placeholder {
				t.Errorf("basic auth did not become a withheld Authorization header: %v", intranet.Headers)
			}
			kw, _ := monitorByName(res, "Blog keyword")
			if deref(kw.Keyword) != "Welcome" || deref(kw.KeywordMode) != "must_contain" {
				t.Errorf("keyword monitor = %+v", kw)
			}
			inv, _ := monitorByName(res, "No error page")
			if deref(inv.KeywordMode) != "must_not_contain" {
				t.Errorf("inverted keyword mode = %q", deref(inv.KeywordMode))
			}
			js, _ := monitorByName(res, "Status JSON")
			if a, set, err := js.Assertion("x"); err != nil || !set || a == nil ||
				a.Path != "status" || a.Operator != "equals" || a.Expected != `"ok"` {
				t.Errorf("json assertion = %+v %v %v", a, set, err)
			}
			tcp, _ := monitorByName(res, "Postgres")
			if tcp.Type != "tcp" || tcp.Target != "db.example.com:5432" || deref(tcp.IntervalS) != 120 {
				t.Errorf("port monitor = %+v", tcp)
			}
			ping, _ := monitorByName(res, "Router")
			if ping.Type != "ping" || ping.Target != "router.example.net" {
				t.Errorf("ping monitor = %+v", ping)
			}
			// Shop (prod) is checked every 30s and retried every 60s, three
			// times: Kuma alerted on the fourth failure, 180s after the
			// first. SubGlance confirms on the fourth too, 90s in.
			if notes := notesOf(res); !strings.Contains(notes, "Shop (prod)") ||
				!strings.Contains(notes, "Kuma alerted 180s after the first failed check, retrying every 60s; "+
					"SubGlance checks again at the 30s interval and alerts after 90s") {
				t.Errorf("the changed time to an alert is not listed: %s", notes)
			}
			push, _ := monitorByName(res, "Nightly backup")
			if push.Type != "push" || push.Target != "" || deref(push.PushIntervalS) != 86400 || push.IntervalS != nil ||
				deref(push.Retries) != 0 || push.PushGraceS != nil {
				t.Errorf("push monitor = %+v", push)
			}
			old, _ := monitorByName(res, "Old site")
			if deref(old.Enabled) {
				t.Error("a monitor paused in Kuma came over enabled")
			}
			cdn, _ := monitorByName(res, "CDN")
			if cdn.Tags["group"] != "Edge" {
				t.Errorf("a monitor in a group did not get the group tag: %v", cdn.Tags)
			}

			if len(res.Document.Channels) != 8 {
				t.Errorf("channels = %d, want 8", len(res.Document.Channels))
			}
			po, _ := channelByName(res, "Pushover")
			if po.Type != "webhook" || po.Config["url"] != configfile.Placeholder || po.Config["body"] != pushoverBody {
				t.Errorf("pushover channel = %+v", po)
			}
			mail, _ := channelByName(res, "Mail admins")
			if mail.Type != "email" || mail.Config["to"] != "ops@example.com, oncall@example.com" ||
				mail.Config["host"] != "smtp.example.com" || mail.Config["port"] != "587" ||
				mail.Config["password"] != configfile.Placeholder {
				t.Errorf("email channel = %+v", mail)
			}
			tg, _ := channelByName(res, "Telegram on-call")
			if tg.Config["chat_id"] != "-1001234" || tg.Config["bot_token"] != configfile.Placeholder {
				t.Errorf("telegram channel = %+v", tg)
			}
			ntfy, _ := channelByName(res, "ntfy phone")
			if ntfy.Config["url"] != "https://ntfy.example.com" || ntfy.Config["topic"] != configfile.Placeholder ||
				ntfy.Config["username"] != "kuma" || ntfy.Config["password"] != configfile.Placeholder {
				t.Errorf("ntfy channel = %+v", ntfy)
			}
		})
	}
}

// The windows Kuma wrote come over as the windows its schedules describe.
func TestConvertWindowsFromBothSchemas(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	move := time.Date(2096, 3, 1, 22, 0, 0, 0, berlin)
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			res := convertFixture(t, fixture)
			var got []string
			for _, m := range res.Document.Maintenance {
				line := m.Name + " " + m.Monitor + m.TagKey + "=" + m.TagValue
				if m.StartsAt != nil {
					line += " " + m.StartsAt.Format(time.RFC3339) + "/" + m.EndsAt.Format(time.RFC3339)
				} else {
					line += " " + m.Timezone + " " + fmt.Sprint(m.Weekdays) + " " + m.LocalTime + " " + strconv.Itoa(m.DurationMinutes)
				}
				got = append(got, line)
			}
			want := []string{
				"Nightly deploy shop-prod= Europe/Amsterdam [1 2 3 4 5] 02:00 60",
				"Nightly deploy api-post= Europe/Amsterdam [1 2 3 4 5] 02:00 60",
				"Weekend backup group=Edge America/New_York [0 6] 23:30 90",
				"Daily rotate status-json= UTC [0 1 2 3 4 5 6] 04:00 15",
				"Sunday cron postgres= Europe/Amsterdam [0] 03:30 45",
				"Datacenter move postgres= " + move.UTC().Format(time.RFC3339) + "/" + move.Add(6*time.Hour).UTC().Format(time.RFC3339),
				"Thursday reboot router= Europe/Amsterdam [4] 05:00 30",
			}
			if !slices.Equal(got, want) {
				t.Errorf("windows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if res.Windows != 14 || res.WindowsConverted != 6 {
				t.Errorf("windows read %d, converted %d; want 14 and 6", res.Windows, res.WindowsConverted)
			}
		})
	}
}

// Nothing falls away silently: every Kuma monitor and channel is either in
// the document or in the skipped list, by name.
func TestEveryObjectIsAccountedFor(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			res := convertFixture(t, fixture)
			skipped := map[string]string{}
			for _, n := range res.Skipped {
				skipped[n.Kind+"/"+n.Name] = n.Reason
			}
			if got := len(res.Document.Monitors) + countKind(res.Skipped, "monitor"); got != res.Monitors {
				t.Errorf("%d monitors converted or skipped, %d in Kuma", got, res.Monitors)
			}
			if got := len(res.Document.Channels) + countKind(res.Skipped, "channel"); got != res.Channels {
				t.Errorf("%d channels converted or skipped, %d in Kuma", got, res.Channels)
			}
			if got := len(res.Document.StatusPages) + countKind(res.Skipped, "status page"); got != res.StatusPages || res.StatusPages == 0 {
				t.Errorf("%d status pages converted or skipped, %d in Kuma", got, res.StatusPages)
			}
			// A window can become several, one per monitor, so windows are
			// counted by the Kuma windows that came over.
			if got := res.WindowsConverted + countKind(res.Skipped, "maintenance"); got != res.Windows || res.Windows == 0 {
				t.Errorf("%d maintenance windows converted or skipped, %d in Kuma", got, res.Windows)
			}
			for _, name := range []string{"monitor/Container web", "monitor/Edge",
				"monitor/Maintenance flag", "status page/API status", "maintenance/Every third day",
				"maintenance/Monthly patch", "maintenance/Six-hourly cron", "maintenance/Past migration",
				"maintenance/Manual hold", "maintenance/Paused window", "maintenance/Summer freeze",
				"maintenance/Docker only"} {
				if skipped[name] == "" {
					t.Errorf("%s is not listed with a reason; skipped = %v", name, skipped)
				}
			}
		})
	}
}

func countKind(notes []Note, kind string) int {
	n := 0
	for _, x := range notes {
		if x.Kind == kind {
			n++
		}
	}
	return n
}

// A converted file is meant to be read, kept and passed around before it is
// imported. None of Kuma's credentials may be in it.
func TestRenderWithholdsEveryCredential(t *testing.T) {
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			out, err := Render(convertFixture(t, fixture))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(out, []byte("kuma-secret")) {
				t.Errorf("a credential reached the file:\n%s", out)
			}
			doc, err := configfile.Parse(out)
			if err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			if len(doc.Monitors) == 0 {
				t.Error("the rendered file has no monitors")
			}
			if !bytes.Contains(out, []byte("# Not imported from Uptime Kuma:")) ||
				!bytes.Contains(out, []byte(`monitor "Container web" (docker): SubGlance has no Docker container check`)) {
				t.Errorf("the report is missing from the file:\n%s", out)
			}
		})
	}
}

// Kuma 2.x has numeric JSON operators; 1.23 always compared for equality.
func TestNumericJSONQuery(t *testing.T) {
	res := convertFixture(t, "kuma-2.5.5.db")
	m, ok := monitorByName(res, "Queue depth")
	if !ok {
		t.Fatal("Queue depth missing")
	}
	a, _, err := m.Assertion("x")
	if err != nil || a == nil || a.Path != "depth" || a.Operator != "less_than" || a.Expected != "100" {
		t.Errorf("assertion = %+v, %v", a, err)
	}
}

// The database is opened read-only and nothing is created beside it, so the
// command can run against the data directory of a Kuma that is running, or
// stopped, or mounted read-only.
func TestConvertLeavesTheDatabaseAlone(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.ReadFile(filepath.Join("testdata", fixtures[1]))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kuma.db")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	// As a stopped Kuma leaves it: WAL mode in the header, no -wal or -shm
	// file. A plain read-only open would create both.
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := w.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	_ = w.Close()
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	orig, _ = os.ReadFile(path)
	before, _ := os.Stat(path)
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("setup left %d files", len(entries))
	}

	// The data directory works as well as the file.
	if _, err := Convert(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	now, _ := os.ReadFile(path)
	if !bytes.Equal(now, orig) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("Convert changed the database")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Convert created files beside the database: %v", names)
	}
}

// While Kuma runs, its latest writes are in kuma.db-wal and not yet in the
// main file. They must be read: a monitor added a minute ago is part of the
// setup being moved.
func TestConvertReadsTheWriteAheadLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuma.db")
	orig, err := os.ReadFile(filepath.Join("testdata", fixtures[1]))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	// A writer that stays open, as Kuma does, with checkpoints off so the
	// row lives only in the -wal file.
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(`INSERT INTO monitor (name, type, hostname, port, interval, active, user_id)
		VALUES ('Added while running', 'port', 'live.example.com', 443, 60, 1, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("no -wal file, so this test proves nothing: %v", err)
	}
	res, err := Convert(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := monitorByName(res, "Added while running"); !ok || m.Target != "live.example.com:443" {
		t.Errorf("the monitor in the write-ahead log was not read: %+v %v", m, ok)
	}
}

func TestConvertRefusesWhatIsNotKuma(t *testing.T) {
	dir := t.TempDir()

	other := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", "file:"+other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE monitors (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Convert(context.Background(), other); !errors.Is(err, ErrNotKuma) {
		t.Errorf("a database without Kuma's tables: err = %v, want ErrNotKuma", err)
	}

	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("not a database at all, but long enough to have a header"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(context.Background(), text); err == nil {
		t.Error("a text file was accepted")
	}

	if _, err := Convert(context.Background(), filepath.Join(dir, "missing.db")); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing file: err = %v", err)
	}
}
