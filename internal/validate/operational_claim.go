package validate

import (
	"fmt"
	"sort"
	"strings"
)

// checkOperationalClaim is spec 08 8b (defects-fix-2026-08, "a contract
// tells its operational truth") — docs/inbox/defects/09-three-contracts-
// one-word.md's own worked example, XC-seomatrix-c4-export: a contract may
// declare `x_binding.runtime_pinnable: true` (schemas/envelope/v2/
// contract.schema.json) — an invitation for a consumer to pin a runtime
// dependency on it — while the SAME document's `x_operational[]` declares
// the very endpoint that invitation depends on as `state: absent`. Both
// facts are legitimate, machine-readable, and correct in isolation; nothing
// in this package asked them the same question until now.
//
// This is [defects/02](../../docs/inbox/defects/02-narrow-answers-survey.md)'s
// class exactly, and its shape follows ADR-011's own two-decision pattern
// (docs/decisions.md, "A rule refuses on the conjunction ... each half alone
// warns" — REF-017/possession.go's precedent, reused here for the SHAPE, not
// the topic): a rule fires on the CONJUNCTION of two checkable facts, never
// on either alone. Here the conjunction only WARNS, never refuses —
// ADR-011 D2's own reasoning about REF-017 applies with the same force to
// this pair: publishing a capability promise before its operational half
// exists is a designed, legitimate protocol sequence (spec 05
// "declared nature" — `a2a contract activate` is what clears
// `x_operational` once the endpoint is live), so the conjunction is a claim
// AHEAD of the facts, not an invalid document. Refusing it would refuse the
// exact publish-then-activate order the protocol asks producers to use.
//
// Each half alone is silent:
//   - `runtime_pinnable: true` with no `x_operational` declared at all, or
//     every declared item `ready`: no claim ahead of any fact, no violation.
//   - `x_operational` declaring an absent item with no `x_binding` at all,
//     or `runtime_pinnable` false/undeclared: nobody is being invited to
//     pin anything, so there is nothing to warn about.
//
// Reads instance directly (the same map[string]any/[]any/scalar shape
// checkPossession and declaredAttachmentDigests already decode this
// document's frontmatter into, engine.go's runCommonEnvelope) rather than a
// second parse — this package's own "one decode, ask it different
// questions" idiom, not internal/cache's YAML-tag probes (mirror.go's own
// xBindingProbe/operationalArrayProbe), which read a DIFFERENT
// representation (raw YAML bytes, for the read model) for a different
// consumer (the dashboard, not the validation gate).
func checkOperationalClaim(instance any) []Violation {
	root, ok := instance.(map[string]any)
	if !ok {
		return nil
	}
	if !runtimePinnableDeclaredTrue(root) {
		return nil
	}
	absentNames := declaredAbsentOperationalNames(root)
	if len(absentNames) == 0 {
		return nil
	}
	sort.Strings(absentNames)
	return []Violation{{
		Code:  "POL-023",
		Class: ClassPolicy,
		Path:  "x_operational",
		Message: fmt.Sprintf(
			"x_binding.runtime_pinnable is declared true while x_operational declares %s absent; a consumer pinning now depends on a capability this document says does not exist yet — the producer clears this by running `a2a contract activate` once it does",
			strings.Join(absentNames, ", "),
		),
		Severity: SeverityWarning,
	}}
}

// runtimePinnableDeclaredTrue reads x_binding.runtime_pinnable off the
// decoded instance the same defensive way declaredAttachmentDigests
// (possession.go) reads attachments[]: a missing or wrongly-shaped field
// degrades to false/absent rather than erroring — schema validation
// (SCH-###, runCommonEnvelope's own corpus.ValidateEnvelope call, which
// always runs before this check in engine.go) is what refuses a malformed
// x_binding; this check only ever asks a document schema already accepted.
func runtimePinnableDeclaredTrue(root map[string]any) bool {
	xBinding, ok := root["x_binding"].(map[string]any)
	if !ok {
		return false
	}
	pinnable, ok := xBinding["runtime_pinnable"].(bool)
	return ok && pinnable
}

// declaredAbsentOperationalNames returns every x_operational[] entry's own
// `name` whose own `state` is the wire literal "absent" (schemas/envelope/
// v2/contract.schema.json's own enum; never the derived "undeclared" a
// reader-side projection like cache.DeriveOperationalItems computes — this
// check only ever sees what the document itself wrote), in first-declared
// order before the caller sorts for a deterministic message.
func declaredAbsentOperationalNames(root map[string]any) []string {
	list, ok := root["x_operational"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, entry := range list {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		state, _ := item["state"].(string)
		if name == "" || state != "absent" {
			continue
		}
		names = append(names, name)
	}
	return names
}
