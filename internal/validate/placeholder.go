// A2 half of the placeholder defect filed 2026-07-27 (verified on the
// released 0.10.0 binary): `a2a new question` then `a2a validate` returned
// ZERO violations with question.md's own `expected_response.shape: "<what
// a good answer looks like>"` placeholder still in place, and `a2a submit`
// accepted it — template prose landing in the field whose whole job is to
// tell a responder what a good answer looks like.
//
// This is deliberately V2-only (see checkUnfilledPlaceholders's own doc
// comment): AC-401.1 ("V1 passes on placeholder-only fills") is a shipped
// acceptance criterion, and this refusal belongs at the boundary where a
// draft becomes a shared record, not at the authoring point where every
// OTHER field is still allowed to carry its literal schema-valid default.
package validate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// placeholderPattern matches a frontmatter scalar that is STILL, verbatim,
// a template's own placeholder token: the whole value opens with `<` and
// closes with `>`, with nothing outside that span. Fully anchored — the
// anchors are the entire guard, and they are enough.
//
// # Why not "no inner angle bracket"
//
// The first version of this check required a single un-nested span
// (`^<[^<>]*>$`) on the theory that inner brackets meant real content.
// The propagation probe found what that reasoning missed: EVERY one of the
// eight canonical templates carries
//
//	title: <human/agent-scannable title, <=120 chars>
//
// whose inner `<` comes from "<=120" — so the narrow pattern silently let
// the single most visible field of every artifact type merge unfilled,
// which is the exact defect this check exists to refuse. Same for a `refs`
// entry's `"<id>#<digest>"`: two bracketed spans in one string.
//
// The wider pattern costs nothing on the case that motivated the narrowing.
// internal/e2e/testdata/sanitization/script-tag.md's
// `title: "<script>alert(1)</script> raw <b>HTML</b> && \"quotes\""` still
// does not trip, because it does not END with `>` — the trailing anchor is
// what excludes it, not the nesting clause. Both are covered by tests
// below, and by the corpus-wide test that reads the shipped templates
// themselves rather than a hand-listed sample.
//
// Accepted cost, stated rather than hidden: a value that genuinely IS
// exactly `<something>` is refused and must be rephrased. Across the eight
// envelope types no field has such a legitimate value — an id, a version,
// a system name, a date and a closed enum never look like this, and a title
// or a free-text field can say the same thing without the outer brackets.
var placeholderPattern = regexp.MustCompile(`^<.*>$`)

// checkUnfilledPlaceholders is the V2-only refusal (POL-010) for a
// frontmatter scalar left as the template's own placeholder token at
// submit time. It walks instance — the already-decoded frontmatter value
// from schema.DecodeYAMLInstance(fm.YAML), map[string]any / []any /
// scalars — RECURSIVELY, because a placeholder can be nested inside a
// mapping (`expected_response.shape`) or inside a sequence element
// (`refs[].ref`), never just the top-level fields applyFills itself walks
// (internal/template's A1 fix).
//
// instance is frontmatter ONLY: schema.DecodeYAMLInstance never sees an
// artifact's body, so this check can never reach into free body prose,
// which legitimately contains angle brackets and is out of scope by
// construction, not by a special case here.
//
// Why V2 only, never V1 (ValidateDraft): AC-401.1 ("V1 passes on
// placeholder-only fills") is a shipped acceptance criterion — `a2a new`
// then `a2a validate` must stay clean so a drafted-but-not-yet-filled
// artifact is still inspectable. The refusal belongs at the boundary
// where a draft becomes a shared record (`a2a submit`), not at the
// authoring point.
func checkUnfilledPlaceholders(instance any) []Violation {
	var out []Violation
	walkPlaceholders(instance, "", &out)
	return out
}

