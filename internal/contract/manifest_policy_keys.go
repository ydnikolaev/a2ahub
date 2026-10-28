package contract

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ErrManifestPolicyKeysUnreadable reports a space.yaml that is not a YAML
// mapping, so no policy key can be read from it.
// ErrManifestPolicyKeysUnreadable is part of the public package API.
var ErrManifestPolicyKeysUnreadable = errors.New("manifest policy keys: space.yaml is not a readable YAML mapping")

// ErrManifestPolicyKeysAmbiguous reports a space.yaml whose `gates` or `flow`
// key can be read two ways: a top-level merge key, a duplicated policy key, or
// an alias reaching either value. Two YAML readers disagree on these forms, so
// the decoder interprets none of them.
// ErrManifestPolicyKeysAmbiguous is part of the public package API.
var ErrManifestPolicyKeysAmbiguous = errors.New("manifest policy keys: gates/flow are ambiguous (merge key, duplicate key or alias)")

// ManifestPolicyKeys holds the raw `gates` and `flow` values of a space.yaml,
// before any vocabulary check. A nil field means the key is absent or null.
// ManifestPolicyKeys is part of the public package API.
type ManifestPolicyKeys struct {
	Gates any
	Flow  any
}

// DecodeManifestPolicyKeys reads the `gates` and `flow` keys of a space.yaml
// through one decoder that the write-time validator and the land-time reader
// share (spec 04 section 10, P4-B carries item 8). It walks the YAML node tree
// and refuses every form on which a struct decode and a node walk disagree: a
// top-level merge key (which a struct decode honours and a node walk does not),
// a duplicated policy key, and any alias inside the `gates` or `flow` value.
// An anchor on a value is not an alias and decodes normally.
// DecodeManifestPolicyKeys is part of the public package API.
func DecodeManifestPolicyKeys(raw []byte) (ManifestPolicyKeys, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return ManifestPolicyKeys{}, fmt.Errorf("%w: %w", ErrManifestPolicyKeysUnreadable, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return ManifestPolicyKeys{}, ErrManifestPolicyKeysUnreadable
	}
	root := doc.Content[0]

	var keys ManifestPolicyKeys
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind == yaml.AliasNode || isMergeKey(key) {
			return ManifestPolicyKeys{}, fmt.Errorf("%w: top-level key at line %d", ErrManifestPolicyKeysAmbiguous, key.Line)
		}
		if key.Kind != yaml.ScalarNode || (key.Value != "gates" && key.Value != "flow") {
			continue
		}
		if seen[key.Value] {
			return ManifestPolicyKeys{}, fmt.Errorf("%w: %q appears twice", ErrManifestPolicyKeysAmbiguous, key.Value)
		}
		seen[key.Value] = true
		if hasAlias(value) {
			return ManifestPolicyKeys{}, fmt.Errorf("%w: %q holds an alias", ErrManifestPolicyKeysAmbiguous, key.Value)
		}
		var decoded any
		if err := value.Decode(&decoded); err != nil {
			return ManifestPolicyKeys{}, fmt.Errorf("%w: %q: %w", ErrManifestPolicyKeysUnreadable, key.Value, err)
		}
		if key.Value == "gates" {
			keys.Gates = decoded
		} else {
			keys.Flow = decoded
		}
	}
	return keys, nil
}

// isMergeKey mirrors yaml.v3's own merge-key test: the plain scalar `<<` with
// no explicit tag, or the explicit !!merge tag. A quoted "<<" is an ordinary
// string key.
func isMergeKey(n *yaml.Node) bool {
	if n.Kind != yaml.ScalarNode {
		return false
	}
	if n.ShortTag() == "!!merge" {
		return true
	}
	return n.Value == "<<" && n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) == 0 && (n.Tag == "" || n.Tag == "!")
}

func hasAlias(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range n.Content {
		if hasAlias(child) {
			return true
		}
	}
	return false
}
