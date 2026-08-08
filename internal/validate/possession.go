// P4 "possession" (spec docs/features/active/agent-exchange-2026-08/
// specs/04-possession.md, plan .../plans/04-possession.plan.md): "a
// reference an agent cannot resolve is worse than no reference", so an
// unresolvable one must be impossible to submit rather than merely
// discouraged (the real getvisa incident this rule is built from —
// docs/inbox/agent-exchange-review/03-bytes-note.md — is reconstructed in
// this package's own possession_test.go, not replayed verbatim; the real
// artifact lives in a private space this checkout does not have cloned).
//
// Two rules, deliberately asymmetric in strength:
//
//   - REF-017 (reject, AC1): the artifact BODY's own text is scanned for
//     `sha256:` tokens — the universe is DERIVED from what an author
//     actually wrote, never a declared list, which would drift the moment
//     prose and declaration disagree (the plan's §The gate says this in
//     as many words). Every token found must be carried by a declared
//     `attachments[].digest` on the SAME envelope's already-decoded
//     instance; one that isn't is a checkable fact — the digest is either
//     carried or it is not — so it refuses.
//   - POL-017 (warning, AC7): a body that merely LOOKS like it enumerates
//     a file tree, while the envelope declares no attachment at all, is a
//     smell worth naming but not a checkable fact ("this looks like a
//     file tree" is a heuristic, never a proof) — so it warns, never
//     refuses. Promoting it to a reject is exactly the mutation this
//     package's tests pin against (possession_test.go).
//
// Deliberately no bytes, no hashing, no internal/datapackage import: per
// the plan's re-anchor (Q2), this is digest-versus-declaration ONLY, over
// the body's own text, which is why it needs nothing beyond what
// runCommonEnvelope already has in scope (fm.Body + the decoded
// instance) — internal/validate's import graph (artifact + schema only)
// is untouched.
//
// # Deviation: REF-017 / POL-017 are not yet in schemas/errors/v1/
// # registry.yaml
//
// schemas/** is lead-reserved for this wave's allowlist, so this package
// cannot append the two rows itself. Both codes follow the registry's own
// numbering (REF-014/016 live, REF-015 reserved-elsewhere; POL-016 live,
// POL-011 tombstoned) and its class convention (REF-017 alongside
// REF-003/004's "does not resolve" family; POL-017 alongside POL-001/010's
// content-pattern-scan family). TestRegistryClosure
// (internal/validate/registry_test.go) stays green because it only checks
// codes on paths it itself exercises and never calls checkPossession —
// but scripts/check-operational-confidence.sh's check_history greps
// internal/validate/*.go (non-test) for literal "REF-###"/"POL-###" codes
// and fails the whole ceiling ("live Go source emits unregistered
// REF-017") until the registry and schemas/errors/v1/history.tsv both
// carry these two rows. That edit is schemas/**, out of this wave's
// allowlist — flagged for the lead to land in the same wave, not deferred
// as a "when convenient" follow-up. Draft rows:
//
//	registry.yaml:
//	  - code: REF-017
//	    class: referential
//	    title: Body names a digest no declared attachment carries
//	    applies_to: envelope body prose `sha256:` tokens versus attachments[].digest (§7, AC1)
//	  - code: POL-017
//	    class: policy
//	    title: Body enumerates what looks like a file tree while no attachment is declared
//	    applies_to: internal/validate.checkPossession — heuristic warning only (§7, AC7)
//
//	history.tsv (tab-separated):
//	  REF-017	live	agent-exchange-2026-08/P4	body names a digest no declared attachment carries	<commit>	-
//	  POL-017	live	agent-exchange-2026-08/P4	body enumerates a file tree while no attachment is declared	<commit>	-
package validate

import (
	"fmt"
	"regexp"
	"strings"
)

// digestTokenPattern matches a `sha256:` digest token anywhere in an
// artifact's BODY. Hex digits only, one or more — deliberately unanchored
// on length: a real digest is 64 hex chars, but a truncated quote (the
// real incident's own "d9ad8acd…675245", ellipsis and all) must still be
// caught as unresolved, because a partial digest resolves to nothing
// either.
var digestTokenPattern = regexp.MustCompile(`sha256:[0-9a-fA-F]+`)

