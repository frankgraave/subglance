package store

import (
	"bytes"
	"errors"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The canvases the accent is measured against are tokens.css's --canvas.
// A theme change that moves either must move the contrast rule with it.
func TestAccentCanvasesMatchTheTokens(t *testing.T) {
	css, err := os.ReadFile("../../web/src/styles/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	block := func(theme string) string {
		start := strings.Index(string(css), `[data-theme="`+theme+`"] {`)
		if start < 0 {
			t.Fatalf("tokens.css has no %s theme block", theme)
		}
		end := strings.Index(string(css)[start:], "\n}")
		return string(css)[start : start+end]
	}
	canvas := regexp.MustCompile(`\n\s*--canvas:\s*([^;]+);`)
	dark := canvas.FindStringSubmatch(block("dark"))
	light := canvas.FindStringSubmatch(block("light"))
	if dark == nil || light == nil {
		t.Fatal("tokens.css lacks a --canvas in one of the themes")
	}
	if want := "oklch(" + strings.TrimPrefix(strconv.FormatFloat(darkCanvasOKLabL, 'f', -1, 64), "0") + " 0 0)"; strings.TrimSpace(dark[1]) != want {
		t.Errorf("dark --canvas is %q; the accent check assumes %q", dark[1], want)
	}
	if strings.TrimSpace(light[1]) != lightCanvasHex {
		t.Errorf("light --canvas is %q; the accent check assumes %q", light[1], lightCanvasHex)
	}
}

func TestLuminanceAndContrastFollowWCAG(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.005 }
	if got := contrastRatio(luminance("#000000"), luminance("#ffffff")); !near(got, 21) {
		t.Errorf("black on white = %.3f, want 21", got)
	}
	// #767676 on white is the textbook 4.54:1 grey.
	if got := contrastRatio(luminance("#767676"), luminance("#ffffff")); !near(got, 4.54) {
		t.Errorf("#767676 on white = %.3f, want 4.54", got)
	}
}

func TestNormaliseAccent(t *testing.T) {
	for in, want := range map[string]string{"": "", "  ": "", " #3B82F6 ": "#3b82f6", "#e5484d": "#e5484d"} {
		got, err := NormaliseAccent(in)
		if err != nil || got != want {
			t.Errorf("NormaliseAccent(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"blue", "#fff", "#12345g", "3b82f6", "#3b82f6;}", "rgb(1,2,3)"} {
		if _, err := NormaliseAccent(bad); err == nil {
			t.Errorf("NormaliseAccent(%q) accepted a value that is not #rrggbb", bad)
		}
	}
}

// An accent is the title's colour in both themes, so it has to read on
// both. Navy vanishes on the dark page and yellow on the light one; the
// refusal states both measurements.
func TestAccentMustClearContrastInBothThemes(t *testing.T) {
	for _, c := range []struct{ accent, dark, light string }{
		{"#000080", "1.", ""},
		{"#ffff00", "", "1."},
	} {
		_, err := NormaliseAccent(c.accent)
		if err == nil {
			t.Fatalf("%s was accepted", c.accent)
		}
		msg := err.Error()
		if !strings.Contains(msg, "against the dark page") || !strings.Contains(msg, "against the light page") ||
			!strings.Contains(msg, "3.00:1") {
			t.Errorf("%s: the refusal does not state the measured ratios and the floor: %s", c.accent, msg)
		}
	}
	// A mid blue clears 3:1 on both.
	if _, err := NormaliseAccent("#3b82f6"); err != nil {
		t.Errorf("#3b82f6 refused: %v", err)
	}
	// The floor is applied as measured, never rounded up to pass.
	dark, light := AccentContrast("#000080")
	if dark >= MinAccentContrast || light < MinAccentContrast {
		t.Errorf("navy: dark %.2f, light %.2f; the fixture no longer fails on the dark side only", dark, light)
	}
	if got := ratio(2.999); got != "2.99:1" {
		t.Errorf("ratio(2.999) = %q; a ratio under the floor must not print as the floor", got)
	}
}

func TestStatusPageLanguageAndAccentAreStoredAndChecked(t *testing.T) {
	db := openTestDB(t)
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectMonitors})
	if p.Language != "en" || p.Accent != "" || p.HideCredit || p.Logo != nil {
		t.Errorf("new page = %q/%q/%v/%v; want English, no accent, the credit shown and no logo",
			p.Language, p.Accent, p.HideCredit, p.Logo)
	}
	p.Language, p.Accent, p.HideCredit = " NL ", "#3B82F6", true
	got, err := db.UpdateStatusPage(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "nl" || got.Accent != "#3b82f6" || !got.HideCredit {
		t.Errorf("updated = %q/%q/%v", got.Language, got.Accent, got.HideCredit)
	}

	for field, change := range map[string]func(*StatusPage){
		"language": func(p *StatusPage) { p.Language = "de" },
		"accent":   func(p *StatusPage) { p.Accent = "#ffff00" },
	} {
		bad := got
		change(&bad)
		_, err := db.UpdateStatusPage(t.Context(), bad)
		var spe *StatusPageError
		if !errors.As(err, &spe) || spe.Field != field {
			t.Errorf("%s: err = %v, want a refusal naming %q", field, err, field)
		}
	}
}

// The column checks back up the Go rule, so a write that skips
// NormaliseStatusPage still cannot store a colour that is not #rrggbb or a
// language the page has no texts for.
func TestBrandingColumnsRefuseWhatTheFormRefuses(t *testing.T) {
	db := openTestDB(t)
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectMonitors})
	for _, q := range []string{
		"UPDATE status_pages SET accent = 'red' WHERE id = ?",
		"UPDATE status_pages SET accent = '#3B82F6' WHERE id = ?",
		"UPDATE status_pages SET language = 'de' WHERE id = ?",
		"UPDATE status_pages SET hide_credit = 2 WHERE id = ?",
	} {
		if _, err := db.Writer.ExecContext(t.Context(), q, p.ID); err == nil {
			t.Errorf("%s: accepted", q)
		}
	}
}

