package statuspage

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Every language the store accepts has a table, and every table only
// languages the store accepts.
func TestEveryStoreLanguageHasTexts(t *testing.T) {
	for _, lang := range store.StatusPageLanguages {
		tx, ok := languages[lang]
		if !ok {
			t.Errorf("language %q is accepted on save but has no texts", lang)
			continue
		}
		if tx.Lang != lang {
			t.Errorf("table %q says lang=%q", lang, tx.Lang)
		}
	}
	if len(languages) != len(store.StatusPageLanguages) {
		t.Errorf("%d tables for %d languages", len(languages), len(store.StatusPageLanguages))
	}
}

var verb = regexp.MustCompile(`%[a-z]`)

// A text missing from a table would print as nothing on that language's
// page, and a format string whose verbs differ from English would print
// %!d(MISSING) or drop a number. Both fail here, field by field.
func TestEveryTextIsTranslated(t *testing.T) {
	en := reflect.ValueOf(english)
	for lang, tx := range languages {
		v := reflect.ValueOf(*tx)
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			got, want := v.Field(i), en.Field(i)
			switch got.Kind() {
			case reflect.String:
				if got.String() == "" {
					t.Errorf("%s: %s is empty", lang, name)
				}
				if g, w := verb.FindAllString(got.String(), -1), verb.FindAllString(want.String(), -1); !reflect.DeepEqual(g, w) {
					t.Errorf("%s: %s has verbs %v, English has %v", lang, name, g, w)
				}
			case reflect.Array:
				for j := 0; j < got.Len(); j++ {
					if got.Index(j).String() == "" {
						t.Errorf("%s: %s[%d] is empty", lang, name, j)
					}
				}
			default:
				t.Errorf("%s: field %s of kind %s is not checked by this test", lang, name, got.Kind())
			}
		}
	}
}

// The template prints no word of its own: text between tags is an action
// or nothing, so a word added there could not be left untranslated.
func TestTemplateWritesNoFixedText(t *testing.T) {
	body := pageTemplate[strings.Index(pageTemplate, "<!DOCTYPE"):]
	body = regexp.MustCompile(`(?s)\{\{.*?\}\}`).ReplaceAllString(body, "")
	for _, m := range regexp.MustCompile(`>([^<]*)<`).FindAllStringSubmatch(body, -1) {
		if text := strings.TrimSpace(m[1]); text != "" && text != "·" {
			t.Errorf("the template writes %q outside an action; put it in texts.go", text)
		}
	}
	for _, attr := range regexp.MustCompile(`(?:alt|title|aria-label|content)="([^"{]+)"`).FindAllStringSubmatch(body, -1) {
		if attr[1] != "width=device-width, initial-scale=1" && attr[1] != "dark light" && attr[1] != "utf-8" {
			t.Errorf("the template writes the attribute text %q; put it in texts.go", attr[1])
		}
	}
}

// A Dutch page says lang="nl", and none of the English words a visitor
// reads appear in it.
func TestADutchPageIsDutchThroughout(t *testing.T) {
	for _, name := range []string{"outage", "maintenance", "empty"} {
		p := previewPage(t, name)
		p.Language = "nl"
		html := render(t, p)
		if !strings.Contains(html, `<html lang="nl"`) {
			t.Errorf("%s: lang is not nl", name)
		}
		for _, english := range []string{">Up<", ">Down<", ">Degraded<", "Updated ", "Services (", "Past 14 days",
			"Maintenance", "In progress", "Now, until", "days ago", "Today", "uptime,", "Times in", "Monitored with",
			"No services", "service is", "services are", "Last 90 days", "Recovered", "Down since", "so far", "Affects"} {
			if strings.Contains(html, english) {
				t.Errorf("%s: the Dutch page contains the English %q", name, english)
			}
		}
	}
	html := render(t, func() Page { p := previewPage(t, "outage"); p.Language = "nl"; return p }())
	for _, want := range []string{"1 van 5 diensten heeft een storing", "Diensten (5)", "Afgelopen 14 dagen",
		"99,71% beschikbaar, 90 dagen", "Tijden in Europe/Amsterdam · Bewaakt met SubGlance", "Bijgewerkt "} {
		if !strings.Contains(html, want) {
			t.Errorf("the Dutch outage page lacks %q", want)
		}
	}
}