// fileTreeLinePattern is AC7's heuristic: a line that, once any leading
// bullet/table-pipe marker is stripped, opens with a token containing a
// dotted extension — the shape of a bare filename or a markdown table
// cell naming one. Anchored at the START of the line (after the optional
// marker) deliberately, so ordinary prose that merely MENTIONS a
// filename mid-sentence does not count; only a line whose whole point is
// naming a path does.
var fileTreeLinePattern = regexp.MustCompile(`^\s*[|\-*]?\s*[\w][\w./-]*\.[A-Za-z0-9]{1,8}\b`)

// fileTreeLineThreshold is how many distinct file-tree-shaped lines a
// body needs before POL-017 fires — chosen to match the real incident
// (03-bytes-note.md: "listed its eight files") while staying well clear
// of an artifact that merely names one or two files in passing (refs
// entries, a single "see foo.json" aside).
const fileTreeLineThreshold = 3

// checkPossession is the P4 possession rule: REF-017 (reject) for every
// body `sha256:` token no declared attachment carries, plus POL-017
// (warning) when the body looks like a file-tree enumeration and the
// envelope declares no attachment at all. body is the artifact's raw
// BODY bytes (never frontmatter — internal/artifact.Frontmatter.Body,
// exactly as this package's rail requires: never re-parsed locally).
// instance is the already-decoded frontmatter instance
// (schema.DecodeYAMLInstance's map[string]any/[]any/scalars shape) this
// package's callers already hold; declaredAttachmentDigests reads its
// `attachments[].digest` field, when the envelope's schema carries one.
func checkPossession(body []byte, instance any) []Violation {
	declared := declaredAttachmentDigests(instance)

	var out []Violation
	for _, tok := range uniqueDigestTokens(body) {
		if declared[tok] {
			continue
		}
		out = append(out, Violation{
			Code:     "REF-017",
			Class:    ClassReferential,
			Message:  fmt.Sprintf("artifact body names %s, which no attachment declared on this envelope carries", tok),
			Severity: SeverityReject,
		})
	}

	if len(declared) == 0 && looksLikeFileTree(body) {
		out = append(out, Violation{
			Code:     "POL-017",
			Class:    ClassPolicy,
			Message:  "artifact body enumerates what looks like a file tree, but the envelope declares no attachment",
			Severity: SeverityWarning,
		})
	}

	return out
}

// declaredAttachmentDigests reads instance's top-level `attachments[]`
// field (schemas/envelope/v2/work_request.schema.json's shape — §7:
// `attachments[].digest`, required, `sha256:`-prefixed) and returns the
// set of digest strings it declares. Every failed type assertion
// (instance not a map, no `attachments` key, an entry with no string
// `digest`) degrades to "not declared" rather than erroring — the schema
// class has already run by the time this executes (runCommonEnvelope
// calls it only on the ok=true path), so a shape this function cannot
// read is either a type that carries no attachments field at all (every
// envelope type except work_request/v2, today) or already flagged by a
// schema-class violation elsewhere in the same result.
func declaredAttachmentDigests(instance any) map[string]bool {
	out := map[string]bool{}
	m, ok := instance.(map[string]any)
	if !ok {
		return out
	}
	atts, ok := m["attachments"].([]any)
	if !ok {
		return out
	}
	for _, a := range atts {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		d, ok := am["digest"].(string)
		if !ok || d == "" {
			continue
		}
		out[d] = true
	}
	return out
}

// uniqueDigestTokens returns every distinct `sha256:` token
// digestTokenPattern finds in body, in first-seen order — deduplicated so
// a digest quoted twice in prose produces one REF-017, not two.
func uniqueDigestTokens(body []byte) []string {
	matches := digestTokenPattern.FindAllString(string(body), -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// looksLikeFileTree reports whether body contains at least
// fileTreeLineThreshold lines matching fileTreeLinePattern — the AC7
// heuristic. A heuristic, deliberately: this function proves nothing
// about whether the bytes actually exist anywhere, only that the prose
// READS like an inventory of them.
func looksLikeFileTree(body []byte) bool {
	count := 0
	for _, line := range strings.Split(string(body), "\n") {
		if fileTreeLinePattern.MatchString(line) {
			count++
			if count >= fileTreeLineThreshold {
				return true
			}
		}
	}
	return false
}
