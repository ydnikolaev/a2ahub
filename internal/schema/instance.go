package schema

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DecodeYAMLInstance parses raw YAML bytes into a plain, JSON-Schema-
// validatable value (map[string]any / []any / string / float64 / bool /
// nil) — the shape jsonschema/v6's Validate expects.
//
// This is NOT the same as `yaml.Unmarshal(raw, &m)` into a generic
// map[string]any: gopkg.in/yaml.v3 implicitly resolves the YAML 1.1
// `!!timestamp` tag for bare date/date-time-looking scalars (e.g.
// `needed_by: 2026-08-20`) into a Go time.Time, which a subsequent
// json.Marshal round-trip then re-renders as a full RFC 3339
// date-*time* string ("2026-08-20T00:00:00Z") — silently corrupting a
// `"format": "date"` field's value and producing a spurious format
// violation. DecodeYAMLInstance walks the yaml.Node tree directly and
// keeps every scalar's ORIGINAL authored text for `!!str` and
// `!!timestamp` alike, so a schema's format/pattern keywords see exactly
// what was written.
func DecodeYAMLInstance(raw []byte) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return nodeToInstance(doc.Content[0])
}

func nodeToInstance(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			val, err := nodeToInstance(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[key] = val
		}
		return m, nil
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			val, err := nodeToInstance(c)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		return arr, nil
	case yaml.ScalarNode:
		return scalarToInstance(n)
	case yaml.AliasNode:
		return nodeToInstance(n.Alias)
	default:
		return nil, fmt.Errorf("schema: DecodeYAMLInstance: unsupported yaml node kind %v", n.Kind)
	}
}

func scalarToInstance(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!str", "!!timestamp":
		// Keep the exact authored text — never let yaml.v3's implicit
		// timestamp resolution reformat a date/date-time value (see
		// DecodeYAMLInstance's doc comment).
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, err
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			return nil, err
		}
		return i, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, err
		}
		return f, nil
	default:
		// Any other/custom tag: fall back to the literal text rather
		// than guessing a Go type.
		return n.Value, nil
	}
}
