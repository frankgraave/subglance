package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A status page's own look (migration 0031): the language of its fixed
// texts, an accent colour for its title, the footer credit and a logo.

// Status page languages. The public page's fixed texts exist in each of
// these; internal/statuspage holds the table.
const (
	StatusPageLanguageEnglish = "en"
	StatusPageLanguageDutch   = "nl"
)

// StatusPageLanguages lists every language a page may be set to.
var StatusPageLanguages = []string{StatusPageLanguageEnglish, StatusPageLanguageDutch}

// MaxStatusPageLogoBytes is the largest logo a page accepts. A logo is drawn
// a few dozen pixels tall; a quarter of a megabyte is room for a sharp one
// and keeps a database row small enough to read on every public request.
const MaxStatusPageLogoBytes = 256 << 10

// The page canvas behind the title, in both themes. They are
// web/src/styles/tokens.css's --canvas: oklch(.205 0 0) in the dark theme and
// #fbfbfa in the light one. status_page_branding_test.go reads tokens.css and
// fails when either drifts.
const (
	darkCanvasOKLabL = 0.205
	lightCanvasHex   = "#fbfbfa"
)

// darkCanvasLuminance is the dark canvas as WCAG relative luminance. A grey
// has no chroma, so its linear sRGB channels all equal OKLab L cubed, and so
// does Y.
const darkCanvasLuminance = darkCanvasOKLabL * darkCanvasOKLabL * darkCanvasOKLabL

var lightCanvasLuminance = luminance(lightCanvasHex)

// MinAccentContrast is the floor an accent has to clear against both
// canvases. The accent colours the page title, which is large text (24px, at
// or above WCAG's 18pt), so AA asks for 3:1. It has to hold in both themes
// because the page follows each visitor's own preference: an accent that
// reads on white and vanishes on near-black is unreadable to half of them.
const MinAccentContrast = 3.0

var accentPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// NormaliseAccent trims and lowercases an accent and checks it: "" (no
// accent) or #rrggbb that clears MinAccentContrast against both canvases.
// The refusal states the measured ratios, so the operator can see which
// way to move the colour.
func NormaliseAccent(accent string) (string, error) {
	accent = strings.ToLower(strings.TrimSpace(accent))
	if accent == "" {
		return "", nil
	}
	if !accentPattern.MatchString(accent) {
		return accent, errors.New("accent must be a colour written as #rrggbb, or empty for none")
	}
	dark, light := AccentContrast(accent)
	if dark < MinAccentContrast || light < MinAccentContrast {
		return accent, fmt.Errorf("accent %s measures %s against the dark page and %s against the light page; "+
			"the title needs %s on both, because the page follows each visitor's theme",
			accent, ratio(dark), ratio(light), ratio(MinAccentContrast))
	}
	return accent, nil
}

// AccentContrast is the contrast ratio of a #rrggbb colour against the dark
// and the light canvas. The caller has checked the format.
func AccentContrast(accent string) (dark, light float64) {
	y := luminance(accent)
	return contrastRatio(y, darkCanvasLuminance), contrastRatio(y, lightCanvasLuminance)
}

// ratio prints a contrast ratio the way WCAG writes it, cut rather than
// rounded so 2.999 never reads as passing 3:1.
func ratio(r float64) string {
	return strconv.FormatFloat(math.Floor(r*100)/100, 'f', 2, 64) + ":1"
}

