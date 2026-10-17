package api

import (
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/store"
)

// The free-text settings of a monitor have ceilings, as its name, its tags
// and every channel setting already do. Without them a 900,000-character
// target was saved, listed on every dashboard poll, and exported into a file
// larger than the import accepts, so the backup of five such monitors could
// not be restored. Each value is counted in characters, as the name is.
const (
	// maxMonitorTextLen bounds the target, the keyword, the expected
	// status list and the JSON assertion's path and expected value. It is
	// the ceiling a channel's URL and settings already have; a URL longer
	// than that is refused by most servers anyway (nginx stops a request
	// line at 8 KB, and browsers and CDNs well before).
	maxMonitorTextLen = 2048

	// maxMonitorBodyLen bounds the request body an http monitor sends. A
	// health check posts a small document; 64 KiB leaves room for a
	// GraphQL query or a SOAP envelope without letting one field carry an
	// upload.
	maxMonitorBodyLen = 64 << 10

	// maxMonitorHeaders and maxHeaderLineLen follow Apache httpd's defaults
	// (LimitRequestFields 100, LimitRequestFieldSize 8190), the strictest
	// common server: a header past them is refused by the far end on every
	// check, with a 400 that reads like an outage of the service. 64
	// headers keeps room under the 100 for the ones SubGlance and any
	// proxy on the way add.
	maxMonitorHeaders = 64
	maxHeaderLineLen  = 8190
)

// textLimitProblem refuses a value longer than maxMonitorTextLen characters,
// naming field.
func textLimitProblem(field, value string) problem {
	if utf8.RuneCountInString(value) > maxMonitorTextLen {
		return fieldProblem(field, fmt.Sprintf("%s must be %d characters or fewer", field, maxMonitorTextLen))
	}
	return problem{}
}

// bodyLimitProblem refuses a request body longer than maxMonitorBodyLen
// characters.
func bodyLimitProblem(body string) problem {
	if utf8.RuneCountInString(body) > maxMonitorBodyLen {
		return fieldProblem("body", fmt.Sprintf("body must be %d characters or fewer", maxMonitorBodyLen))
	}
	return problem{}
}

// headersLimitProblem refuses more than maxMonitorHeaders headers, or one
// whose line as sent, `Name: value`, is longer than maxHeaderLineLen bytes.
// Bytes rather than characters, because that is what a server counts.
func headersLimitProblem(headers map[string]string) problem {
	if len(headers) > maxMonitorHeaders {
		return fieldProblem("headers", fmt.Sprintf("a monitor sends at most %d headers; this one has %d", maxMonitorHeaders, len(headers)))
	}
	// Sorted, so a request with two long headers is always blamed on the
	// same one.
	for _, k := range slices.Sorted(maps.Keys(headers)) {
		if n := len(k) + len(": ") + len(headers[k]); n > maxHeaderLineLen {
			return fieldProblem("headers", fmt.Sprintf(
				"header %.64q is %d bytes as sent; most servers refuse a header line over %d", k, n, maxHeaderLineLen))
		}
	}
	return problem{}
}

// requestTextProblem applies every limit above to the settings a create or a
// preview sends. Called before the settings are parsed, so a refusal that
// quotes the value it refuses never quotes 200,000 characters of it.
func requestTextProblem(target, keyword, expectedStatus, body string, headers map[string]string, assertion *jsonAssertionWire) problem {
	for _, f := range []struct{ field, value string }{
		{"target", target}, {"keyword", keyword}, {"expected_status", expectedStatus},
	} {
		if p := textLimitProblem(f.field, f.value); !p.ok() {
			return p
		}
	}
	if p := bodyLimitProblem(body); !p.ok() {
		return p
	}
	if p := headersLimitProblem(headers); !p.ok() {
		return p
	}
	return assertionLimitProblem(assertion, nil)
}

// assertionLimitProblem refuses a JSON assertion whose path or expected value
// is longer than maxMonitorTextLen characters. The expected value is measured
// as written, quotes included. It is a scalar (anything else is refused by the
// assertion's own validation), and a decoded scalar has no whitespace to
// compact, so what is measured is also what is stored.
//
// With stored, a path or value equal to the stored one is not measured: a
// monitor saved before the limit keeps its assertion through an edit, or an
// import of its own export, that sends it back unchanged.
func assertionLimitProblem(w *jsonAssertionWire, stored *store.JSONAssertion) problem {
	if w == nil {
		return problem{}
	}
	if stored == nil || w.Path != stored.Path {
		if p := textLimitProblem("json_assertion.path", w.Path); !p.ok() {
			return p
		}
	}
	expected := string(w.Expected)
	if stored == nil || expected != stored.Expected {
		if p := textLimitProblem("json_assertion.expected", expected); !p.ok() {
			return p
		}
	}
	return problem{}
}
