package cache

// registered_consumers.go is P4 wave 5's ONE home (decision 8,
// 04-per-version-lifecycle.plan.md) for the D-022 union: every system with
// a `satisfied` requirement whose `target_contract` names a contract, OR a
// `consumes.yaml` entry naming it. internal/cli/cmd_contract.go and
// internal/mcp/tools_contract.go used to carry this verbatim, each its own
// copy — a mirror read, and this package already owns exactly that kind of
// scan (resolver_index.go's own doc comment: "before this, both
// internal/cli/adapters.go and internal/mcp/adapters.go carried their OWN
// copy of this walk").
//
// Edge 1 (04-per-version-lifecycle.md §4): a consumer registered on a
// DIFFERENT major must not block retiring this line forever.
// consumes.yaml's own `major` field (space.Dependency.Major) already
// carries the fact this filter needs — no registry schema change, no
// migration. The satisfied-requirement half of the union carries NO
// version at all (envelope/v1/requirement.schema.json's target_contract is
// a bare "^XC-" id, nothing more), so it is deliberately NOT filtered by
// major: every satisfied requirement counts against every major. A
// consumer whose registration cannot be versioned must never be silently
// dropped from a gate that exists to protect it.

import (
	"fmt"
	"path/filepath"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// requirementProbe is this file's own minimal decode of a requirement's
// frontmatter (ISP idiom — see decode.go's own doc comment): just the
// fields this scan needs.
//
// `To` is here because of a defect both former per-surface copies carried
// and neither noticed. They built the fold envelope from {ID, Kind, From}
// only, and a requirement reaches `satisfied` via
// published --acknowledge(RoleTarget)--> acknowledged --satisfy--> satisfied.
// RoleTarget resolves to `env.To0()`, so with To empty the acknowledge was
// flagged unauthorized and never applied, satisfy from `published` had no
// row, and the requirement STOPPED AT `published` for every real history.
//
// The consequence was not cosmetic: the satisfied-requirement half of the
// D-022 consumer union could never fire, so a consumer who registered by
// filing a requirement never blocked `contract retire`. A gate written to
// fail closed was failing open down one of its two branches, silently,
// because the branch simply never evaluated true. Verified by folding both
// envelope shapes: without To the state ends `published` with two flags;
// with To it ends `satisfied` with none.
type requirementProbe struct {
	ID             string     `yaml:"id"`
	From           string     `yaml:"from"`
	To             envelopeTo `yaml:"to"`
	TargetContract string     `yaml:"target_contract"`
}

// envelopeTo decodes the base envelope's `to`, which the schema allows in
// two shapes (`schemas/envelope/v1/base.schema.json`): a non-empty array
// of system ids, or the scalar `all` for a broadcast. A plain []string
// field fails to decode the scalar form, which would drop the whole probe
// and silently re-create the hole this type exists to close — one shape of
// data disappearing from a gate is exactly the failure being fixed here.
type envelopeTo []string

// UnmarshalYAML accepts both shapes. `all` is carried through as a literal
// single entry rather than expanded: no system is named `all`, so a
// broadcast-addressed requirement still never resolves an acknowledge —
// the same answer as before this fix, reached deliberately instead of by
// a decode failure.
func (t *envelopeTo) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*t = envelopeTo{s}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*t = xs
	return nil
}

// FindRegisteredConsumers returns every registered consumer of contractID,
// UNSCOPED by major — this is contractDeprecateAddressees' own query (WHO a
// deprecation announcement is addressed to; Edge 1 scopes the RETIRE gate
// only, spec 04 §4, so a deprecation's addressee set is unchanged). See
// FindRegisteredConsumersForMajor for the version-scoped variant.
func FindRegisteredConsumers(mirrorDir, contractID string) (map[string]bool, error) {
	return findRegisteredConsumers(mirrorDir, contractID, -1)
}

// FindRegisteredConsumersForMajor is the same D-022 union, scoped to major
// (Edge 1): a consumes.yaml dependency counts only when its own `major`
// equals major; a satisfied requirement counts regardless of major (see
// this file's own doc comment for why).
func FindRegisteredConsumersForMajor(mirrorDir, contractID string, major int) (map[string]bool, error) {
	return findRegisteredConsumers(mirrorDir, contractID, major)
}