func contrastRatio(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// luminance is WCAG 2.x relative luminance of #rrggbb.
func luminance(hexColour string) float64 {
	var y float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		v, _ := strconv.ParseUint(hexColour[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		y += weight * c
	}
	return y
}

// StatusPageLogo describes a page's uploaded logo; the bytes are read
// separately, only when the logo itself is requested.
type StatusPageLogo struct {
	// ContentType is decided from the bytes when the logo is uploaded:
	// image/png, image/jpeg or image/webp.
	ContentType string
	Width       int
	Height      int
	// Key is random and new on every upload. It names the file in its
	// public address, so a changed logo is a new address and a cache can
	// never serve the old one under it.
	Key       string
	Bytes     int
	UpdatedAt time.Time
}

// FileName is the logo's public file name: its key and the extension of
// its type.
func (l StatusPageLogo) FileName() string {
	return l.Key + logoExtensions[l.ContentType]
}

var logoExtensions = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}

// logoFileName is a public logo file name as FileName writes it.
var logoFileName = regexp.MustCompile(`^([0-9a-f]{16})\.(png|jpg|webp)$`)

// StatusPageLogoUpload is one checked logo, ready to store. The caller
// decodes the image; the store only refuses what its table would.
type StatusPageLogoUpload struct {
	ContentType   string
	Width, Height int
	Data          []byte
}

// SetStatusPageLogo stores a page's logo, replacing any it had, under a new
// key. It reports ErrNotFound for a missing page.
func (db *DB) SetStatusPageLogo(ctx context.Context, pageID int64, logo StatusPageLogoUpload) (StatusPageLogo, error) {
	if _, ok := logoExtensions[logo.ContentType]; !ok {
		return StatusPageLogo{}, fmt.Errorf("status page logo: unsupported type %q", logo.ContentType)
	}
	if len(logo.Data) == 0 || len(logo.Data) > MaxStatusPageLogoBytes || logo.Width <= 0 || logo.Height <= 0 {
		return StatusPageLogo{}, fmt.Errorf("status page logo: %d bytes, %dx%d is out of range", len(logo.Data), logo.Width, logo.Height)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return StatusPageLogo{}, fmt.Errorf("generate logo key: %w", err)
	}
	key := hex.EncodeToString(b[:])
	now := time.Now().Unix()

	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return StatusPageLogo{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var found int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM status_pages WHERE id = ?", pageID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusPageLogo{}, fmt.Errorf("%w: status page %d", ErrNotFound, pageID)
	}
	if err != nil {
		return StatusPageLogo{}, fmt.Errorf("look up status page %d: %w", pageID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_logos (page_id, content_type, width, height, file_key, data, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (page_id) DO UPDATE SET content_type = excluded.content_type, width = excluded.width,
			height = excluded.height, file_key = excluded.file_key, data = excluded.data,
			updated_at = excluded.updated_at`,
		pageID, logo.ContentType, logo.Width, logo.Height, key, logo.Data, now); err != nil {
		return StatusPageLogo{}, fmt.Errorf("store status page logo: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE status_pages SET updated_at = ? WHERE id = ?", now, pageID); err != nil {
		return StatusPageLogo{}, fmt.Errorf("touch status page %d: %w", pageID, err)
	}
	if err := tx.Commit(); err != nil {
		return StatusPageLogo{}, fmt.Errorf("commit: %w", err)
	}
	return StatusPageLogo{
		ContentType: logo.ContentType, Width: logo.Width, Height: logo.Height,
		Key: key, Bytes: len(logo.Data), UpdatedAt: time.Unix(now, 0).UTC(),
	}, nil
}

// DeleteStatusPageLogo removes a page's logo. It reports ErrNotFound when
// the page has none, so a second press does not claim to have done
// something. The removal and the page's new updated_at commit together, as
// they do in SetStatusPageLogo: an error means nothing changed.
func (db *DB) DeleteStatusPageLogo(ctx context.Context, pageID int64) error {
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, "DELETE FROM status_page_logos WHERE page_id = ?", pageID)
	if err != nil {
		return fmt.Errorf("delete status page logo: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete status page logo: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: logo of status page %d", ErrNotFound, pageID)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE status_pages SET updated_at = ? WHERE id = ?", time.Now().Unix(), pageID); err != nil {
		return fmt.Errorf("touch status page %d: %w", pageID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// StatusPageLogoData returns a page's logo and its bytes, or ErrNotFound,
// whether or not the page is published: it is the operator's copy.
func (db *DB) StatusPageLogoData(ctx context.Context, pageID int64) (StatusPageLogo, []byte, error) {
	return db.logoData(ctx, "l.page_id = ?", pageID)
}

// PublishedStatusPageLogo returns the logo a public file name names, with
// its bytes, but only while the page that owns it is published. Anything
// else, including a malformed name, is ErrNotFound, so the public route
// cannot tell a switched-off page's logo from one that never existed.
func (db *DB) PublishedStatusPageLogo(ctx context.Context, fileName string) (StatusPageLogo, []byte, error) {
	m := logoFileName.FindStringSubmatch(fileName)
	if m == nil {
		return StatusPageLogo{}, nil, fmt.Errorf("%w: logo %q", ErrNotFound, fileName)
	}
	logo, data, err := db.logoData(ctx, "l.file_key = ? AND p.enabled = 1", m[1])
	if err != nil {
		return StatusPageLogo{}, nil, err
	}
	// The extension is part of the address: a PNG asked for as .jpg is not
	// the file the page links to.
	if logo.FileName() != fileName {
		return StatusPageLogo{}, nil, fmt.Errorf("%w: logo %q", ErrNotFound, fileName)
	}
	return logo, data, nil
}

func (db *DB) logoData(ctx context.Context, where string, arg any) (StatusPageLogo, []byte, error) {
	var (
		logo StatusPageLogo
		data []byte
		at   int64
	)
	err := db.Reader.QueryRowContext(ctx, `
		SELECT l.content_type, l.width, l.height, l.file_key, l.data, l.updated_at
		FROM status_page_logos l JOIN status_pages p ON p.id = l.page_id
		WHERE `+where, arg).Scan(&logo.ContentType, &logo.Width, &logo.Height, &logo.Key, &data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusPageLogo{}, nil, fmt.Errorf("%w: status page logo", ErrNotFound)
	}
	if err != nil {
		return StatusPageLogo{}, nil, fmt.Errorf("read status page logo: %w", err)
	}
	logo.Bytes, logo.UpdatedAt = len(data), time.Unix(at, 0).UTC()
	return logo, data, nil
}
