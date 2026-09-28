package checker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
)

// JSONOperator is how a JSON assertion compares the value it found.
type JSONOperator string

const (
	// JSONEquals passes when the value has the expected type and value.
	JSONEquals JSONOperator = "equals"
	// JSONNotEquals passes when the value differs in type or value.
	JSONNotEquals JSONOperator = "not_equals"
	// JSONExists passes when the path resolves to anything, null included.
	JSONExists JSONOperator = "exists"
	// JSONLessThan passes when the value is a number below the expected one.
	JSONLessThan JSONOperator = "less_than"
	// JSONGreaterThan passes when the value is a number above the expected one.
	JSONGreaterThan JSONOperator = "greater_than"
)

// JSONOperators lists every operator, in the order an API error names them.
func JSONOperators() []string {
	return []string{
		string(JSONEquals), string(JSONNotEquals), string(JSONExists),
		string(JSONLessThan), string(JSONGreaterThan),
	}
}

// JSONAssertion is one condition on a field of a JSON response body.
//
// One path, one operator, one value, and deliberately no more: a health
// endpoint that answers 200 with {"status":"degraded"} is the case this
// covers. Several conditions, boolean logic or an expression language would
// turn a monitor setting into a query language; an endpoint that needs that
// much judgement is better served by a health route that makes the judgement
// itself and answers with one field.
type JSONAssertion struct {
	// Path is dot notation with array indexes: `checks.db.status`,
	// `items[0].ok`, `[0].id` for a top-level array. A key that itself
	// contains a dot or a bracket cannot be addressed.
	Path string

	Operator JSONOperator

	// Expected is the comparison value as JSON text: `"up"`, `1`, `true`,
	// `null`. Empty for JSONExists. It is JSON rather than a Go string so
	// that "1" and 1 stay different values, which is the whole reason for
	// checking a field instead of searching the body for a keyword.
	Expected json.RawMessage
}

// jsonStep is one hop of a parsed path: a key, or an array index.
type jsonStep struct {
	key   string
	index int
	isIdx bool
}

// parseJSONPath splits a path into its steps, or explains what is wrong with
// it. The API reaches it through ValidateJSONAssertion, so a bad path is
// refused when it is typed rather than on every check afterwards.
func parseJSONPath(path string) ([]jsonStep, error) {
	if path == "" {
		return nil, errors.New("path is empty")
	}
	var steps []jsonStep
	i := 0
	expectKey := true // the start of the path, or right after a dot
	for i < len(path) {
		switch path[i] {
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed [ at position %d", i+1)
			}
			digits := path[i+1 : i+end]
			n, err := strconv.Atoi(digits)
			if digits == "" || err != nil || n < 0 || strings.TrimLeft(digits, "0123456789") != "" {
				return nil, fmt.Errorf("[%s] is not an array index; use a whole number such as [0]", digits)
			}
			if expectKey && len(steps) > 0 {
				return nil, fmt.Errorf("a dot must be followed by a key, not [ (position %d)", i+1)
			}
			steps = append(steps, jsonStep{index: n, isIdx: true})
			i += end + 1
			expectKey = false
		case '.':
			if expectKey {
				return nil, fmt.Errorf("empty key at position %d", i+1)
			}
			expectKey = true
			i++
			if i == len(path) {
				return nil, errors.New("path ends with a dot")
			}
		case ']':
			return nil, fmt.Errorf("unexpected ] at position %d", i+1)
		default:
			if !expectKey {
				return nil, fmt.Errorf("a key must follow a dot (position %d)", i+1)
			}
			end := strings.IndexAny(path[i:], ".[]")
			if end < 0 {
				end = len(path) - i
			}
			steps = append(steps, jsonStep{key: path[i : i+end]})
			i += end
			expectKey = false
		}
	}
	return steps, nil
}

// ValidateJSONAssertion checks everything about an assertion that does not
// need a response: the path parses, the operator exists, and the expected
// value suits the operator.
//
// The error names the part at fault by its API field, "path", "operator" or
// "expected", so a caller can point at the right control.
func ValidateJSONAssertion(a JSONAssertion) (field string, err error) {
	if _, err := parseJSONPath(a.Path); err != nil {
		return "path", fmt.Errorf("invalid path %q: %w", a.Path, err)
	}
	switch a.Operator {
	case JSONExists:
		if len(a.Expected) != 0 {
			return "expected", errors.New("exists takes no expected value; omit it")
		}
		return "", nil
	case JSONEquals, JSONNotEquals, JSONLessThan, JSONGreaterThan:
	default:
		return "operator", fmt.Errorf("unknown operator %q; use one of %s",
			a.Operator, strings.Join(JSONOperators(), ", "))
	}
	if len(a.Expected) == 0 {
		return "expected", fmt.Errorf("%s needs an expected value", a.Operator)
	}
	want, err := decodeJSONValue(a.Expected)
	if err != nil {
		return "expected", fmt.Errorf("expected is not a JSON value: %w", err)
	}
	switch want.(type) {
	case map[string]any, []any:
		return "expected", errors.New("expected must be a string, number, boolean or null, not an object or array")
	}
	if a.Operator == JSONLessThan || a.Operator == JSONGreaterThan {
		if _, isNum := want.(json.Number); !isNum {
			return "expected", fmt.Errorf("%s compares numbers; expected is %s", a.Operator, describeJSON(want))
		}
	}
	return "", nil
}

// decodeJSONValue decodes exactly one JSON value, keeping numbers as their
// literal text so that large integers and decimals compare exactly.
func decodeJSONValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}