func findRegisteredConsumers(mirrorDir, contractID string, major int) (map[string]bool, error) {
	out := map[string]bool{}

	reqMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "requires", "XR-*.md"))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	var matched []requirementProbe
	for _, m := range reqMatches {
		raw, rerr := readBounded(m, maxCacheReadBytes)
		if rerr != nil {
			return nil, fmt.Errorf("cache: %w", rerr)
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe requirementProbe
		if yaml.Unmarshal(fm.YAML, &probe) != nil || probe.TargetContract != contractID {
			continue
		}
		matched = append(matched, probe)
	}
	if len(matched) > 0 {
		// Determine each matching requirement's OWN folded state — this
		// read-only resolution is membership-agnostic (Fold's zero-events
		// fallback / table lookup does not need it; only authorization
		// checks would), so every requirement's actor resolves as a member
		// regardless of the real manifest, same as both former per-surface
		// copies.
		events, _, werr := walkEvents(mirrorDir)
		if werr != nil {
			return nil, fmt.Errorf("cache: %w", werr)
		}
		eventsBySubject := map[string][]fold.Event{}
		for _, re := range events {
			eventsBySubject[re.Ev.Subject] = append(eventsBySubject[re.Ev.Subject], fold.Event{
				ULID: re.Ev.Event, Subject: re.Ev.Subject, Transition: re.Ev.Transition,
				ClaimedState: fold.State(re.Ev.State),
				Actor:        fold.Actor{Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System},
				Version:      canonicalEventVersion(re.Ev.Version),
			})
		}
		for _, probe := range matched {
			var state fold.State
			evs := eventsBySubject[probe.ID]
			if len(evs) == 0 {
				state = fold.NewResult(fold.KindRequirement).State
			} else {
				// To is threaded through — see requirementProbe's own doc
				// comment for what omitting it silently cost.
				state = fold.Fold(fold.KindRequirement, fold.Envelope{ID: probe.ID, Kind: fold.KindRequirement, From: probe.From, To: probe.To}, evs, func(string) fold.MembershipStatus { return fold.MembershipMember }).State
			}
			if state == fold.StateSatisfied {
				out[probe.From] = true
			}
		}
	}

	consumesMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "consumes.yaml"))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	for _, m := range consumesMatches {
		raw, rerr := readBounded(m, maxCacheReadBytes)
		if rerr != nil {
			return nil, fmt.Errorf("cache: %w", rerr)
		}
		registry, cerr := parseConsumesStrict(raw, m)
		if cerr != nil {
			// FAIL CLOSED. This function's output is the retire
			// precondition's consumer list: "I could not read this
			// registry" must never round down to "this system consumes
			// nothing", or a contract gets retired out from under a system
			// that is subscribed to it.
			return nil, cerr
		}
		for _, d := range registry.Dependencies {
			if d.Contract != contractID {
				continue
			}
			if major >= 0 && d.Major != major {
				continue
			}
			out[registry.System] = true
		}
	}
	return out, nil
}

// parseConsumesStrict parses a committed consumes.yaml and REFUSES anything
// that is not a real consumes/v1 registry — a placeholder `consumes: []`
// file unmarshals cleanly into a zero-valued struct, indistinguishable from
// a system that genuinely consumes nothing, unless schema/system are
// checked explicitly.
func parseConsumesStrict(raw []byte, path string) (space.Consumes, error) {
	registry, err := space.ParseConsumes(raw)
	if err != nil {
		return space.Consumes{}, fmt.Errorf("cache: %s is not valid yaml: %w", path, err)
	}
	if registry.Schema != "consumes/v1" || registry.System == "" {
		return space.Consumes{}, fmt.Errorf(
			"cache: %s is not a consumes/v1 registry (needs `schema: consumes/v1`, `system: <id>`, `dependencies: [...]`) — "+
				"refusing to treat it as \"no registered consumers\"; fix the file (or write it with `a2a contract adopt`)", path)
	}
	return registry, nil
}
