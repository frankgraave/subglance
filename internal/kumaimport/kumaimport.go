// Package kumaimport converts an Uptime Kuma database into a SubGlance
// configuration file.
//
// It writes a configfile.Document and nothing else. The server has one import
// path, POST /api/v1/config/import, with its dry run, its all-or-nothing
// validation and its report; a converter that wrote to the database directly
// would be a second path with its own rules. Going through the file also means
// the result can be read before anything happens.
//
// Kuma's schema is internal to Kuma and changes between releases without
// notice. The reader therefore never names a column in SQL: it reads whole
// rows and looks fields up by name, so a column that a release added or
// dropped is an absent field rather than a failed query. The fixtures in
// testdata were written by real Kuma installations, one per supported major
// version, not by hand.
//
// Nothing is dropped silently. Every monitor or channel that cannot be
// carried over is listed with its reason, and so is every one that came over
// with a change worth checking.
package kumaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"

	_ "modernc.org/sqlite" // the driver the store uses: pure Go, no cgo
)

// Note is one line of the report: an object that was left out, or one that
// was converted with a change the reader should know about.
type Note struct {
	Kind   string // "monitor", "channel", "status page", "maintenance", "domain warning"
	Name   string
	Type   string // Kuma's type, when the object has one
	Reason string
}

func (n Note) String() string {
	label := n.Kind
	if n.Name != "" {
		label += " " + strconv.Quote(oneLine(n.Name))
	}
	if n.Type != "" {
		label += " (" + n.Type + ")"
	}
	return label + ": " + oneLine(n.Reason)
}

// Result is a converted database.
type Result struct {
	Document configfile.Document
	// Schema names the Kuma generation the file came from: "1.x" or "2.x".
	Schema string
	// Skipped lists what is not in Document, Changed what is in it with a
	// difference. Both are in the order the objects appear in Kuma.
	Skipped []Note
	Changed []Note
	// Monitors, Channels, Windows and StatusPages count the rows read, imported or
	// not. WindowsConverted counts the Kuma windows that came over; one can
	// become several SubGlance windows, one per monitor it covered.
	Monitors, Channels, Windows, StatusPages int
	WindowsConverted                         int
	// DomainMonitors counts the domain monitors added for Kuma's domain
	// expiry warning. They are the last ones in Document.Monitors, and no
	// Kuma monitor became one, so they are not among the converted.
	DomainMonitors int
}

// ErrNotKuma means the file is an SQLite database without Kuma's tables.
var ErrNotKuma = errors.New("not an Uptime Kuma database: it has no monitor and notification tables")

// Convert reads the Kuma database at path and returns the configuration it
// describes. path is kuma.db or the data directory holding it. The database
// is opened read-only and nothing is written beside it, so it is safe to run
// against the database of a Kuma that is still running.
func Convert(ctx context.Context, path string) (Result, error) {
	// Kuma's data directory works as well as the file in it.
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "kuma.db")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{}, fmt.Errorf("%s does not exist. Kuma keeps its database in its data directory as kuma.db; "+
				"a Kuma 2 installation set up with MariaDB has no such file and cannot be read", path)
		}
		return Result{}, err
	}
	db, err := open(ctx, path)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = db.Close() }()

	src, err := load(ctx, db)
	if err != nil {
		return Result{}, err
	}
	return convert(src), nil
}

// open opens the database read-only, without creating anything beside it.
//
// Kuma runs SQLite in WAL mode, where recent writes live in kuma.db-wal until
// they are folded into the main file. While Kuma has the database open that
// file exists, and so does the -shm index readers share, so a read-only
// connection reads Kuma's latest writes through them. When Kuma is stopped it
// folds everything in and removes both. A read-only connection would then
// create them afresh, which fails on a read-only mount (a Docker volume
// mounted :ro) and otherwise leaves files in Kuma's data directory. With no
// -wal file there is nothing outside the main file to read, so the database
// is opened as immutable instead, which creates nothing.
func open(ctx context.Context, path string) (*sql.DB, error) {
	// An absolute path, written as a URL so a ? or # in a directory name is
	// part of the path rather than the start of the parameters.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&immutable=1"}
	if _, err := os.Stat(abs + "-wal"); err == nil {
		u.RawQuery = "mode=ro"
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	return db, nil
}

// row is one database row keyed by column name.
type row map[string]any

func (r row) str(k string) string {
	switch v := r[k].(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		return v.Format(time.RFC3339)
	default:
		return fmt.Sprint(v)
	}
}

func (r row) int(k string) int {
	switch v := r[k].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v + 0.5)
	case bool:
		if v {
			return 1
		}
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(r.str(k)), 64)
	if err != nil {
		return 0
	}
	return int(f + 0.5)
}

func (r row) bool(k string) bool { return r.int(k) != 0 }

