package api

import (
	"bytes"
	"encoding/json"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// jsonAssertionWire is the API shape of a JSON body assertion.
//
// Expected is any JSON scalar, written as itself: `"up"` is a string, `1` a
// number, `true` a boolean. Taking it as raw JSON rather than as a Go string
// is what keeps "1" and 1 apart all the way from the request to the check.
type jsonAssertionWire struct {
	Path     string          `json:"path"`
	Operator string          `json:"operator"`
	Expected json.RawMessage `json:"expected,omitempty"`
}

// jsonAssertionFromWire validates a wire assertion and converts it for the
// store. A nil input is no assertion.
//
// Problems name the sub-field at fault, `json_assertion.path` and so on, so a
// form with three controls can point at the one that is wrong.
func jsonAssertionFromWire(w *jsonAssertionWire) (*store.JSONAssertion, problem) {
	if w == nil {
		return nil, problem{}
	}
	// `"expected": null` is the value null, which equals can compare
	// against, so it must not read as "omitted". encoding/json hands a
	// literal null to a RawMessage as its four bytes; only a missing key
	// leaves it empty. json_assertion_test.go pins that.
	expected := w.Expected
	a := checker.JSONAssertion{Path: w.Path, Operator: checker.JSONOperator(w.Operator), Expected: expected}
	if field, err := checker.ValidateJSONAssertion(a); err != nil {
		return nil, fieldProblem("json_assertion."+field, err.Error())
	}
	stored := &store.JSONAssertion{Path: w.Path, Operator: w.Operator}
	if len(expected) > 0 {
		var compact bytes.Buffer
		// Validated above, so this cannot fail; compacting keeps what is
		// stored and echoed independent of the caller's whitespace.
		if err := json.Compact(&compact, expected); err != nil {
			return nil, fieldProblem("json_assertion.expected", "expected is not a JSON value")
		}
		stored.Expected = compact.String()
	}
	return stored, problem{}
}

// jsonAssertionToWire is the read side of jsonAssertionFromWire.
func jsonAssertionToWire(a *store.JSONAssertion) *jsonAssertionWire {
	if a == nil {
		return nil
	}
	out := &jsonAssertionWire{Path: a.Path, Operator: a.Operator}
	if a.Expected != "" {
		out.Expected = json.RawMessage(a.Expected)
	}
	return out
}

// jsonAssertionTypeProblem refuses an assertion on a monitor that has no
// response body to read. Silently storing one would show a condition in the
// settings that no check ever evaluates.
func jsonAssertionTypeProblem(typ string) problem {
	if typ == "http" {
		return problem{}
	}
	return fieldProblem("json_assertion", "a JSON assertion applies only to http monitors")
}
