package statuspage

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// fullPage has every field set, every list non-empty and every pointer
// non-nil, so marshalling it shows every key the type can ever produce.
func fullPage() Page {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	pct := 99.5
	return Page{
		Title: "t", Description: "d", GeneratedAt: at, Timezone: "UTC",
		Summary: Summary{Up: 1},
		Entries: []Entry{{
			Key: "k", Name: "n", Status: StatusUp, InMaintenance: true, Uptime90d: &pct,
			Days: []Day{{Date: "2026-09-29", State: DayUp, DownMinutes: 1}},
		}},
		Maintenance: []Maintenance{{StartsAt: at, EndsAt: at, Keys: []string{"k"}}},
		Outages:     []Outage{{Key: "k", StartedAt: at, ResolvedAt: &at, DurationS: 1}},
	}
}

// keyPaths flattens a decoded JSON value into dotted key paths; list
// elements are written as "[]".
func keyPaths(prefix string, v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = true
			keyPaths(p, child, out)
		}
	case []any:
		for _, child := range t {
			keyPaths(prefix+"[]", child, out)
		}
	}
}

// publicFields is design §1.1 as key paths. A key that is not on this list
// fails the test below, so adding one means editing this list and the
// design document, in a diff a reviewer reads.
var publicFields = []string{
	"title", "description", "generated_at", "timezone",
	"summary", "summary.up", "summary.degraded", "summary.down", "summary.unmonitored",
	"entries",
	"entries[].key", "entries[].name", "entries[].status", "entries[].in_maintenance",
	"entries[].uptime_90d", "entries[].days",
	"entries[].days[].date", "entries[].days[].state", "entries[].days[].down_minutes",
	"maintenance", "maintenance[].starts_at", "maintenance[].ends_at", "maintenance[].keys",
	"outages", "outages[].key", "outages[].started_at", "outages[].resolved_at", "outages[].duration_s",
}

func TestPublicJSONHoldsExactlyTheDesignedFields(t *testing.T) {
	raw, err := json.Marshal(fullPage())
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	keyPaths("", decoded, got)

	want := map[string]bool{}
	for _, f := range publicFields {
		want[f] = true
	}
	var extra, missing []string
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	if len(extra) > 0 {
		t.Errorf("public JSON carries fields not in design §1.1: %v", extra)
	}
	if len(missing) > 0 {
		t.Errorf("design §1.1 fields missing from the public JSON: %v", missing)
	}
}

// Every field name the public type produces is named in design §1.1, so the
// document a reviewer checks and the code cannot describe different pages.
func TestPublicFieldsAreNamedInTheDesign(t *testing.T) {
	doc, err := os.ReadFile("../../docs/design/status-page.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "### 1.1")
	end := strings.Index(text, "### 1.2")
	if start < 0 || end < start {
		t.Fatal("design document has no §1.1 section")
	}
	section := text[start:end]
	for _, f := range publicFields {
		leaf := f[strings.LastIndex(f, ".")+1:]
		if !strings.Contains(section, "`"+leaf+"`") && !strings.Contains(section, `"`+leaf+`"`) {
			t.Errorf("field %q is not named in design §1.1", f)
		}
	}
}

// The public types may only hold plain values and other public types. A
// store type embedded here would carry its fields into the JSON the moment
// it gains one, which is the accident the dedicated type exists to prevent.
func TestPublicTypesHoldNoStoreTypes(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if seen[rt] || rt.Kind() != reflect.Struct {
			return
		}
		seen[rt] = true
		if rt == reflect.TypeOf(time.Time{}) {
			return
		}
		if rt.PkgPath() != reflect.TypeOf(Page{}).PkgPath() {
			t.Errorf("public type holds %s from %s", rt.Name(), rt.PkgPath())
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			walk(rt.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(Page{}))
}

func TestSummariseCountsNoDataAndPausedAsUnmonitored(t *testing.T) {
	got := Summarise([]Entry{
		{Status: StatusUp}, {Status: StatusUp}, {Status: StatusDegraded},
		{Status: StatusDown}, {Status: StatusNoData}, {Status: StatusNotMonitored},
	})
	want := Summary{Up: 2, Degraded: 1, Down: 1, Unmonitored: 2}
	if got != want {
		t.Errorf("Summarise = %+v, want %+v", got, want)
	}
}