// source is everything the conversion reads, loaded up front.
type source struct {
	schema        string
	monitors      []row
	notifications []row
	links         []row // monitor_notification
	tags          []row
	monitorTags   []row
	statusPages   []row
	pageGroups    []row
	pageMonitors  []row
	pageDomains   []row
	pageIncidents []row
	pageWindows   []row
	maintenance   []row
	// monitorMaintenance links windows to the monitors they cover.
	monitorMaintenance []row
	settings           []row
	// proxies are Kuma's HTTP proxies, read for the address a monitor's
	// check went through; their credentials are never written.
	proxies []row
	// now decides which of Kuma's windows have ended. It is read once, so
	// one conversion judges every window at the same instant.
	now time.Time
}

// queries names every statement the reader runs. Whole rows, never a column
// list: see the package comment.
var queries = map[string]string{
	"monitor":                 "SELECT * FROM monitor ORDER BY id",
	"notification":            "SELECT * FROM notification ORDER BY id",
	"monitor_notification":    "SELECT * FROM monitor_notification ORDER BY id",
	"tag":                     "SELECT * FROM tag ORDER BY id",
	"monitor_tag":             "SELECT * FROM monitor_tag ORDER BY id",
	"status_page":             "SELECT * FROM status_page ORDER BY id",
	"group":                   `SELECT * FROM "group" ORDER BY id`,
	"monitor_group":           "SELECT * FROM monitor_group ORDER BY id",
	"status_page_cname":       "SELECT * FROM status_page_cname ORDER BY id",
	"incident":                "SELECT * FROM incident ORDER BY id",
	"maintenance_status_page": "SELECT * FROM maintenance_status_page ORDER BY id",
	"maintenance":             "SELECT * FROM maintenance ORDER BY id",
	"monitor_maintenance":     "SELECT * FROM monitor_maintenance ORDER BY id",
	"setting":                 "SELECT * FROM setting",
	"proxy":                   "SELECT * FROM proxy ORDER BY id",
}

func load(ctx context.Context, db *sql.DB) (source, error) {
	tables, err := tableNames(ctx, db)
	if err != nil {
		return source{}, err
	}
	if !tables["monitor"] || !tables["notification"] {
		return source{}, ErrNotKuma
	}

	// Kuma 2 moved its migrations to knex; 1.x patched the schema with its
	// own SQL files and has no knex table.
	src := source{schema: "1.x", now: time.Now()}
	if tables["knex_migrations"] {
		src.schema = "2.x"
	}
	read := func(table string) ([]row, error) {
		if !tables[table] {
			return nil, nil
		}
		return readAll(ctx, db, queries[table])
	}
	if src.monitors, err = read("monitor"); err != nil {
		return source{}, err
	}
	if src.notifications, err = read("notification"); err != nil {
		return source{}, err
	}
	if src.links, err = read("monitor_notification"); err != nil {
		return source{}, err
	}
	if src.tags, err = read("tag"); err != nil {
		return source{}, err
	}
	if src.monitorTags, err = read("monitor_tag"); err != nil {
		return source{}, err
	}
	if src.statusPages, err = read("status_page"); err != nil {
		return source{}, err
	}
	if src.pageGroups, err = read("group"); err != nil {
		return source{}, err
	}
	if src.pageMonitors, err = read("monitor_group"); err != nil {
		return source{}, err
	}
	if src.pageDomains, err = read("status_page_cname"); err != nil {
		return source{}, err
	}
	if src.pageIncidents, err = read("incident"); err != nil {
		return source{}, err
	}
	if src.pageWindows, err = read("maintenance_status_page"); err != nil {
		return source{}, err
	}
	if src.maintenance, err = read("maintenance"); err != nil {
		return source{}, err
	}
	if src.monitorMaintenance, err = read("monitor_maintenance"); err != nil {
		return source{}, err
	}
	if src.settings, err = read("setting"); err != nil {
		return source{}, err
	}
	if src.proxies, err = read("proxy"); err != nil {
		return source{}, err
	}
	return src, nil
}

func tableNames(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, fmt.Errorf("read the table list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = true
	}
	return tables, rows.Err()
}