func TestLogoIsStoredReplacedAndOnlyPublishedPublicly(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectMonitors})
	first, err := db.SetStatusPageLogo(ctx, p.ID, StatusPageLogoUpload{ContentType: "image/png", Width: 40, Height: 20, Data: []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}\.png$`).MatchString(first.FileName()) {
		t.Errorf("file name %q is not a random key with the type's extension", first.FileName())
	}
	read, err := db.GetStatusPage(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Logo == nil || read.Logo.Key != first.Key || read.Logo.Width != 40 || read.Logo.Bytes != 3 {
		t.Errorf("page reads logo %+v, want the one stored", read.Logo)
	}

	// Off: the public lookup must not find it, though the operator's does.
	if _, _, err := db.PublishedStatusPageLogo(ctx, first.FileName()); !errors.Is(err, ErrNotFound) {
		t.Errorf("logo of a page that is off: err = %v, want ErrNotFound", err)
	}
	if _, data, err := db.StatusPageLogoData(ctx, p.ID); err != nil || string(data) != "one" {
		t.Errorf("operator's copy = %q, %v", data, err)
	}
	p.Enabled = true
	if _, err := db.UpdateStatusPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, data, err := db.PublishedStatusPageLogo(ctx, first.FileName()); err != nil || string(data) != "one" {
		t.Errorf("published logo = %q, %v", data, err)
	}
	for _, name := range []string{first.Key + ".jpg", first.Key, "../" + first.FileName(), strings.ToUpper(first.FileName())} {
		if _, _, err := db.PublishedStatusPageLogo(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("PublishedStatusPageLogo(%q): err = %v, want ErrNotFound", name, err)
		}
	}

	// A new upload is a new address; the old one stops answering.
	second, err := db.SetStatusPageLogo(ctx, p.ID, StatusPageLogoUpload{ContentType: "image/webp", Width: 8, Height: 8, Data: []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	if second.Key == first.Key {
		t.Error("a replaced logo kept its key, so a cache would keep serving the old file")
	}
	if _, _, err := db.PublishedStatusPageLogo(ctx, first.FileName()); !errors.Is(err, ErrNotFound) {
		t.Errorf("the replaced logo still answers: %v", err)
	}

	if err := db.DeleteStatusPageLogo(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteStatusPageLogo(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
	if read, _ := db.GetStatusPage(ctx, p.ID); read.Logo != nil {
		t.Errorf("logo after delete = %+v", read.Logo)
	}
}

func TestLogoRefusesWhatTheTableWould(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	p := seedStatusPage(t, db, StatusPage{Slug: "acme", Title: "Acme", Selection: StatusPageSelectMonitors})
	for name, up := range map[string]StatusPageLogoUpload{
		"svg":   {ContentType: "image/svg+xml", Width: 1, Height: 1, Data: []byte("<svg/>")},
		"empty": {ContentType: "image/png", Width: 1, Height: 1},
		"big":   {ContentType: "image/png", Width: 1, Height: 1, Data: bytes.Repeat([]byte{1}, MaxStatusPageLogoBytes+1)},
		"size":  {ContentType: "image/png", Data: []byte("x")},
	} {
		if _, err := db.SetStatusPageLogo(ctx, p.ID, up); err == nil {
			t.Errorf("%s: stored", name)
		}
	}
	if _, err := db.SetStatusPageLogo(ctx, 999, StatusPageLogoUpload{ContentType: "image/png", Width: 1, Height: 1, Data: []byte("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing page: err = %v, want ErrNotFound", err)
	}
	// Deleting the page takes its logo with it.
	if _, err := db.SetStatusPageLogo(ctx, p.ID, StatusPageLogoUpload{ContentType: "image/png", Width: 1, Height: 1, Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteStatusPage(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Reader.QueryRowContext(ctx, "SELECT count(*) FROM status_page_logos").Scan(&n); err != nil || n != 0 {
		t.Errorf("logos left after the page was deleted: %d, %v", n, err)
	}
}

func TestLogosIsAReservedSlug(t *testing.T) {
	if _, err := NormaliseStatusPage(StatusPage{Slug: "logos", Title: "x", Selection: StatusPageSelectMonitors}); !errors.Is(err, ErrInvalidStatusPage) {
		t.Errorf("slug logos: err = %v; it would shadow the logo files beside every page", err)
	}
}
