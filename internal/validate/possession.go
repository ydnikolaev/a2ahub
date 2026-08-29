// P4 "possession" (spec docs/features/archive/agent-exchange-2026-08/
// specs/04-possession.md, plan .../plans/04-possession.plan.md): "a
// reference an agent cannot resolve is worse than no reference", so an
// unresolvable one must be impossible to submit rather than merely
// discouraged (the real getvisa incident this rule is built from —
// docs/inbox/agent-exchange-review/03-bytes-note.md — is reconstructed in
// this package's own possession_test.go, not replayed verbatim; the real
// artifact lives in a private space this checkout does not have cloned).
//
// Two rules, RECOMPOSED by ADR-011 after fb-20260812-e6d189 (major): the
// pre-ADR REF-017 refused on a bare `sha256:` mention alone, which fired on
// a contract-set digest named in an ordinary sentence — an artifact merged
// four days before the rule existed, permanently reddening that space's
// post-merge audit with no in-protocol repair. ADR-011's fix is not a
// narrower digest filter (rejected: a contract-set digest and an
// attachment digest are the SAME shape, `sha256:` + hex — no regex tells
// them apart); it is recognizing that a bare digest mention alone was
// never a checkable fact about missing bytes. Only the CONJUNCTION is:
//
//   - REF-017 (reject, AC1): fires only when ALL THREE hold on the same
//     envelope — (1) the body names a `sha256:` token no declared
//     `attachments[].digest` carries, (2) the body enumerates what looks
//     like a file tree, AND (3) the envelope declares no attachment at
//     all. That conjunction is the exact shape of the founding incident (a
//     work_request that named a bundle by digest and listed its eight
//     files while the bytes sat in a gitignored directory) — a receiver
//     reading it has every reason to expect bytes that never arrived. That
//     is the checkable fact; a lone digest is not.
//   - POL-017 (warning): fires for EITHER signal alone, when the
//     conjunction above does not hold. A bare digest mention (undeclared,
//     but no accompanying file-tree-shaped body, or an attachment IS
//     declared elsewhere on the envelope) creates no expectation of
//     bytes — a receiver cannot fetch from a digest it was merely told
//     about, so it is a smell, not a proof. A file-tree enumeration with no
//     attachment declared at all is the same: "this looks like a file
//     tree" is a heuristic, never a proof. Both share one code because both
//     share one consequence (warn, never refuse) — capabilities.go's own
//     contrast ("branches with different consequences get different
//     codes") is why the codes stay separate from REF-017, not a reason to
//     split POL-017 further. When the conjunction DOES hold, POL-017 still
//     fires alongside REF-017 for the same body: the conjunction's own
//     terms include POL-017's file-tree trigger, so both codes appear —
//     REF-017 is the verdict, POL-017 names the heuristic signal that also
//     happens to be true. Promoting POL-017 to a reject on its own is
//     exactly the mutation this package's tests pin against
//     (possession_test.go).
//
// ADR-011 decision 3 (fb-20260812-e6d189, filed after decisions 1/2 above
// had already shipped): REF-017 as composed here still judges the
// FULL-REPO post-merge audit (v3-full-repo) against artifacts committed
// before this rule existed, permanently reddening a space with no
// in-protocol repair — committed history is immutable and no verb
// retracts a closed exchange. The fix is NOT here: this file's
// checkPossession stays mode-agnostic (it has no InvocationPoint in
// scope, by design — see this file's own header). The enforcement site is
// internal/cli/cmd_validate_ci.go's validateCIArtifact, which calls
// Result.SuppressingCode("REF-017") only when mode == "v3-full-repo" —
// v3-pr (the write gate, where refusing still prevents the merge) keeps
// enforcing REF-017 unchanged. POL-017 is deliberately NOT suppressed in
// either mode: it is a warning, so the full-repo audit still surfaces the
// smell without refusing anything immutable.
//
// Deliberately no bytes, no hashing, no internal/datapackage import: per
// the plan's re-anchor (Q2), this is digest-versus-declaration ONLY, over
// the body's own text, which is why it needs nothing beyond what
// runCommonEnvelope already has in scope (fm.Body + the decoded
// instance) — internal/validate's import graph (artifact + schema only)
// is untouched.
//
// # Where the registry rows live, and why not here
//
// `schemas/errors/v1/registry.yaml` and `history.tsv` carry both codes as of
// wave R5. They are the SSOT for the code TEXT; this file is the SSOT for the
// BEHAVIOUR, and ADR-011 is the SSOT for the rule. A copy of the row text used
// to sit here as a draft for the lead to land, and it outlived its purpose the
// moment the rows existed: it went on asserting a single-clause `applies_to`
// the live registry had already replaced with the three-part conjunction and
// the v3-pr/v3-full-repo scope. Read the registry, not a comment about it.
//
// It is worth keeping WHY the rows are mandatory rather than merely tidy:
// `scripts/check-operational-confidence.sh`'s check_history greps this
// package's non-test sources for literal `REF-###`/`POL-###` codes and fails
// the whole ceiling ("live Go source emits unregistered REF-017") until both
// files carry them. `TestRegistryClosure` does not cover it — that test only
// checks codes on paths it itself exercises, and it never calls
// checkPossession.
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
// naming a path does. The extension is captured (group 1): looksLikeFileTree
// additionally requires it to contain at least one LETTER — a real file
// extension always does, but a version-string segment (a prose line
// starting "v0.19.11") is all digits, and without this check that segment
// satisfies this pattern just as well as a real filename. RE2 (this
// package's regexp engine) has no lookahead to express "not all digits"
// inline, so the letter check is a Go-side post-filter on the captured
// group rather than a wider inline pattern — see extensionHasLetter.
var fileTreeLinePattern = regexp.MustCompile(`^\s*[|\-*]?\s*[\w][\w./-]*\.([A-Za-z0-9]{1,8})\b`)

