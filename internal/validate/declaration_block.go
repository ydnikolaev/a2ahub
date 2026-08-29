// P5 "declared nature" (spec docs/features/archive/agent-exchange-2026-08/
// specs/05-declared-nature.md, AC3/US-4): "a maintainer wants an agent's
// hand-rolled `key = value` block in a body to be refused, since it
// proves a field is missing." The live evidence is
// XW-axon-20260801-vet6's own body — a fenced block naming
// `artifact_class`, `compatibility_status`, `adoptable` and
// `runtime_pinnable` beside the sentence "the package declares it in
// machine-readable form", when at the time nothing did.
//
// AC3 is satisfied BY ACCIDENT before this rule exists: P4's REF-017
// digest scan (possession.go) happens to hit the same fixture whenever
// the body also carries a `sha256:` token, and a body with the hand-rolled
// block but NO digest passes clean. POL-018 is the check that actually
// polices the block itself, independent of whether prose happens to also
// name a digest.
//
// The forbidden key set is DERIVED, never listed: schema.EnvelopeFieldPaths
// (version, typ) for the artifact's OWN type, reduced to each path's final
// dotted segment (its leaf field name — e.g. "binding.artifact_class"
// contributes "artifact_class"). A field added to a schema tomorrow is
// policed the day it ships, with no second list to keep in sync — and a
// key that is a real field on some OTHER envelope type, but not this
// artifact's own, does not fire (declaration_block_test.go pins this: a
// contract-only field name inside a work_request body is not forbidden).
//
// Line-anchored exactly as possession.go's fileTreeLinePattern is: matched
// against the START of each line (after an optional bullet/table-pipe
// marker), so prose that merely MENTIONS "x = y" mid-sentence never fires
// — only a line whose whole point is declaring a key does.
//
// # Deviation: POL-018 is not yet in schemas/errors/v1/registry.yaml
//
// schemas/** is lead-reserved for this wave's allowlist (two waves running
// concurrently), so this package cannot append the row itself.
// schemas/errors/v1/history.tsv carries a `reserved` row for POL-018 (the
// same convention REF-015 already uses while its registry row is pending)
// so scripts/check-operational-confidence.sh's check_history does not fail
// on a live/absent mismatch — but that same script's "live Go source
// emits unregistered <code>" check WILL fail until the registry row lands,
// exactly as possession.go's own doc comment recorded for REF-017/POL-017
// before their rows were added. Draft row (see this brief's
// registry_row_needed for the exact text handed to the lead):
//
//	registry.yaml:
//	  - code: POL-018
//	    class: policy
//	    title: Body hand-rolls a `key = value` block naming a field declared on this envelope's own type
//	    applies_to: internal/validate.checkDeclarationBlock — reject; the forbidden key set is derived per call from schema.EnvelopeFieldPaths(version, typ) for the artifact's own type (agent-exchange-2026-08/P5, AC3/US-4)
package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// declarationBlockLinePattern is AC3's line-anchor guard, deliberately the
// same shape as possession.go's fileTreeLinePattern: a line that, once any
// leading bullet/table-pipe marker is stripped, opens with a bare
// identifier, an "=", and a value — the shape of a hand-typed
// `key = value` declaration line. Anchored at the START of the line so
// ordinary prose that merely mentions "x = y" mid-sentence never matches;
// only a line whose whole point is declaring a key does.
var declarationBlockLinePattern = regexp.MustCompile(`^\s*[|\-*]?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*\S`)

// checkDeclarationBlock is POL-018: a body carrying a line-anchored
// `key = value` declaration whose key matches a field name declared on
// THIS artifact's own (version, typ) schema is refused, once per distinct
// offending key. version/typ are the already-parsed values runCommonEnvelope
// holds (schema.ParseVersion(env.Schema), env.Type) — this function reads no
// frontmatter itself, matching possession.go's checkPossession contract of
// taking exactly the body bytes plus what the caller already decoded.
func checkDeclarationBlock(body []byte, version int, typ string) ([]Violation, error) {
	forbidden, err := declaredFieldLeafNames(version, typ)
	if err != nil {
		return nil, fmt.Errorf("validate: checkDeclarationBlock: %w", err)
	}

	var out []Violation
	for _, key := range uniqueDeclarationBlockKeys(body) {
		if !forbidden[key] {
			continue
		}
		out = append(out, Violation{
			Code:     "POL-018",
			Class:    ClassPolicy,
			Message:  fmt.Sprintf("artifact body hand-rolls a `%s = ...` declaration line, but %q is a real field on this envelope's own schema — declare it there, not in prose", key, key),
			Severity: SeverityReject,
		})
	}
	return out, nil
}

// declaredFieldLeafNames derives the forbidden key set from
// schema.EnvelopeFieldPaths(version, typ) — the union of the shared base
// and the per-type schema for THIS artifact's own type, never a hardcoded
// list. Each dotted path is reduced to its final segment (its leaf field
// name): "binding.artifact_class" contributes "artifact_class", a
// top-level path like "id" contributes itself unchanged. A field a body
// author could plausibly hand-type as a bare `key = value` line names its
// leaf, not its full dotted path, which is exactly the shape the live
// evidence used ("artifact_class = ...", not "binding.artifact_class =
// ...").
func declaredFieldLeafNames(version int, typ string) (map[string]bool, error) {
	paths, err := schema.EnvelopeFieldPaths(version, typ)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		leaf := p
		if i := strings.LastIndexByte(leaf, '.'); i >= 0 {
			leaf = leaf[i+1:]
		}
		leaf = strings.TrimSuffix(leaf, "[]")
		if leaf == "" {
			continue
		}
		out[leaf] = true
	}
	return out, nil
}

// uniqueDeclarationBlockKeys returns every distinct key
// declarationBlockLinePattern finds in body, in first-seen order —
// deduplicated so a key repeated across a fenced block produces one
// violation, not one per line.
func uniqueDeclarationBlockKeys(body []byte) []string {
	seen := make(map[string]bool)
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		m := declarationBlockLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}
