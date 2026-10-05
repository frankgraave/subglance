package notifier

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Paths of the two documents that state which channel types exist.
const (
	readmePath   = "../../README.md"
	channelsPath = "../../docs/channels.md"
)

// numberWords spells the channel count the way the README writes it.
var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six",
	"seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen",
	"fifteen", "sixteen"}

// deliverableTypes is the channel set a running server can actually send
// through: the default sender map, not a list kept in this test.
//
// The map is the source because it is the last place a new type has to be
// added before it delivers anything. Reading it here means the docs are
// checked against what ships, and a type added to the code without a word in
// the docs fails below rather than in a reader's first week.
func deliverableTypes(t *testing.T) []string {
	t.Helper()
	n := New(Options{})
	types := make([]string, 0, len(n.senders))
	for typ := range n.senders {
		types = append(types, typ)
	}
	slices.Sort(types)
	if len(types) >= len(numberWords) {
		t.Fatalf("%d channel types: extend numberWords", len(types))
	}
	return types
}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// TestReadmeNamesEveryChannelType guards the README's two claims about
// channels: how many there are, and which. Both went stale once already: the
// count said five while eight were delivering, and the status table left out
// SMS for as long as SMS had existed.
func TestReadmeNamesEveryChannelType(t *testing.T) {
	types := deliverableTypes(t)
	readme := readDoc(t, readmePath)

	// The status table's notifications row names every type.
	row := regexp.MustCompile(`(?m)^\| Notifications — ([^|]+)\|`).FindStringSubmatch(readme)
	if row == nil {
		t.Fatalf("%s has no `| Notifications — ... |` row in its status table; the check is broken", readmePath)
	}
	named := map[string]bool{}
	for _, name := range strings.Split(row[1], ",") {
		named[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, typ := range types {
		if !named[typ] {
			t.Errorf("README status table: the Notifications row does not name %q", typ)
		}
		delete(named, typ)
	}
	for name := range named {
		t.Errorf("README status table: the Notifications row names %q, which is not a channel type", name)
	}

	// Every count of channels in the README is the real one.
	counts := regexp.MustCompile(`(?i)\b(`+strings.Join(numberWords, "|")+
		`) (?:notification channels|channel types)\b`).FindAllStringSubmatch(readme, -1)
	if len(counts) == 0 {
		t.Fatalf("%s states no channel count; the check is broken", readmePath)
	}
	want := numberWords[len(types)]
	for _, c := range counts {
		if strings.ToLower(c[1]) != want {
			t.Errorf("README says %q; there are %s channel types", c[0], want)
		}
	}
}

// TestChannelsDocHasARowPerType checks the settings table in docs/channels.md,
// the one reference for what each type needs: one row per type the server
// delivers through, and no row for a type it does not.
func TestChannelsDocHasARowPerType(t *testing.T) {
	types := deliverableTypes(t)
	doc := readDoc(t, channelsPath)

	// Bounded to the one table under its header, up to the blank line that
	// ends it: the payload table further down also starts its rows with a
	// backticked word, and must not count as a list of channel types.
	const header = "| Type | Required | Optional |"
	start := strings.Index(doc, header)
	if start == -1 {
		t.Fatalf("%s has no %q table; the check is broken", channelsPath, header)
	}
	table := doc[start:]
	if end := strings.Index(table, "\n\n"); end != -1 {
		table = table[:end]
	}

	var rows []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(table, -1) {
		rows = append(rows, m[1])
	}
	slices.Sort(rows)
	if !slices.Equal(rows, types) {
		t.Errorf("%s settings table has rows %v; the server delivers through %v", channelsPath, rows, types)
	}
}

// TestWebhookPayloadDocNamesEveryField ties the webhook payload section of
// docs/channels.md to the Alert struct the webhook posts as-is. The section
// is the contract a receiving script is written against, so a field added to
// Alert without a line there is a field nobody was told about, and a field
// documented there but gone from Alert is a script reading nothing.
func TestWebhookPayloadDocNamesEveryField(t *testing.T) {
	doc := readDoc(t, channelsPath)
	const heading = "\n## The webhook payload\n"
	start := strings.Index(doc, heading)
	if start == -1 {
		t.Fatalf("%s has no %q section; the check is broken", channelsPath, strings.TrimSpace(heading))
	}
	section := doc[start+len(heading):]
	if next := strings.Index(section, "\n## "); next != -1 {
		section = section[:next]
	}

	// The first column of the field table: one or more backticked names.
	documented := map[string]bool{}
	for _, row := range regexp.MustCompile(`(?m)^\| ([^|]+) \|`).FindAllStringSubmatch(section, -1) {
		for _, name := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(row[1], -1) {
			documented[name[1]] = true
		}
	}
	if len(documented) == 0 {
		t.Fatalf("%s: the webhook payload section has no field table; the check is broken", channelsPath)
	}

	alert := reflect.TypeFor[Alert]()
	for i := range alert.NumField() {
		tag := alert.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		if !documented[name] {
			t.Errorf("%s: the webhook payload table does not describe %q", channelsPath, name)
		}
		delete(documented, name)
	}
	for name := range documented {
		t.Errorf("%s: the webhook payload table describes %q, which the payload does not have", channelsPath, name)
	}
}

// TestWebhookBodyDocMatchesTheTemplates ties the "A body of your own" section
// of docs/channels.md to the code: its table names exactly the placeholders a
// template may use, and every JSON example in it is one the API accepts. The
// examples are what people paste into the form; one that the save refuses is
// a support question before anyone has seen an alert.
func TestWebhookBodyDocMatchesTheTemplates(t *testing.T) {
	doc := readDoc(t, channelsPath)
	const heading = "\n## A body of your own\n"
	start := strings.Index(doc, heading)
	if start == -1 {
		t.Fatalf("%s has no %q section; the check is broken", channelsPath, strings.TrimSpace(heading))
	}
	section := doc[start+len(heading):]
	if next := strings.Index(section, "\n## "); next != -1 {
		section = section[:next]
	}

	var documented []string
	for _, row := range regexp.MustCompile(`(?m)^\| ([^|]+) \|`).FindAllStringSubmatch(section, -1) {
		for _, name := range regexp.MustCompile(`\{\{([a-z_]+)\}\}`).FindAllStringSubmatch(row[1], -1) {
			documented = append(documented, name[1])
		}
	}
	slices.Sort(documented)
	if want := WebhookPlaceholderNames(); !slices.Equal(documented, want) {
		t.Errorf("%s placeholder table names %v; a template may use %v", channelsPath, documented, want)
	}

	examples := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllStringSubmatch(section, -1)
	if len(examples) < 3 {
		t.Fatalf("%s: %d JSON examples in the body section, want Teams, Matrix and Pushover", channelsPath, len(examples))
	}
	for i, ex := range examples {
		if err := validateWebhookBody(ex[1], true); err != nil {
			t.Errorf("%s: body example %d is refused: %v\n%s", channelsPath, i+1, err, ex[1])
		}
	}
}