func readAll(ctx context.Context, db *sql.DB, query string) ([]row, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read Kuma's database: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("read Kuma's database: %w", err)
		}
		r := make(row, len(cols))
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// convert turns the loaded rows into a document. It cannot fail: anything it
// cannot carry over becomes a Note instead.
func convert(src source) Result {
	res := Result{
		Document:    configfile.Document{Version: configfile.Version},
		Schema:      src.schema,
		Monitors:    len(src.monitors),
		Channels:    len(src.notifications),
		Windows:     len(src.maintenance),
		StatusPages: len(src.statusPages),
	}

	channelKeys := map[int64]string{} // Kuma notification id -> channel key
	lostNames := map[int64]string{}   // Kuma notification id -> name, for one that did not come over
	taken := map[string]bool{}
	for _, n := range src.notifications {
		before := len(res.Skipped)
		ch, ok := convertChannel(n, &res)
		if !ok {
			// The name the skipped channel is reported under, so the
			// monitors behind it name it the same way.
			name := strings.TrimSpace(n.str("name"))
			if len(res.Skipped) > before {
				name = res.Skipped[len(res.Skipped)-1].Name
			}
			lostNames[int64(n.int("id"))] = name
			continue
		}
		ch.Key = configfile.DeriveKey(ch.Name, "channel", func(k string) bool { return taken[k] })
		taken[ch.Key] = true
		channelKeys[int64(n.int("id"))] = ch.Key
		res.Document.Channels = append(res.Document.Channels, ch)
	}

	links := map[int64][]string{}
	// lost lists, per Kuma monitor id, the notifications it alerted through
	// that did not come over. A link to a notification that no longer
	// exists is in neither list: Kuma's own query joins on the
	// notification, so it alerted nobody through it either.
	lost := map[int64][]string{}
	lostSeen := map[[2]int64]bool{}
	for _, l := range src.links {
		mid, nid := int64(l.int("monitor_id")), int64(l.int("notification_id"))
		if key, ok := channelKeys[nid]; ok {
			links[mid] = append(links[mid], key)
		} else if name, ok := lostNames[nid]; ok && !lostSeen[[2]int64{mid, nid}] {
			lostSeen[[2]int64{mid, nid}] = true
			lost[mid] = append(lost[mid], name)
		}
	}
	tagNames := map[int64]string{}
	for _, t := range src.tags {
		tagNames[int64(t.int("id"))] = t.str("name")
	}
	tags := map[int64][]kumaTag{}
	for _, mt := range src.monitorTags {
		mid := int64(mt.int("monitor_id"))
		tags[mid] = append(tags[mid], kumaTag{name: tagNames[int64(mt.int("tag_id"))], value: mt.str("value")})
	}
	groups := map[int64]string{}
	byID := map[int64]row{}
	for _, m := range src.monitors {
		byID[int64(m.int("id"))] = m
		if m.str("type") == "group" {
			groups[int64(m.int("id"))] = strings.TrimSpace(m.str("name"))
		}
	}
	proxies := map[int64]row{}
	for _, p := range src.proxies {
		proxies[int64(p.int("id"))] = p
	}

	taken = map[string]bool{}
	monitorKeys := map[int64]string{} // Kuma monitor id -> monitor key
	certDays := certWarnDays(src.settings)
	for _, m := range src.monitors {
		id := int64(m.int("id"))
		mon, ok := convertMonitor(m, &res)
		if !ok {
			continue
		}
		typ := m.str("type")
		note := func(reason string) { changed(&res, "monitor", mon.Name, typ, reason) }
		convertCertWarning(m, &mon, typ, certDays, note)
		convertResponseCapture(m, &mon, typ, note)
		noteCheckSettings(m, typ, proxies, note)
		notePausedGroup(m, byID, note)
		mon.Tags = convertTags(mon.Name, tags[id], groups[int64(m.int("parent"))], &res)
		mon.Channels = links[id]
		if mon.Channels == nil {
			mon.Channels = []string{}
		}
		sort.Strings(mon.Channels)
		noteLostChannels(lost[id], len(mon.Channels), note)
		mon.Key = configfile.DeriveKey(mon.Name, "monitor", func(k string) bool { return taken[k] })
		taken[mon.Key] = true
		monitorKeys[id] = mon.Key
		res.Document.Monitors = append(res.Document.Monitors, mon)
	}
	added := convertDomainWarnings(src, links, taken, &res)
	res.DomainMonitors = len(added)
	res.Document.Monitors = append(res.Document.Monitors, added...)
	convertWindows(src, monitorKeys, &res)

	convertStatusPages(src, monitorKeys, &res)

	return res
}

// oneLine keeps a name from breaking out of the comment it is written in.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Render writes the result as a configuration file: the document, then the
// report as comments at the end, where a person reading the file finds it and
// the importer ignores it.
func Render(res Result) ([]byte, error) {
	body, err := configfile.Marshal(res.Document)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Converted from an Uptime Kuma %s database by `subglance import uptime-kuma`.\n", res.Schema)
	fmt.Fprintf(&b, "# Review it, then import it under Settings, Import & export, which shows a dry run first.\n")
	b.Write(body)
	section := func(title string, notes []Note) {
		if len(notes) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n# %s\n", title)
		for _, n := range notes {
			fmt.Fprintf(&b, "#   - %s\n", n)
		}
	}
	section("Not imported from Uptime Kuma:", res.Skipped)
	section("Imported with a change to check:", res.Changed)
	return []byte(b.String()), nil
}