func walkPlaceholders(node any, path string, out *[]Violation) {
	switch v := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic emission order
		for _, k := range keys {
			walkPlaceholders(v[k], joinPath(path, k), out)
		}
	case []any:
		for i, e := range v {
			walkPlaceholders(e, joinPath(path, strconv.Itoa(i)), out)
		}
	case string:
		if path != "" && placeholderPattern.MatchString(v) {
			*out = append(*out, Violation{
				Code:  "POL-010",
				Class: ClassPolicy,
				Path:  path,
				Message: fmt.Sprintf("%s is still the template's own placeholder value %q; %s",
					path, v, placeholderRemedy(path)),
				Severity: SeverityReject,
			})
		}
	}
}

// placeholderRemedy names the action that actually clears the violation, in
// the caller's own vocabulary. It is written for the weakest reader in the
// audience: an automated caller that will do literally what the sentence says
// and has no way to guess what it left out. So the sentence has to be true for
// the specific path, which takes three cases rather than one.
//
// The first draft of this had two bugs, both found by reading the real emitted
// messages for all eight types rather than by reasoning:
//
//   - It told the caller to hand-edit ANY path containing a list index. But
//     `--field to=seomatrix` works: internal/template's setField accepts a bare
//     scalar for a sequence field as the one-element list. Three of the eight
//     types' most common refusals (`to.0`, `acceptance_criteria.0`,
//     `fulfills.0`) were being sent to a text editor for a job a flag does.
//     The real boundary is narrower: a flag can reach a list whose PLACEHOLDER
//     IS THE WHOLE LIST, because the dotted pass descends mappings to the
//     sequence and setField replaces it. It cannot reach INTO an element
//     (`refs.0.ref`, `deliverables.0.name`), where the index sits mid-path and
//     resolveDottedPath stops at the sequence by design.
//   - For the list case it must also say what the flag DOES, since replacing a
//     list is not the same as editing one entry. In the shipped templates the
//     list holds exactly the one placeholder, so it is the right action — but
//     the caller should not learn that the hard way on a longer list.
//
// On the "or delete the line" half: an OPTIONAL field left at its placeholder
// is cleared by DELETING it, not by inventing a value. Three shipped templates
// carry one (`needed_by`, `valid_until`, `env_requirements`), and they reach
// here because internal/schema deliberately does not assert `format`
// (corpus.go) — so `needed_by: <YYYY-MM-DD>` is schema-valid, and an author
// told only to "fill it" would invent a date the requirement does not have.
// This function does NOT know which fields are optional, and deliberately does
// not learn: the wrong branch is already caught. Delete a REQUIRED field and
// the same V2 run's schema-class check refuses it by name in the same breath —
// a bounded, immediate, self-correcting mistake. Teaching this function the
// per-type `required` sets would duplicate the schema corpus to improve a
// sentence whose wrong reading costs one more refusal.
func placeholderRemedy(path string) string {
	segments := strings.Split(path, ".")
	lastIsIndex := false
	indexInside := false
	for i, seg := range segments {
		if _, err := strconv.Atoi(seg); err != nil {
			continue
		}
		if i == len(segments)-1 {
			lastIsIndex = true
			continue
		}
		indexInside = true
	}

	switch {
	case indexInside:
		return fmt.Sprintf("edit %s in the drafted file before submitting, or remove that entry if it is not needed"+
			" (--field cannot reach INTO a list element — its dotted paths descend mappings, and stop at the list)", path)
	case lastIsIndex:
		list := strings.Join(segments[:len(segments)-1], ".")
		return fmt.Sprintf("fill it with --field %s=<value> before submitting, which sets `%s` to that single entry"+
			" — or edit the drafted file directly if the list needs more than one", list, list)
	default:
		return fmt.Sprintf("fill it with --field %s=<value> before submitting, or delete the line"+
			" if it is an optional field you do not need (do not invent a value for it)", path)
	}
}

func joinPath(base, seg string) string {
	if base == "" {
		return seg
	}
	return base + "." + seg
}
