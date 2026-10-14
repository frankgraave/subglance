package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// maxJSONBody is the largest request body a JSON endpoint reads. A monitor
// with a request body of its own is the largest thing the API takes as JSON,
// and a megabyte is far beyond any of them; the bound is what keeps a hostile
// client from making the server allocate its way to an out-of-memory kill.
const maxJSONBody = 1 << 20

// maxBulkJSONBody bounds the bulk tag and channel operations, whose bodies are
// a selection of monitor ids and a few names.
const maxBulkJSONBody = 256 << 10

// readJSON decodes the request body into dst and answers the request itself
// when it cannot: false means a refusal has been written and the handler
// returns. It reads at most maxJSONBody bytes.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return readJSONLimit(w, r, maxJSONBody, dst)
}

// readJSONLimit is readJSON with a limit of its own.
//
// Every JSON request body in this package is read here, so that every endpoint
// refuses a body the same way: one JSON value, no field the request type does
// not have, no more than limit bytes, and a refusal that names the request
// field at fault in the API's words rather than encoding/json's. Before this
// existed, each handler wrote the decoder's error text, which named Go types
// (`createMonitorRequest.interval_s of type int`) and set no field even when
// the decoder knew it. TestRequestBodiesAreReadByOneDecoder keeps it that way.
func readJSONLimit(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	// Reject fields the API does not know. Silently ignoring them is a trap:
	// a client that sends interval_seconds instead of interval_s gets a 201,
	// believes it configured a 5-second check, and receives the default
	// instead. A typo should be an error at the moment it is made.
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// A second value in the same body is a client bug. Acting on the
		// first one alone reports success for a request the caller did not
		// make: `{"target":"a"}{"target":"b"}` would otherwise create "a".
		if extra := dec.Decode(new(json.RawMessage)); !errors.Is(extra, io.EOF) {
			err = errTrailingValue
			var tooBig *http.MaxBytesError
			if errors.As(extra, &tooBig) {
				err = extra
			}
		}
	}
	if err == nil {
		return true
	}
	status, p := bodyRefusal(err, dst, "", limit)
	writeProblem(w, status, p)
	return false
}

// decodeStrict decodes one JSON value held in data into dst, refusing unknown
// fields and anything after the value, as readJSON does for a request body.
// It serves a part of a body kept raw so that null and absent stay apart.
func decodeStrict(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return errTrailingValue
	}
	return nil
}

// errTrailingValue is a body that holds more than one JSON value.
var errTrailingValue = errors.New("the request body must be exactly one JSON value")

// bodyRefusal turns an error from decoding into dst into the answer the API
// gives for it. prefix is the request field dst was decoded from when it is a
// part of the body rather than all of it ("json_assertion"), so a field is
// named by its whole path; limit is the size bound the body was read under,
// or 0 when there was none.
func bodyRefusal(err error, dst any, prefix string, limit int64) (int, problem) {
	var (
		tooBig  *http.MaxBytesError
		typeErr *json.UnmarshalTypeError
		syntax  *json.SyntaxError
	)
	switch {
	case errors.As(err, &tooBig):
		return http.StatusRequestEntityTooLarge,
			bodyProblem("the request body is larger than " + byteSize(limit))
	case errors.As(err, &typeErr):
		return http.StatusBadRequest, typeProblem(typeErr, dst, prefix)
	case errors.As(err, &syntax):
		return http.StatusBadRequest, bodyProblem("invalid JSON at byte " +
			strconv.FormatInt(syntax.Offset, 10) + ": " + syntax.Error())
	case errors.Is(err, io.EOF):
		return http.StatusBadRequest, bodyProblem("the request body is empty; send a JSON object")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return http.StatusBadRequest, bodyProblem("invalid JSON: the request body ends before the JSON value does")
	case errors.Is(err, errTrailingValue):
		return http.StatusBadRequest, bodyProblem(err.Error())
	}
	// A time.Time (a maintenance window's starts_at) parses itself, and
	// encoding/json passes its error on without the field it was for.
	var badTime *time.ParseError
	if errors.As(err, &badTime) {
		return http.StatusBadRequest, bodyProblem(timeFormat + ", not " + strconv.Quote(badTime.Value))
	}
	// encoding/json reports an unknown field, and a time that is not a JSON
	// string, with untyped errors whose text is the only place they show.
	msg := err.Error()
	if strings.HasPrefix(msg, "Time.UnmarshalJSON:") {
		return http.StatusBadRequest, bodyProblem(timeFormat)
	}
	if key, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		// No field: the key is not an input any form has, and a client
		// placing the message by field would have nowhere to put it.
		return http.StatusBadRequest, bodyProblem("unknown field " + key)
	}
	// A read that failed part way (the client went away) is all that is
	// left; the reader cannot act on its text.
	return http.StatusBadRequest, bodyProblem("could not read the request body")
}

