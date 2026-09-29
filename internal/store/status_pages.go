package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// A status page shows a chosen set of monitors to visitors without an account
// (migration 0022, docs/design/status-page.md). This file holds the tables'
// Go side: the page itself and the entries that put a monitor on it under a
// public name.
//
// Nothing here renders anything public. The public response is a separate,
// dedicated type in the API layer, so a field added to StatusPage or
// StatusPageEntry cannot reach a visitor without being named there.

// Status page selection modes.
const (
	// StatusPageSelectMonitors lists exactly the monitors that have an entry.
	StatusPageSelectMonitors = "monitors"
	// StatusPageSelectTag lists the monitors carrying TagKey=TagValue that
	// also have an entry; a tagged monitor without one is not shown until it
	// is given a public name.
	StatusPageSelectTag = "tag"
)

// Limits from docs/design/status-page.md §4.
const (
	maxStatusPageTitle       = 120
	maxStatusPageDescription = 500
	maxStatusPageDisplayName = 80
	// MaxStatusPageEntries bounds one page. A public page is a summary for
	// people outside; several hundred rows is a dashboard, and every entry is
	// 90 days of history the render has to compute.
	MaxStatusPageEntries = 200
)

// statusPageSlug is the design's slug rule: lowercase letters, digits and
// dashes, starting with a letter or digit, at most 63 characters, so a slug
// is also a valid DNS label for anyone mapping status.example.com onto it.
var statusPageSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// reservedStatusPageSlugs would shadow a path the server or the web UI
// already serves under the same prefix.
var reservedStatusPageSlugs = map[string]bool{"api": true, "assets": true, "fonts": true}

// ErrStatusPageSlugTaken is returned when another page already uses the slug.
var ErrStatusPageSlugTaken = errors.New("store: a status page with this slug already exists")

// ErrInvalidStatusPage matches every validation failure, so the API can
// refuse all of them the same way without matching on message text. The
// concrete error is a *StatusPageError, which names the field.
var ErrInvalidStatusPage = errors.New("invalid status page")

// ErrUnknownMonitor is returned when an entry names a monitor that does not
// exist.
var ErrUnknownMonitor = errors.New("store: unknown monitor")

