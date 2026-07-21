package validate

import (
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// refEntry mirrors one entry of an envelope's `refs[]` field (§5.2,
// §3.8) — just enough to run the referential class's digest-pin check;
// full structural validation of this shape is the schema class's job.
type refEntry struct {
	Ref  string `yaml:"ref"`
	Note string `yaml:"note"`
}

// envelope is validate's own minimal, partial decode of an artifact's
// frontmatter — only the fields the referential/authz/lifecycle/policy
// classes need to read directly. It is NOT a full §5.2 model (that
// would duplicate the schema class's job); a field absent here is either
// schema-class-only territory or simply unused by this package's own
// logic.
type envelope struct {
	Schema         string     `yaml:"schema"`
	ID             string     `yaml:"id"`
	Type           string     `yaml:"type"`
	From           string     `yaml:"from"`
	To             any        `yaml:"to"` // []string or the literal "all"
	Category       string     `yaml:"category"`
	Blocking       *bool      `yaml:"blocking"`
	Refs           []refEntry `yaml:"refs"`
	Classification string     `yaml:"classification"`
}

// decodeEnvelope parses fm's raw YAML bytes into both a typed envelope
// (for this package's own field reads) and a plain JSON-Schema-
// validatable instance (for internal/schema's ValidateEnvelope) via
// schema.DecodeYAMLInstance — never a naive yaml.Unmarshal-into-any +
// json-roundtrip, which would silently corrupt date/date-time field
// values (see that function's doc comment). A failure here is CC-001
// (malformed frontmatter YAML) territory, already screened by
// internal/artifact.ParseFrontmatter before this is called.
func decodeEnvelope(yamlBytes []byte) (envelope, any, error) {
	var env envelope
	if err := yaml.Unmarshal(yamlBytes, &env); err != nil {
		return envelope{}, nil, err
	}
	instance, err := schema.DecodeYAMLInstance(yamlBytes)
	if err != nil {
		return envelope{}, nil, err
	}
	return env, instance, nil
}

// toSystems normalizes the `to` field into a slice of system IDs; "all"
// (broadcasts) returns (nil, true).
func toSystems(to any) (systems []string, isAll bool) {
	switch v := to.(type) {
	case string:
		if v == "all" {
			return nil, true
		}
		return []string{v}, false
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				systems = append(systems, s)
			}
		}
		return systems, false
	case []string:
		return v, false
	default:
		return nil, false
	}
}