// fileTreeLineThreshold is how many distinct file-tree-shaped lines a
// body needs before POL-017 fires — chosen to match the real incident
// (03-bytes-note.md: "listed its eight files") while staying well clear
// of an artifact that merely names one or two files in passing (refs
// entries, a single "see foo.json" aside).
const fileTreeLineThreshold = 3

// checkPossession is the P4 possession rule, ADR-011's recomposition:
// REF-017 (reject) fires only on the CONJUNCTION of all three —
// (1) a body `sha256:` token no declared attachment carries, (2) the body
// looks like a file-tree enumeration, and (3) the envelope declares no
// attachment at all — the exact shape of the founding incident. Either
// signal alone (an undeclared digest with no accompanying file-tree shape,
// or with an attachment declared elsewhere; or a file-tree enumeration
// with no attachment declared) is not a checkable fact about missing
// bytes, so it downgrades to POL-017 (warning). body is the artifact's raw
// BODY bytes (never frontmatter — internal/artifact.Frontmatter.Body,
// exactly as this package's rail requires: never re-parsed locally).
// instance is the already-decoded frontmatter instance
// (schema.DecodeYAMLInstance's map[string]any/[]any/scalars shape) this
// package's callers already hold; declaredAttachmentDigests reads its
// `attachments[].digest` field, when the envelope's schema carries one.
func checkPossession(body []byte, instance any) []Violation {
	declared := declaredAttachmentDigests(instance)
	noAttachmentsDeclared := len(declared) == 0
	fileTree := looksLikeFileTree(body)

	// The founding-incident shape: an undeclared digest AND a file-tree
	// body AND zero declared attachments, together. Only this conjunction
	// is a checkable fact ("bytes were expected and never arrived");
	// either term alone is a heuristic, never a proof (this file's own
	// doc comment, verbatim).
	conjunction := noAttachmentsDeclared && fileTree

	var out []Violation
	for _, tok := range uniqueDigestTokens(body) {
		if declared[tok] {
			continue
		}
		if conjunction {
			out = append(out, Violation{
				Code:     "REF-017",
				Class:    ClassReferential,
				Message:  fmt.Sprintf("artifact body names %s, which no attachment declared on this envelope carries, alongside what looks like a file-tree enumeration with no attachment declared at all", tok),
				Severity: SeverityReject,
			})
			continue
		}
		out = append(out, Violation{
			Code:     "POL-017",
			Class:    ClassPolicy,
			Message:  fmt.Sprintf("artifact body names %s in prose; a bare digest mention creates no expectation of bytes and cannot itself be fetched from", tok),
			Severity: SeverityWarning,
		})
	}

	// Unchanged from the pre-ADR rule: a file-tree-shaped body with zero
	// declared attachments warns regardless of whether a digest is also
	// present — when the conjunction above holds, this fires alongside
	// REF-017 for the same body (its own trigger, T && A, is exactly the
	// conjunction's other two terms), so both codes appear: REF-017 is the
	// verdict, POL-017 names the heuristic signal that also happens to be
	// true.
	if noAttachmentsDeclared && fileTree {
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
// fileTreeLineThreshold lines matching fileTreeLinePattern AND whose
// captured extension contains a letter (extensionHasLetter) — the AC7
// heuristic, narrowed to reject the false positive a bare digit-only
// "extension" (a version string's last dotted segment, e.g. "v0.19.11")
// would otherwise produce. A heuristic, deliberately: this function
// proves nothing about whether the bytes actually exist anywhere, only
// that the prose READS like an inventory of them.
func looksLikeFileTree(body []byte) bool {
	count := 0
	for _, line := range strings.Split(string(body), "\n") {
		m := fileTreeLinePattern.FindStringSubmatch(line)
		if m == nil || !extensionHasLetter(m[1]) {
			continue
		}
		count++
		if count >= fileTreeLineThreshold {
			return true
		}
	}
	return false
}

// extensionHasLetter reports whether ext (fileTreeLinePattern's captured
// group 1, already constrained to [A-Za-z0-9]{1,8}) contains at least one
// ASCII letter — the fact a real file extension always carries and a
// digits-only version segment never does.
func extensionHasLetter(ext string) bool {
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}