// StatusPage is one public page's configuration.
type StatusPage struct {
	ID          int64
	Slug        string
	Title       string
	Description string
	// Timezone is the IANA zone the page shows times in, chosen for its
	// audience rather than taken from the server.
	Timezone string

	// Selection is StatusPageSelectMonitors or StatusPageSelectTag. TagKey
	// and TagValue are set only for a tag page.
	Selection string
	TagKey    string
	TagValue  string

	// Indexable drops the page's noindex header. Off by default: publishing
	// a page for users is not a decision to publish it to search engines.
	Indexable bool
	// Enabled makes the page reachable. Off by default, so a draft
	// publishes nothing.
	Enabled bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// StatusPageEntry puts one monitor on one page.
type StatusPageEntry struct {
	MonitorID int64
	// PublicKey identifies the entry on the public page instead of the
	// monitor id. It is random, set when the monitor is added to the page,
	// kept for as long as the monitor stays on it, and never reused.
	PublicKey string
	// DisplayName is the only name a visitor sees. There is no fallback to
	// the monitor's internal name.
	DisplayName string
	Position    int
}

// StatusPageEntryInput is one row of a SetStatusPageEntries call. The order
// of the slice is the order on the page.
type StatusPageEntryInput struct {
	MonitorID   int64
	DisplayName string
}

// StatusPageError is one refused page or entry list. It matches
// ErrInvalidStatusPage under errors.Is.
//
// Field names the setting at fault in the spelling the API uses ("slug",
// "tag_value", "entries"), so a form can put the message beside the input
// that caused it instead of matching on the wording.
type StatusPageError struct {
	Field string
	Msg   string
}

func (e *StatusPageError) Error() string { return ErrInvalidStatusPage.Error() + ": " + e.Msg }

// Is makes errors.Is(err, ErrInvalidStatusPage) hold for every refusal.
func (e *StatusPageError) Is(target error) bool { return target == ErrInvalidStatusPage }

func invalidStatusPage(field, format string, args ...any) error {
	return &StatusPageError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// NormaliseStatusPage trims and checks a page before it is stored, filling in
// UTC for an empty time zone. Every failure wraps ErrInvalidStatusPage.
//
// The slug is lowercased rather than refused when typed in capitals: the
// column compares without case, so "Acme" and "acme" are one page anyway, and
// storing the lowercase form keeps the URL the operator sees and the one the
// server answers identical.
func NormaliseStatusPage(p StatusPage) (StatusPage, error) {
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	if !statusPageSlug.MatchString(p.Slug) {
		return p, invalidStatusPage("slug", "slug must be 1-63 lowercase letters, digits or dashes, starting with a letter or digit")
	}
	if reservedStatusPageSlugs[p.Slug] {
		return p, invalidStatusPage("slug", "slug %q is reserved", p.Slug)
	}

	p.Title = strings.TrimSpace(p.Title)
	if n := utf8.RuneCountInString(p.Title); n == 0 || n > maxStatusPageTitle {
		return p, invalidStatusPage("title", "title must be 1-%d characters", maxStatusPageTitle)
	}
	if strings.ContainsAny(p.Title, "\n\r\t") {
		return p, invalidStatusPage("title", "title must not contain line breaks or tabs")
	}

	p.Description = strings.TrimSpace(p.Description)
	if utf8.RuneCountInString(p.Description) > maxStatusPageDescription {
		return p, invalidStatusPage("description", "description must be at most %d characters", maxStatusPageDescription)
	}

	p.Timezone = strings.TrimSpace(p.Timezone)
	if p.Timezone == "" {
		p.Timezone = "UTC"
	}
	if p.Timezone == "Local" {
		// "Local" is whatever zone the server runs in, which is exactly
		// what the page's zone exists to avoid.
		return p, invalidStatusPage("timezone", "use an explicit IANA timezone")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return p, invalidStatusPage("timezone", "unknown IANA timezone %q", p.Timezone)
	}

	switch p.Selection {
	case StatusPageSelectMonitors:
		if p.TagKey != "" || p.TagValue != "" {
			return p, invalidStatusPage(tagField(p.TagKey), "a page that selects monitors takes no tag")
		}
	case StatusPageSelectTag:
		// The key is checked on its own first, with a stand-in value, so
		// a refusal can name the input that is actually wrong.
		if _, _, err := NormaliseRoutingTag(p.TagKey, "-"); err != nil {
			return p, invalidStatusPage("tag_key", "%s", err.Error())
		}
		key, value, err := NormaliseRoutingTag(p.TagKey, p.TagValue)
		if err != nil {
			return p, invalidStatusPage("tag_value", "%s", err.Error())
		}
		p.TagKey, p.TagValue = key, value
	default:
		return p, invalidStatusPage("selection", "selection must be %q or %q", StatusPageSelectMonitors, StatusPageSelectTag)
	}
	return p, nil
}

// tagField blames the tag input that was filled in on a page that takes no
// tag: the key when there is one, otherwise the value.
func tagField(key string) string {
	if key != "" {
		return "tag_key"
	}
	return "tag_value"
}

const statusPageColumns = `id, slug, title, description, timezone, selection, tag_key, tag_value,
	indexable, enabled, created_at, updated_at`

func scanStatusPage(row interface{ Scan(...any) error }) (StatusPage, error) {
	var (
		p                  StatusPage
		tagKey, tagValue   sql.NullString
		indexable, enabled int
		created, updated   int64
	)
	if err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &p.Timezone, &p.Selection,
		&tagKey, &tagValue, &indexable, &enabled, &created, &updated); err != nil {
		return StatusPage{}, err
	}
	p.TagKey, p.TagValue = tagKey.String, tagValue.String
	p.Indexable, p.Enabled = indexable == 1, enabled == 1
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return p, nil
}

// nullIfEmpty stores "" as NULL, which the table's CHECK requires for the tag
// columns of a page that selects monitors.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListStatusPages returns every page, oldest first.
func (db *DB) ListStatusPages(ctx context.Context) ([]StatusPage, error) {
	rows, err := db.Reader.QueryContext(ctx, "SELECT "+statusPageColumns+" FROM status_pages ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query status pages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []StatusPage{}
	for rows.Next() {
		p, err := scanStatusPage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan status page: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetStatusPage returns one page by id, or ErrNotFound.
func (db *DB) GetStatusPage(ctx context.Context, id int64) (StatusPage, error) {
	p, err := scanStatusPage(db.Reader.QueryRowContext(ctx,
		"SELECT "+statusPageColumns+" FROM status_pages WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return StatusPage{}, fmt.Errorf("%w: status page %d", ErrNotFound, id)
	}
	if err != nil {
		return StatusPage{}, fmt.Errorf("get status page %d: %w", id, err)
	}
	return p, nil
}

// GetStatusPageBySlug returns one page by slug, without regard to case, or
// ErrNotFound. It returns disabled pages too: telling a disabled page from an
// unknown slug is the caller's job, and the public route must answer both
// with the same 404 (design §3.1).
func (db *DB) GetStatusPageBySlug(ctx context.Context, slug string) (StatusPage, error) {
	p, err := scanStatusPage(db.Reader.QueryRowContext(ctx,
		"SELECT "+statusPageColumns+" FROM status_pages WHERE slug = ?", slug))
	if errors.Is(err, sql.ErrNoRows) {
		return StatusPage{}, fmt.Errorf("%w: status page %q", ErrNotFound, slug)
	}
	if err != nil {
		return StatusPage{}, fmt.Errorf("get status page %q: %w", slug, err)
	}
	return p, nil
}

// CreateStatusPage validates and stores a page and returns it with its id.
// It reports ErrInvalidStatusPage or ErrStatusPageSlugTaken and stores
// nothing when it does.
func (db *DB) CreateStatusPage(ctx context.Context, p StatusPage) (StatusPage, error) {
	p, err := NormaliseStatusPage(p)
	if err != nil {
		return StatusPage{}, err
	}
	now := time.Now().Unix()
	res, err := db.Writer.ExecContext(ctx, `
		INSERT INTO status_pages (slug, title, description, timezone, selection, tag_key, tag_value,
			indexable, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Title, p.Description, p.Timezone, p.Selection, nullIfEmpty(p.TagKey), nullIfEmpty(p.TagValue),
		boolInt(p.Indexable), boolInt(p.Enabled), now, now)
	if isUniqueViolation(err) {
		return StatusPage{}, fmt.Errorf("%w: %s", ErrStatusPageSlugTaken, p.Slug)
	}
	if err != nil {
		return StatusPage{}, fmt.Errorf("insert status page: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return StatusPage{}, fmt.Errorf("last insert id: %w", err)
	}
	return db.GetStatusPage(ctx, id)
}

// UpdateStatusPage replaces a page's settings and leaves its entries alone.
// It reports ErrNotFound, ErrInvalidStatusPage or ErrStatusPageSlugTaken.
func (db *DB) UpdateStatusPage(ctx context.Context, p StatusPage) (StatusPage, error) {
	p, err := NormaliseStatusPage(p)
	if err != nil {
		return StatusPage{}, err
	}
	res, err := db.Writer.ExecContext(ctx, `
		UPDATE status_pages SET slug = ?, title = ?, description = ?, timezone = ?, selection = ?,
			tag_key = ?, tag_value = ?, indexable = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		p.Slug, p.Title, p.Description, p.Timezone, p.Selection, nullIfEmpty(p.TagKey), nullIfEmpty(p.TagValue),
		boolInt(p.Indexable), boolInt(p.Enabled), time.Now().Unix(), p.ID)
	if isUniqueViolation(err) {
		return StatusPage{}, fmt.Errorf("%w: %s", ErrStatusPageSlugTaken, p.Slug)
	}
	if err != nil {
		return StatusPage{}, fmt.Errorf("update status page %d: %w", p.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return StatusPage{}, fmt.Errorf("update status page %d: %w", p.ID, err)
	}
	if n == 0 {
		return StatusPage{}, fmt.Errorf("%w: status page %d", ErrNotFound, p.ID)
	}
	return db.GetStatusPage(ctx, p.ID)
}

// DeleteStatusPage removes a page and its entries, or reports ErrNotFound.
func (db *DB) DeleteStatusPage(ctx context.Context, id int64) error {
	res, err := db.Writer.ExecContext(ctx, "DELETE FROM status_pages WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete status page %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete status page %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: status page %d", ErrNotFound, id)
	}
	return nil
}

// ListStatusPageEntries returns every entry of a page in page order,
// including entries a tag page does not currently show. It returns an empty
// slice for a page without entries and does not check that the page exists.
func (db *DB) ListStatusPageEntries(ctx context.Context, pageID int64) ([]StatusPageEntry, error) {
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT monitor_id, public_key, display_name, position
		FROM status_page_entries WHERE page_id = ? ORDER BY position, monitor_id`, pageID)
	if err != nil {
		return nil, fmt.Errorf("query status page entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []StatusPageEntry{}
	for rows.Next() {
		var e StatusPageEntry
		if err := rows.Scan(&e.MonitorID, &e.PublicKey, &e.DisplayName, &e.Position); err != nil {
			return nil, fmt.Errorf("scan status page entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetStatusPageEntries makes a page's entries equal to the list given, in
// that order, in one transaction.
//
// A monitor that stays on the page keeps its public key, so a visitor's link
// or a script keyed on it survives a rename or a reorder. A monitor that is
// added gets a new random key; one that is removed loses its key for good.
// It reports ErrNotFound for a missing page, ErrUnknownMonitor for a monitor
// that does not exist and ErrInvalidStatusPage for a bad name or a monitor
// listed twice, and changes nothing when it does.
func (db *DB) SetStatusPageEntries(ctx context.Context, pageID int64, entries []StatusPageEntryInput) ([]StatusPageEntry, error) {
	if len(entries) > MaxStatusPageEntries {
		return nil, invalidStatusPage("entries", "at most %d monitors per page", MaxStatusPageEntries)
	}
	seen := make(map[int64]bool, len(entries))
	for i := range entries {
		name := strings.TrimSpace(entries[i].DisplayName)
		if n := utf8.RuneCountInString(name); n == 0 || n > maxStatusPageDisplayName {
			return nil, invalidStatusPage("entries", "display name of monitor %d must be 1-%d characters", entries[i].MonitorID, maxStatusPageDisplayName)
		}
		if strings.ContainsAny(name, "\n\r\t") {
			return nil, invalidStatusPage("entries", "display name of monitor %d must not contain line breaks or tabs", entries[i].MonitorID)
		}
		if seen[entries[i].MonitorID] {
			return nil, invalidStatusPage("entries", "monitor %d is listed twice", entries[i].MonitorID)
		}
		seen[entries[i].MonitorID] = true
		entries[i].DisplayName = name
	}

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var found int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM status_pages WHERE id = ?", pageID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: status page %d", ErrNotFound, pageID)
	}
	if err != nil {
		return nil, fmt.Errorf("look up status page %d: %w", pageID, err)
	}

	if err := checkMonitorsExist(ctx, tx, entries); err != nil {
		return nil, err
	}
	keys, err := existingEntryKeys(ctx, tx, pageID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM status_page_entries WHERE page_id = ?", pageID); err != nil {
		return nil, fmt.Errorf("clear status page entries: %w", err)
	}
	for pos, e := range entries {
		key, kept := keys[e.MonitorID]
		if !kept {
			if key, err = newStatusPageKey(); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO status_page_entries (page_id, monitor_id, public_key, display_name, position)
			VALUES (?, ?, ?, ?, ?)`, pageID, e.MonitorID, key, e.DisplayName, pos); err != nil {
			return nil, fmt.Errorf("add monitor %d to status page: %w", e.MonitorID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE status_pages SET updated_at = ? WHERE id = ?", time.Now().Unix(), pageID); err != nil {
		return nil, fmt.Errorf("touch status page %d: %w", pageID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return db.ListStatusPageEntries(ctx, pageID)
}

// checkMonitorsExist looks every entry's monitor up in one query, so a full
// page costs one round trip on the writer connection instead of one per entry.
// It reports ErrUnknownMonitor for the first entry, in page order, whose
// monitor does not exist. The IDs travel as one JSON array parameter.
func checkMonitorsExist(ctx context.Context, tx *sql.Tx, entries []StatusPageEntryInput) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]int64, len(entries))
	for i, e := range entries {
		ids[i] = e.MonitorID
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode monitor ids: %w", err)
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT id FROM monitors WHERE id IN (SELECT value FROM json_each(?))", string(encoded))
	if err != nil {
		return fmt.Errorf("look up monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	exists := make(map[int64]bool, len(entries))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan monitor id: %w", err)
		}
		exists[id] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("look up monitors: %w", err)
	}
	for _, e := range entries {
		if !exists[e.MonitorID] {
			return fmt.Errorf("%w: %d", ErrUnknownMonitor, e.MonitorID)
		}
	}
	return nil
}

func existingEntryKeys(ctx context.Context, tx *sql.Tx, pageID int64) (map[int64]string, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT monitor_id, public_key FROM status_page_entries WHERE page_id = ?", pageID)
	if err != nil {
		return nil, fmt.Errorf("query status page keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	keys := map[int64]string{}
	for rows.Next() {
		var (
			id  int64
			key string
		)
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan status page key: %w", err)
		}
		keys[id] = key
	}
	return keys, rows.Err()
}

// newStatusPageKey returns 16 hex characters from crypto/rand. Sixty-four
// random bits make a collision with any key ever issued on one instance
// negligible; the UNIQUE constraint turns the impossible case into an error
// rather than two entries sharing a key.
func newStatusPageKey() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate status page key: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ShownStatusPageEntries returns the entries a visitor sees on a page, in page
// order.
//
// On a monitors page that is every entry. On a tag page it is the entries
// whose monitor carries the page's tag right now: an entry for a monitor that
// lost the tag drops off, and a tagged monitor without an entry is not shown
// at all, because the only name it has is its internal one.
func (db *DB) ShownStatusPageEntries(ctx context.Context, p StatusPage) ([]StatusPageEntry, error) {
	if p.Selection != StatusPageSelectTag {
		return db.ListStatusPageEntries(ctx, p.ID)
	}
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT e.monitor_id, e.public_key, e.display_name, e.position
		FROM status_page_entries e
		JOIN monitor_tags t ON t.monitor_id = e.monitor_id AND t.key = ? AND t.value = ?
		WHERE e.page_id = ?
		ORDER BY e.position, e.monitor_id`, p.TagKey, p.TagValue, p.ID)
	if err != nil {
		return nil, fmt.Errorf("query shown status page entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []StatusPageEntry{}
	for rows.Next() {
		var e StatusPageEntry
		if err := rows.Scan(&e.MonitorID, &e.PublicKey, &e.DisplayName, &e.Position); err != nil {
			return nil, fmt.Errorf("scan status page entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UnnamedStatusPageMonitors returns, for a tag page, the ids of monitors that
// carry the tag but have no entry and are therefore not shown — the editor's
// "not shown until named" list. It returns nothing for a monitors page.
func (db *DB) UnnamedStatusPageMonitors(ctx context.Context, p StatusPage) ([]int64, error) {
	if p.Selection != StatusPageSelectTag {
		return []int64{}, nil
	}
	rows, err := db.Reader.QueryContext(ctx, `
		SELECT t.monitor_id FROM monitor_tags t
		WHERE t.key = ? AND t.value = ?
		  AND NOT EXISTS (SELECT 1 FROM status_page_entries e WHERE e.page_id = ? AND e.monitor_id = t.monitor_id)
		ORDER BY t.monitor_id`, p.TagKey, p.TagValue, p.ID)
	if err != nil {
		return nil, fmt.Errorf("query unnamed status page monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan monitor id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
