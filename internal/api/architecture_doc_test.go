package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const architecturePath = "../../docs/ARCHITECTURE.md"

// TestArchitectureRoutesAreServed holds the endpoint list in ARCHITECTURE.md
// §4 to the route table the server is built from.
//
// That list is the first description of the API most readers meet, and it
// drifted while the spec stayed correct: it named an acknowledge route that
// was never served, an incident lookup that does not exist and query
// parameters the handlers do not read. The spec has its own test; this is the
// same check for the short version.
func TestArchitectureRoutesAreServed(t *testing.T) {
	raw, err := os.ReadFile(architecturePath)
	if err != nil {
		t.Fatalf("read %s: %v", architecturePath, err)
	}
	doc := string(raw)

	const heading = "\n## 4. API design\n"
	start := strings.Index(doc, heading)
	if start == -1 {
		t.Fatalf("%s has no %q section; the check is broken", architecturePath, strings.TrimSpace(heading))
	}
	section := doc[start+len(heading):]
	if next := strings.Index(section, "\n## "); next != -1 {
		section = section[:next]
	}

	// The first fenced block of the section is the endpoint list.
	open := strings.Index(section, "```\n")
	if open == -1 {
		t.Fatalf("%s §4 has no endpoint list; the check is broken", architecturePath)
	}
	block := section[open+len("```\n"):]
	if end := strings.Index(block, "```"); end != -1 {
		block = block[:end]
	}

	var srv Server
	served := map[string]bool{}
	for _, rt := range srv.routes() {
		served[routeKey(rt)] = true
	}

	line := regexp.MustCompile(`(?m)^(GET|POST|PUT|PATCH|DELETE)\s+(/\S*)`)
	found := line.FindAllStringSubmatch(block, -1)
	if len(found) == 0 {
		t.Fatalf("%s §4 lists no endpoints; the check is broken", architecturePath)
	}
	for _, m := range found {
		path, _, _ := strings.Cut(m[2], "?")
		if key := m[1] + " " + path; !served[key] {
			t.Errorf("%s §4 lists %s, which the server does not serve", architecturePath, key)
		}
	}
}
