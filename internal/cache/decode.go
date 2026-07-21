package cache

// This file is this package's own minimal decode of the same underlying
// documents internal/validate and internal/cli each independently
// project their own minimal shape from (the established ISP idiom this
// repo uses at every layer boundary — see internal/cli/adapters.go's
// mirrorEvent doc comment). cache never imports internal/schema or
// internal/validate for this: it only needs the handful of fields its
// own read-model computation consumes.

import "gopkg.in/yaml.v3"

// refEntry is one envelope/event `refs[]` entry (§5.7 ref grammar).
type refEntry struct {
	Ref  string `yaml:"ref"`
	Note string `yaml:"note"`
}

// envelopeProbe is cache's own minimal envelope/v1 decode: the fields the
// inbox/outbox/show/thread/search/contracts computation needs, across
// every one of the 8 §3.1 types (response's `parent`/`result` fields are
// simply empty/zero on every other type).
type envelopeProbe struct {
	Schema         string     `yaml:"schema"`
	ID             string     `yaml:"id"`
	Type           string     `yaml:"type"`
	Title          string     `yaml:"title"`
	Space          string     `yaml:"space"`
	From           string     `yaml:"from"`
	To             any        `yaml:"to"`
	Priority       string     `yaml:"priority"`
	Blocking       bool       `yaml:"blocking"`
	NeededBy       string     `yaml:"needed_by"`
	Thread         string     `yaml:"thread"`
	Refs           []refEntry `yaml:"refs"`
	Classification string     `yaml:"classification"`
	Actor          struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"actor"`
	RequiredApprovers []string `yaml:"required_approvers"` // decision only

	// Parent/Result are response/v1-only fields (response.schema.json):
	// Parent names the artifact this response answers — the schema-
	// grounded fact this package's parent+response gather composes over
	// (plan 07 Placement decision), rather than an invented `refs[]`
	// convention.
	Parent string `yaml:"parent"`
	Result string `yaml:"result"`
}

// to0 returns the exchange's single target system (D-027), or "" if
// none/broadcast.
func (e envelopeProbe) to0() string {
	s := normalizeTo(e.To)
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// isBroadcast reports whether the envelope's `to` is the literal "all"
// broadcast form (base.schema.json's oneOf).
func (e envelopeProbe) isBroadcast() bool {
	s, ok := e.To.(string)
	return ok && s == "all"
}

// normalizeTo normalizes an envelope `to` field (either a []any of
// strings, per YAML decode, or the literal "all") into a []string — this
// package's own copy of the same small normalize internal/cli's
// adapters.go performs internally (toStringSlice, unexported there).
func normalizeTo(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	default:
		return nil
	}
}

// eventProbe is cache's own minimal event/v1 decode.
type eventProbe struct {
	Schema     string `yaml:"schema"`
	Event      string `yaml:"event"`
	Space      string `yaml:"space"`
	Subject    string `yaml:"subject"`
	Transition string `yaml:"transition"`
	State      string `yaml:"state"`
	Actor      struct {
		Kind   string `yaml:"kind"`
		Name   string `yaml:"name"`
		System string `yaml:"system"`
	} `yaml:"actor"`
	At      string     `yaml:"at"`
	Refs    []refEntry `yaml:"refs"`
	Version string     `yaml:"version"`
}

// decodeEnvelope decodes raw's YAML frontmatter block into an
// envelopeProbe.
func decodeEnvelope(yamlBlock []byte) (envelopeProbe, error) {
	var e envelopeProbe
	if err := yaml.Unmarshal(yamlBlock, &e); err != nil {
		return envelopeProbe{}, err
	}
	return e, nil
}

// decodeEvent decodes a committed event/v1 YAML document into an
// eventProbe.
func decodeEvent(raw []byte) (eventProbe, error) {
	var e eventProbe
	if err := yaml.Unmarshal(raw, &e); err != nil {
		return eventProbe{}, err
	}
	return e, nil
}
