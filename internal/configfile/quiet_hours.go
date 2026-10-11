package configfile

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// quietHoursFields are the members a quiet_hours mapping may have.
var quietHoursFields = map[string]bool{"start": true, "end": true, "timezone": true, "during": true}

// Quiet reads the channel's quiet_hours field. set is false when the field is
// absent, which on import keeps the channel's quiet hours; a nil window with
// set true is an explicit `quiet_hours: null`, which removes them.
//
// The field is a yaml.Node rather than a pointer for the reason
// Monitor.JSONAssertion is one: YAML's null and an absent key both decode to
// a nil pointer, and a file exported from a channel without quiet hours has
// to be able to say so in a form an import acts on.
//
// The document decoder's unknown-field and duplicate-key checks do not reach
// inside a node, so both are done here.
func (c Channel) Quiet(path string) (q *QuietHours, set bool, err error) {
	n := c.QuietHours
	switch {
	case n.Kind == 0:
		return nil, false, nil
	case n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null":
		return nil, true, nil
	case n.Kind != yaml.MappingNode:
		return nil, true, problemf(path, "must be a mapping with start, end, timezone and during, or null")
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		switch {
		case !quietHoursFields[key]:
			return nil, true, problemf(path, "field %s not found in the quiet_hours type", key)
		case seen[key]:
			return nil, true, problemf(field(path, key), "is given twice")
		}
		seen[key] = true
	}
	out := &QuietHours{}
	if err := n.Decode(out); err != nil {
		return nil, true, problemf(path, "%s", err.Error())
	}
	return out, true, nil
}

// QuietHoursNode is the inverse of Channel.Quiet, for export. A nil window is
// written as an explicit null, so a file exported from a channel without
// quiet hours also removes them on import.
func QuietHoursNode(q *QuietHours) (yaml.Node, error) {
	if q == nil {
		return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	var n yaml.Node
	if err := n.Encode(q); err != nil {
		return yaml.Node{}, fmt.Errorf("encode quiet hours: %w", err)
	}
	return n, nil
}