func TestAnUnknownLanguageRendersInEnglish(t *testing.T) {
	p := previewPage(t, "allup")
	p.Language = "xx"
	if html := render(t, p); !strings.Contains(html, `<html lang="en"`) || !strings.Contains(html, "All 5 services are up") {
		t.Error("an unknown language must fall back to English, not to empty texts")
	}
}

func TestDutchDatesAndDurations(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 10, 7, 14, 32, 0, 0, loc)
	if got := dutch.updated(now); got != "wo 7 okt, 14:32" {
		t.Errorf("updated = %q", got)
	}
	start := time.Date(2026, 9, 28, 23, 50, 0, 0, loc)
	end := start.Add(20 * time.Minute)
	if got := dutch.span(start, end, time.Date(2026, 9, 29, 9, 0, 0, 0, loc)); got != "Gisteren, 23:50 – Vandaag, 00:10" {
		t.Errorf("span = %q", got)
	}
	if got := dutch.duration(4*time.Hour + 51*time.Minute); got != "4 u 51 min" {
		t.Errorf("duration = %q", got)
	}
	if got := dutch.duration(10 * time.Second); got != "minder dan 1 min" {
		t.Errorf("duration = %q", got)
	}
}

// The credit can be hidden; the time zone it shares a line with cannot,
// because the page's times mean nothing without it.
func TestTheCreditCanBeHiddenAndTheTimeZoneStays(t *testing.T) {
	p := previewPage(t, "allup")
	if html := render(t, p); !strings.Contains(html, `<p class="sp-foot">Times in Europe/Amsterdam · Monitored with SubGlance</p>`) {
		t.Error("the credit is shown by default")
	}
	p.CreditShown = false
	html := render(t, p)
	if strings.Contains(html, "SubGlance") {
		t.Error("a hidden credit still names SubGlance")
	}
	if !strings.Contains(html, `<p class="sp-foot">Times in Europe/Amsterdam</p>`) {
		t.Error("hiding the credit must leave the time zone")
	}
}

// The accent arrives as its own stylesheet, allowed by its own hash, and
// nothing that is not #rrggbb gets as far as the style element.
func TestTheAccentIsAHashedStylesheet(t *testing.T) {
	r := newTestRenderer(t)
	p := previewPage(t, "allup")
	p.Accent = "#3b82f6"
	b, err := r.Render(p)
	if err != nil {
		t.Fatal(err)
	}
	styles := styleElement.FindAllStringSubmatch(string(b), -1)
	if len(styles) != 2 || styles[1][1] != ".sp-head h1{color:#3b82f6}" {
		t.Fatalf("styles = %q, want the page's and the accent's", styles)
	}
	csp := r.ContentSecurityPolicyFor(p)
	for _, s := range styles {
		if !strings.Contains(csp, hashOf(s[1])) {
			t.Errorf("policy %q does not allow %q", csp, s[1])
		}
	}
	if csp == r.ContentSecurityPolicy() {
		t.Error("an accented page needs a policy of its own")
	}

	for _, hostile := range []string{"red", "#3b82f6}body{display:none", "</style><script>alert(1)</script>", "#3B82F6"} {
		p.Accent = hostile
		b, err := r.Render(p)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(styleElement.FindAllString(string(b), -1)); n != 1 {
			t.Errorf("accent %q produced %d style elements", hostile, n)
		}
		if r.ContentSecurityPolicyFor(p) != r.ContentSecurityPolicy() {
			t.Errorf("accent %q changed the policy", hostile)
		}
	}
}

// The logo is an image beside the page: a relative address, its size
// reserved, no text of its own (the title next to it names the page), and
// allowed by img-src 'self' only.
func TestTheLogoIsARelativeImage(t *testing.T) {
	r := newTestRenderer(t)
	p := previewPage(t, "allup")
	if strings.Contains(render(t, p), "<img") {
		t.Error("a page without a logo draws an image")
	}
	p.Logo = &Logo{Path: "/status/logos/0123456789abcdef.png", Width: 120, Height: 40}
	html := render(t, p)
	if !strings.Contains(html, `<img class="sp-logo" src="logos/0123456789abcdef.png" width="120" height="40" alt="">`) {
		t.Errorf("logo markup missing or changed:\n%s", html[:min(len(html), 4000)])
	}
	if !strings.Contains(r.ContentSecurityPolicy(), "img-src 'self'") {
		t.Error("the policy must allow the logo from this origin, and only from it")
	}
	p.Logo.Path = "https://evil.example/x.png"
	if strings.Contains(render(t, p), "<img") {
		t.Error("a logo path off this server must not be drawn")
	}
}