// evaluateJSONAssertion runs a on body. It returns "" when the assertion
// holds, and otherwise a sentence that says what was there.
//
// Every failure names the path, so the message stands on its own in an alert
// that is read without the monitor's settings to hand.
func evaluateJSONAssertion(a JSONAssertion, body []byte) string {
	steps, err := parseJSONPath(a.Path)
	if err != nil {
		// Validated on save, so only a row edited behind the API gets here.
		return fmt.Sprintf("invalid JSON path %q: %v", a.Path, err)
	}
	doc, err := decodeJSONValue(body)
	if err != nil {
		return fmt.Sprintf("response is not valid JSON, so %s could not be read", a.Path)
	}

	got, missing := resolveJSONPath(doc, steps)
	if missing != "" {
		return fmt.Sprintf("%s does not exist in the response: %s", a.Path, missing)
	}
	if a.Operator == JSONExists {
		return ""
	}

	want, err := decodeJSONValue(a.Expected)
	if err != nil {
		return fmt.Sprintf("the expected value for %s is not valid JSON", a.Path)
	}

	switch a.Operator {
	case JSONEquals:
		if jsonEqual(got, want) {
			return ""
		}
		if jsonKind(got) != jsonKind(want) {
			return fmt.Sprintf("%s was %s, expected %s", a.Path, describeJSON(got), describeJSON(want))
		}
		return fmt.Sprintf("%s was %s, expected %s", a.Path, compactJSON(got), compactJSON(want))
	case JSONNotEquals:
		if !jsonEqual(got, want) {
			return ""
		}
		return fmt.Sprintf("%s was %s, expected anything else", a.Path, compactJSON(got))
	case JSONLessThan, JSONGreaterThan:
		word := "less than"
		if a.Operator == JSONGreaterThan {
			word = "greater than"
		}
		gotNum, isNum := got.(json.Number)
		if !isNum {
			return fmt.Sprintf("%s was %s, expected a number %s %s", a.Path, describeJSON(got), word, compactJSON(want))
		}
		g, gok := new(big.Rat).SetString(gotNum.String())
		w, wok := new(big.Rat).SetString(want.(json.Number).String())
		if !gok || !wok {
			return fmt.Sprintf("%s was %s, which could not be compared as a number", a.Path, gotNum)
		}
		cmp := g.Cmp(w)
		if (a.Operator == JSONLessThan && cmp < 0) || (a.Operator == JSONGreaterThan && cmp > 0) {
			return ""
		}
		return fmt.Sprintf("%s was %s, expected %s %s", a.Path, gotNum, word, compactJSON(want))
	}
	return fmt.Sprintf("unknown JSON assertion operator %q", a.Operator)
}

// resolveJSONPath walks steps through doc. When a step cannot be taken it
// returns a description of where the walk stopped, so "does not exist" says
// which part of the path was missing rather than leaving the reader to bisect
// it by hand.
func resolveJSONPath(doc any, steps []jsonStep) (value any, missing string) {
	cur := doc
	walked := ""
	for _, s := range steps {
		where := walked
		if where == "" {
			where = "the top level"
		}
		if s.isIdx {
			arr, isArr := cur.([]any)
			if !isArr {
				return nil, fmt.Sprintf("%s is %s, not an array", where, describeKind(cur))
			}
			if s.index >= len(arr) {
				return nil, fmt.Sprintf("%s has %d item(s), so there is no [%d]", where, len(arr), s.index)
			}
			cur = arr[s.index]
			walked += "[" + strconv.Itoa(s.index) + "]"
			continue
		}
		obj, isObj := cur.(map[string]any)
		if !isObj {
			return nil, fmt.Sprintf("%s is %s, not an object", where, describeKind(cur))
		}
		next, found := obj[s.key]
		if !found {
			return nil, fmt.Sprintf("%s has no key %q", where, s.key)
		}
		cur = next
		if walked != "" {
			walked += "."
		}
		walked += s.key
	}
	return cur, ""
}

// jsonEqual compares two decoded values with their types: the string "1" is
// not the number 1, and false is not null. Numbers compare by value, so 1 and
// 1.0 are equal.
func jsonEqual(a, b any) bool {
	if jsonKind(a) != jsonKind(b) {
		return false
	}
	switch av := a.(type) {
	case json.Number:
		x, xok := new(big.Rat).SetString(av.String())
		y, yok := new(big.Rat).SetString(b.(json.Number).String())
		if !xok || !yok {
			return av.String() == b.(json.Number).String()
		}
		return x.Cmp(y) == 0
	case string:
		return av == b.(string)
	case bool:
		return av == b.(bool)
	case nil:
		return true
	default:
		// Objects and arrays are never an expected value, so a composite
		// value found at the path is simply not equal to it.
		return false
	}
}

// jsonKind names the JSON type of a decoded value.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "value"
}

// describeKind is jsonKind with its article, for the middle of a sentence.
func describeKind(v any) string {
	switch k := jsonKind(v); k {
	case "null":
		return "null"
	case "array", "object":
		return "an " + k
	default:
		return "a " + k
	}
}

// describeJSON names a value together with its type, for messages where the
// type is the point: `the string "1"`, `the number 1`.
func describeJSON(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "the " + jsonKind(v) + " " + compactJSON(v)
}

// compactJSON renders a decoded value back as JSON, shortened so a large
// value cannot turn one failure message into a page of text.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	const limit = 120
	if len(b) > limit {
		return string(b[:limit]) + "…"
	}
	return string(b)
}