// timeFormat is the refusal of a time the request body did not write as one.
const timeFormat = "a time must be an RFC 3339 string such as 2026-10-14T09:00:00Z"

// typeProblem words a value of the wrong JSON type against the request field
// it was sent for.
func typeProblem(e *json.UnmarshalTypeError, dst any, prefix string) problem {
	field := joinField(prefix, e.Field)
	if field == "" {
		// The body itself is the wrong type: an array or a string sent
		// where the endpoint takes an object.
		return bodyProblem("the request body must be " + jsonPhrase(e.Type) + ", not " + sentPhrase(e.Value))
	}
	declared := fieldType(reflect.TypeOf(dst), e.Field)
	got := elemType(e.Type)
	subject, want := field, got
	switch {
	case declared == nil, elemType(declared) == got:
	case isList(declared) && elemType(elemType(declared).Elem()) == got:
		subject = "each item in " + field
	case elemType(declared).Kind() == reflect.Map && elemType(elemType(declared).Elem()) == got:
		subject = "each value in " + field
	default:
		want = elemType(declared)
	}
	if isInteger(want) && isIntegerLiteral(e.Value) {
		return fieldProblem(field, subject+" is out of range")
	}
	return fieldProblem(field, subject+" must be "+jsonPhrase(want)+", not "+sentPhrase(e.Value))
}

// fieldType finds the Go type of the request field at a dotted JSON path
// such as "dns.expected", or nil when the path does not lead to one.
func fieldType(t reflect.Type, path string) reflect.Type {
	if t == nil || path == "" {
		return nil
	}
	for name := range strings.SplitSeq(path, ".") {
		t = elemType(t)
		for isList(t) {
			t = elemType(t.Elem())
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		f, ok := jsonField(t, name)
		if !ok {
			return nil
		}
		t = f.Type
	}
	return t
}

// jsonField finds the struct field encoding/json fills from the key name,
// looking through embedded structs as encoding/json does.
func jsonField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" && elemType(f.Type).Kind() == reflect.Struct {
			if inner, ok := jsonField(elemType(f.Type), name); ok {
				return inner, true
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		if tag == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// jsonPhrase names what a Go type accepts, in JSON's terms.
func jsonPhrase(t reflect.Type) string {
	t = elemType(t)
	switch {
	case t.Kind() == reflect.String:
		return "a string"
	case t.Kind() == reflect.Bool:
		return "true or false"
	case isInteger(t):
		return "a whole number"
	case t.Kind() == reflect.Float32, t.Kind() == reflect.Float64:
		return "a number"
	case isList(t):
		return "an array of " + jsonPlural(t.Elem())
	case t.Kind() == reflect.Map:
		return "an object whose values are " + jsonPlural(t.Elem())
	case t.Kind() == reflect.Struct:
		return "an object"
	}
	return "a JSON value"
}

// jsonPlural is jsonPhrase for the items of an array or the values of an
// object.
func jsonPlural(t reflect.Type) string {
	t = elemType(t)
	switch {
	case t.Kind() == reflect.String:
		return "strings"
	case t.Kind() == reflect.Bool:
		return "true or false"
	case isInteger(t):
		return "whole numbers"
	case t.Kind() == reflect.Float32, t.Kind() == reflect.Float64:
		return "numbers"
	case isList(t):
		return "arrays"
	case t.Kind() == reflect.Map, t.Kind() == reflect.Struct:
		return "objects"
	}
	return "JSON values"
}

// sentPhrase names what was sent, from UnmarshalTypeError.Value: a JSON kind
// ("string", "array") or, for a number, "number" and its text.
func sentPhrase(value string) string {
	if n, ok := strings.CutPrefix(value, "number "); ok {
		return n
	}
	switch value {
	case "bool":
		return "true or false"
	case "array", "object":
		return "an " + value
	}
	return "a " + value
}

// isIntegerLiteral reports whether the value sent was a number written
// without a fraction or exponent. Refused as an integer, it is one too large
// for the field, and "must be a whole number" would describe it wrongly.
func isIntegerLiteral(value string) bool {
	n, ok := strings.CutPrefix(value, "number ")
	return ok && !strings.ContainsAny(n, ".eE")
}

func isInteger(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func isList(t reflect.Type) bool {
	t = elemType(t)
	return t.Kind() == reflect.Slice || t.Kind() == reflect.Array
}

// elemType looks through pointers to the type they point at.
func elemType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func joinField(prefix, field string) string {
	switch {
	case prefix == "":
		return field
	case field == "":
		return prefix
	}
	return prefix + "." + field
}

// byteSize writes a body limit the way a person sets one: whole MiB or KiB.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
