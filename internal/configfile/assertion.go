package configfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// JSONAssertion is an HTTP monitor's condition on one field of a JSON
// response body.
//
// Expected is the comparison value as JSON text (`"up"`, `1`, `true`, `null`),
// empty for the exists operator. In a file it is written as a plain YAML
// value, so `expected: up` is the string and `expected: 1` the number, and the
// typed difference the check depends on survives the round trip: `"1"` and 1
// are different values in both formats.
type JSONAssertion struct {
	Path     string
	Operator string
	Expected string
}

// Assertion reads the monitor's json_assertion field. set is false when the
// field is absent, which on import keeps what the monitor has; a nil
// assertion with set true is an explicit `json_assertion: null`, which
// removes it.
//
// The field is a yaml.Node rather than a pointer because YAML's null and an
// absent key both decode to a nil pointer, and the two mean different things
// here.
func (m Monitor) Assertion(path string) (a *JSONAssertion, set bool, err error) {
	n := m.JSONAssertion
	switch {
	case n.Kind == 0:
		return nil, false, nil
	case n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null":
		return nil, true, nil
	case n.Kind != yaml.MappingNode:
		return nil, true, problemf(path, "must be a mapping with path, operator and expected, or null")
	}
	out := &JSONAssertion{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		switch key {
		case "path", "operator":
			if val.Kind != yaml.ScalarNode || val.ShortTag() != "!!str" {
				return nil, true, problemf(field(path, key), "must be a string")
			}
			if key == "path" {
				out.Path = val.Value
			} else {
				out.Operator = val.Value
			}
		case "expected":
			text, err := scalarJSON(val)
			if err != nil {
				return nil, true, problemf(field(path, "expected"), "%s", err.Error())
			}
			out.Expected = text
		default:
			return nil, true, problemf(path, "field %s not found in the json_assertion type", key)
		}
	}
	return out, true, nil
}

// field names a member of the object at path, in the dotted style a person
// reads: field("monitors[0]", "key") is monitors[0].key.
func field(path, name string) string { return path + "." + name }

// scalarJSON renders a YAML scalar as JSON text, keeping its type.
func scalarJSON(n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("must be a single value: a string, number, true, false or null")
	}
	switch n.ShortTag() {
	case "!!str":
		b, err := json.Marshal(n.Value)
		return string(b), err
	case "!!null":
		return "null", nil
	case "!!bool":
		var v bool
		if err := n.Decode(&v); err != nil {
			return "", err
		}
		return strconv.FormatBool(v), nil
	case "!!int", "!!float":
		// The text is kept as written rather than decoded, so a large
		// integer is not rounded through float64 on the way. YAML spellings
		// JSON has no form for (0x1F, .inf) are refused.
		var probe any
		dec := json.NewDecoder(bytes.NewReader([]byte(n.Value)))
		dec.UseNumber()
		if err := dec.Decode(&probe); err != nil || dec.More() {
			return "", fmt.Errorf("%q is not a number JSON can express; write it in decimal", n.Value)
		}
		if _, ok := probe.(json.Number); !ok {
			return "", fmt.Errorf("%q is not a number JSON can express; write it in decimal", n.Value)
		}
		return n.Value, nil
	default:
		return "", fmt.Errorf("unsupported value of type %s", n.ShortTag())
	}
}

// AssertionNode is the inverse of Monitor.Assertion, for export. A nil
// assertion is written as an explicit null, so a file exported from a monitor
// without one also removes one on import.
func AssertionNode(a *JSONAssertion) (yaml.Node, error) {
	if a == nil {
		return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	n := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		str("path"), str(a.Path), str("operator"), str(a.Operator),
	}}
	if a.Expected == "" {
		return n, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(a.Expected)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return yaml.Node{}, fmt.Errorf("stored expected value %q is not JSON: %w", a.Expected, err)
	}
	var val *yaml.Node
	switch x := v.(type) {
	case string:
		val = str(x)
	case json.Number:
		tag := "!!int"
		if _, err := x.Int64(); err != nil {
			tag = "!!float"
		}
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: x.String()}
	case bool:
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case nil:
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	default:
		return yaml.Node{}, fmt.Errorf("stored expected value %q is not a single value", a.Expected)
	}
	n.Content = append(n.Content, str("expected"), val)
	return n, nil
}
