package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const operationsPath = "../../docs/operations.md"

// envKeysInOperations lists every SUBGLANCE_* variable the operations guide's
// configuration table names.
//
// The table is the reference an operator reads before changing anything, and
// it is the only place that states a default and what an option means. Scanned
// from the configuration table specifically rather than from the whole file,
// for two reasons: a variable mentioned in passing in prose does not count as
// documented, and a second table elsewhere in the guide must not be able to
// satisfy this check. The claim being guarded is "this option has a row in the
// configuration table", not "this string appears somewhere in the document".
//
// The scan is bounded to that one table rather than to every pipe-delimited
// line. Today the file has exactly one table, so an unbounded scan would agree
// — which is precisely why it is worth bounding now: the first person to add a
// second table would otherwise widen this guard without touching it, and
// nothing would say so.
func envKeysInOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(operationsPath)
	if err != nil {
		t.Fatalf("read %s: %v", operationsPath, err)
	}

	// The table under `## Configuration`, up to the blank line that ends it.
	body := string(raw)
	start := strings.Index(body, "\n## Configuration\n")
	if start == -1 {
		t.Fatalf("%s has no `## Configuration` heading; the scan is broken", operationsPath)
	}
	header := strings.Index(body[start:], "\n|---")
	if header == -1 {
		t.Fatalf("%s has no table under `## Configuration`; the scan is broken", operationsPath)
	}
	table := body[start+header:]
	if end := strings.Index(table, "\n\n"); end != -1 {
		table = table[:end]
	}

	// A row looks like:
	//   | `--flag` | `SUBGLANCE_THING` | default | meaning |
	re := regexp.MustCompile("(?m)^\\|[^|\n]*\\|\\s*`(SUBGLANCE_[A-Z_]+)`\\s*\\|")
	seen := map[string]bool{}
	var keys []string
	for _, m := range re.FindAllStringSubmatch(table, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	if len(keys) == 0 {
		t.Fatalf("found no SUBGLANCE_* rows in %s; the scan is broken", operationsPath)
	}
	sort.Strings(keys)
	return keys
}

// TestOperationsDocumentsEveryOption is the drift guard for the configuration
// table, in both directions.
//
// Its sibling TestComposeDocumentsEveryOption has protected docker-compose.yml
// since it was written, and its comment argued that "a stale example is as
// misleading as a stale table in the README" — while nothing checked that
// table. The README's configuration reference has since moved to
// docs/operations.md, and this is that missing half.
//
// The asymmetry is worth stating, because it is why prose needs a test that
// code does not: an undocumented option is not a compile error, not a failing
// check and not visible in review of the change that added it. It is simply
// absent, and the operator who needed it never learns it exists. In the other
// direction a documented option that Load does not read is worse than absent —
// someone sets it, sees no error, and believes they have changed something.
func TestOperationsDocumentsEveryOption(t *testing.T) {
	inSource := envKeysInSource(t)
	inDocs := envKeysInOperations(t)

	sourceSet := map[string]bool{}
	for _, k := range inSource {
		sourceSet[k] = true
	}
	docsSet := map[string]bool{}
	for _, k := range inDocs {
		docsSet[k] = true
	}

	for _, k := range inSource {
		if !docsSet[k] {
			t.Errorf("%s is read by Load but has no row in %s", k, operationsPath)
		}
	}
	for _, k := range inDocs {
		if !sourceSet[k] {
			t.Errorf("%s has a row in %s but Load never reads it", k, operationsPath)
		}
	}
}
