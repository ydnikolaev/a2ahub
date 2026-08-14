package html

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestTemplateRendersDeclaredFields is the C7 gate: "a field is not shipped
// until something decodes it" — this repo's own name for the failure mode
// wave 36's scout found six times (agent-exchange-2026-08, F1-F6): a field
// the Go model types carry that the SHIPPED page never reads. Three
// existing reflection gates in this package (item_projection_test.go,
// openitem_projection_test.go) and internal/cache's own
// item_completeness_test.go all stop at the Go type boundary — cache type
// -> html type. None of them ask whether the html type's own JSON tags
// reach the RENDERED page, which is exactly why a field can cross every one
// of those gates and still never appear on screen (F1's OperationalItems is
// the proof: it is carried end to end through two Go projections and named
// in a prop-object literal, and still never painted).
//
// WHAT THIS PROVES, AND WHAT IT DOES NOT (read this before trusting a green
// result here):
//
//   - It reflects over the exported JSON-tagged fields of four model types
//     (ArtifactDetail, ArtifactDetailEvent, ThreadOpenItem, Contract) plus
//     ONE level into any field whose element type is itself a struct
//     (a slice-of-struct or pointer-to-struct — datapackage.AttachmentClaim
//     off Attachments, cache.OperationalItem off OperationalItems, etc.),
//     and requires the JSON tag string to appear in the SHIPPED, embedded
//     internal/html/template.html (placeholderTemplate,
//     render.go) — the generated artifact npm run build:dashboard produces,
//     never the web/design-source it comes from. That is what the lead
//     grepped by hand to confirm F1/F5/F6 against, so it is the only source
//     this gate is allowed to trust too.
//
//   - A bare substring match proves a key is NAMED somewhere in a
//     5192-line file, NOT that it is PAINTED — a common word can appear for
//     an entirely unrelated field (this file's own "why", reused by at
//     least two other features, and "digest", reused by the unrelated
//     carried-set file listing) and a match there is a FALSE PASS this gate
//     cannot see through. To cut that risk for the fields this scan can
//     reach directly (depth 0: the four types' own fields), a field must
//     appear on at least TWO lines that are not themselves a whole-line
//     `//` comment — one occurrence is exactly what F1's own
//     `operationalItems: a.operational_items || []` is: named once, in a
//     prop object nothing ever reads again, with its own second, comment-
//     only mention beside it. A genuinely wired field appears at least
//     twice: once where it is threaded into a prop/variable, once where
//     that prop is actually read by a render function. This is what turns
//     F1 red without this test having to parse the client's own template
//     grammar.
//
//   - The one-level-deep nested fields (an attachment's own `digest`, an
//     operational item's own `name`/`state`) get only the weak, ONE-
//     occurrence-anywhere check, because "two occurrences of a nested tag"
//     is not a meaningful signal once several unrelated types share short
//     field names — this scan cannot tell WHICH parent object a bare
//     `.digest` belongs to from text alone. That weakness is exactly why F6
//     (attachment `digest`) is caught structurally instead: Attachments
//     itself is a depth-0 field on ArtifactDetail, gets the strong 2+
//     check, and — like OperationalItems — is named in exactly one
//     prop-object line (`attachments: a.attachments || []`) and never
//     painted per-row, so the STRONG check on the parent slice is what
//     reds F6, not a (weak, and here also false-positive-prone via the
//     unrelated Files listing's own `f.digest`) nested check on `digest`
//     itself.
//
//   - The comment-vs-code line split is intentionally CONSERVATIVE in one
//     direction only: a line counts as a comment when its trimmed content
//     starts with `//`; a trailing `// note` at the end of a code line
//     still counts as code. That under-strips rather than over-strips —
//     it can only make the two-occurrence bar EASIER to clear, never mask
//     a field that is truly never read. The direction of the imprecision
//     is the one that cannot hide a real F1/F5/F6-shaped gap.
//
// A green result here means: every field named is at least named twice
// outside a comment (or, for a one-level-nested field, named once
// anywhere). It does NOT mean the field is rendered correctly, is visible
// without a feature flag, or reaches every surface the spec promises —
// only that this specific silent-drop failure mode did not repeat.
func TestTemplateRendersDeclaredFields(t *testing.T) {
	t.Parallel()

	raw := dashboardTemplateCorpus(t)
	if len(raw) < 1000 {
		t.Fatal("placeholderTemplate (template.html via go:embed) is suspiciously small — the embed did not pick up the shipped file")
	}

	for _, ct := range checkedTemplateTypes {
		t.Run(ct.name, func(t *testing.T) {
			checks := templateFieldChecks(ct.typ, ct.name)
			if len(checks) == 0 {
				t.Fatalf("reflection found no JSON-tagged fields on %s — the check itself is broken, not the template", ct.name)
			}
			excused := templateExcusedFields[ct.name]
			var missing []string
			for _, fc := range checks {
				if reason, ok := excused[fc.label]; ok {
					t.Logf("%s (tag %q) excused: %s", fc.label, fc.tag, reason)
					continue
				}
				comment, code := countOccurrences(raw, fc.tag)
				var painted bool
				if fc.strong {
					painted = code >= 2
				} else {
					painted = comment+code >= 1
				}
				if !painted {
					missing = append(missing, fc.label+" (json tag "+fc.tag+")")
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Fatalf("%d field(s) of %s never reach the shipped template.html (or, for a depth-0 field, are named only once outside a comment): %s\n"+
					"Either paint it in web/design-source and regenerate template.html, or add it to templateExcusedFields with the reason it legitimately never reaches the rendered page.",
					len(missing), ct.name, strings.Join(missing, "; "))
			}
		})
	}
}

// TestTemplateCarriesEveryFoldTransitionLiteral is F5's own gate: the
// shipped template's TRANSITION_PAST/EVENT_SIGNAL comment
// (internal/html/template.html, generated from
// web/design-source/14-local-dashboard-v4.dc.html) claims "Both maps carry
// every key the fold engine can emit (internal/fold/table.go)". Nothing
// enforced that sentence — it is FALSE today: fold.TActivate ("activate")
// has zero occurrences anywhere in the shipped file, so an `activate` event
// falls back to the raw English literal even in Russian mode, framed as a
// neutral "Informational event" (EVENT_SIGNAL's own default tone) instead
// of the owner-only, pendency-discharging attestation it actually is.
//
// This checks presence only (existence, not the strong/weak split above):
// a transition literal is a short, mostly-distinctive token appearing as an
// object-key pattern (`name:` bare, or `"name":` for a hyphenated literal
// like verify-pass), which is a narrower shape than a bare substring search
// and was enough to hand-confirm zero hits for "activate" against the
// shipped file directly.
func TestTemplateCarriesEveryFoldTransitionLiteral(t *testing.T) {
	t.Parallel()

	raw := dashboardTemplateCorpus(t)
	transitions := map[string]string{
		fold.TCreate:      "TCreate",
		fold.TPublish:     "TPublish",
		fold.TDeprecate:   "TDeprecate",
		fold.TRetire:      "TRetire",
		fold.TAcknowledge: "TAcknowledge",
		fold.TSatisfy:     "TSatisfy",
		fold.TDecline:     "TDecline",
		fold.TWithdraw:    "TWithdraw",
		fold.TSupersede:   "TSupersede",
		fold.TSubmit:      "TSubmit",
		fold.TAccept:      "TAccept",
		fold.TStart:       "TStart",
		fold.TBlock:       "TBlock",
		fold.TUnblock:     "TUnblock",
		fold.TRespond:     "TRespond",
		fold.TClose:       "TClose",
		fold.TDispute:     "TDispute",
		fold.TCancel:      "TCancel",
		fold.TPropose:     "TPropose",
		fold.TApprove:     "TApprove",
		fold.TReject:      "TReject",
		fold.TVerifyPass:  "TVerifyPass",
		fold.TVerifyFail:  "TVerifyFail",
		fold.TVerify:      "TVerify",
		fold.TNote:        "TNote",
		fold.TActivate:    "TActivate",
	}

	var missing []string
	for literal, constName := range transitions {
		if reason, ok := transitionExcusedLiterals[literal]; ok {
			t.Logf("fold.%s (%q) excused: %s", constName, literal, reason)
			continue
		}
		bareKey := literal + ":"
		quotedKey := `"` + literal + `":`
		if !strings.Contains(raw, bareKey) && !strings.Contains(raw, quotedKey) {
			missing = append(missing, literal+" (fold."+constName+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d fold transition literal(s) never appear as a display-map key in the shipped template.html, even though TRANSITION_PAST's own comment claims every emittable key is carried: %s\n"+
			"Either add a display label in web/design-source's TRANSITION_PAST/EVENT_SIGNAL and regenerate, or add it to transitionExcusedLiterals with the reason it is legitimately never displayed.",
			len(missing), strings.Join(missing, ", "))
	}
}

// templateExcusedFields excuses a field this gate would otherwise flag,
// grouped by the type's own name and keyed by this test's own label
// (Type.Field or Type.Field[].nestedField) — the same "name it, name why"
// shape item_projection_test.go's itemFieldsThisPackageDeliberatelyDrops
// already uses.
//
// The first RED run of this gate (wave 36 phase A, agent-exchange-2026-08)
// found 26 unreached fields across the four types — nine of them ARE named
// findings (F1: ArtifactDetail.OperationalItems, ThreadOpenItem.
// OperationalItems; F3: ThreadOpenItem.ExpectedTransition, .HumanGate; F6:
// ArtifactDetail.Attachments), and stay UNEXCUSED here on purpose — the
// acceptance criterion for this wave is that this gate is RED for them, and
// an excuse map entry would silently turn that red green. F5 (fold.
// TActivate) is a separate test (TestTemplateCarriesEveryFoldTransitionLiteral)
// and carries no excuse for the same reason.
//
// The remaining 17 are genuine residue: real, currently-unpainted fields
// this wave never claimed to have audited (Category, the actor-identity
// quintet on ArtifactDetailEvent, the Refs digest pair, the rest of
// Attachment's own metadata beyond `digest`, State{By,Event,Since} and
// NextActions on ThreadOpenItem). Per this wave's own brief ("if the first
// red list exceeds ~15 fields, do not write 40 excuses ... excuse the
// residue with ONE grouped entry per type"), every one of them is excused
// with the SAME reason text per type, not 17 individually-argued
// justifications: this wave never looked at them, so there is nothing to
// argue beyond "out of scope, not yet audited" — the size of that residue
// (17 fields) is itself reported to the lead (gate_field_list), not
// papered over here.
const templateResidueReason = "wave 36 phase A (agent-exchange-2026-08) scoped this gate to F1/F3/F5/F6; this field was never audited against the rendered page and is neither confirmed painted nor a named finding — residue for a future pass, not a decision."

const dashboardCoherenceW2CarryReason = "dashboard-coherence-2026-08 P7 carries this typed technical fact before the P9 item/thread family renders it; P16 removes this migration entry when it performs the final view-model consumption freeze."

var templateExcusedFields = map[string]map[string]string{
	"ArtifactDetail": {
		"ArtifactDetail.Attachments[].ConformsTo":        templateResidueReason,
		"ArtifactDetail.Attachments[].ExpiresAt":         templateResidueReason,
		"ArtifactDetail.Attachments[].LapseClaim":        templateResidueReason,
		"ArtifactDetail.Attachments[].LapsedOn":          templateResidueReason,
		"ArtifactDetail.Attachments[].Retention":         templateResidueReason,
		"ArtifactDetail.Attachments[].VerificationClaim": templateResidueReason,
		"ArtifactDetail.Events[].ActorSession":           templateResidueReason,
		"ArtifactDetail.Events[].ProducedBy":             templateResidueReason,
		"ArtifactDetail.Refs[].PinnedDigest":             templateResidueReason,
		"ArtifactDetail.Refs[].ResolvedDigest":           templateResidueReason,
	},
	"ArtifactDetailEvent": {
		"ArtifactDetailEvent.ActorKind":    templateResidueReason,
		"ArtifactDetailEvent.ActorModel":   templateResidueReason,
		"ArtifactDetailEvent.ActorSession": templateResidueReason,
		"ArtifactDetailEvent.ActorSystem":  templateResidueReason,
		"ArtifactDetailEvent.ClaimedState": templateResidueReason,
		"ArtifactDetailEvent.ProducedBy":   templateResidueReason,
	},
	"ThreadOpenItem": {
		"ThreadOpenItem.NextActions":  templateResidueReason,
		"ThreadOpenItem.RuleIdentity": dashboardCoherenceW2CarryReason,
		"ThreadOpenItem.StateBy":      templateResidueReason,
		"ThreadOpenItem.StateEvent":   templateResidueReason,
		"ThreadOpenItem.StateSince":   templateResidueReason,
	},
	"Contract": {
		"Contract.Category": templateResidueReason,
	},
}

// transitionExcusedLiterals excuses a fold transition literal from
// TestTemplateCarriesEveryFoldTransitionLiteral. Empty today: every
// TCreate..TVerify literal already has a display-map entry in the shipped
// template (confirmed by this test's own first RED run, where fold.TActivate
// was the only miss) — an excuse here would mean "the fold engine can emit
// this and the page will never say what it means," which spec 06's own
// C7 language treats as a defect to fix, not a decision to record.
var transitionExcusedLiterals = map[string]string{}

// checkedTemplateType names one model type this gate reflects over, plus
// its own reflect.Type.
type checkedTemplateType struct {
	name string
	typ  reflect.Type
}

// checkedTemplateTypes is the brief's own four: html.ArtifactDetail,
// html.ArtifactDetailEvent, html.ThreadOpenItem and html.Contract — the
// types F1/F2/F3/F4/F6 each name as the Go-side carrier that reaches (or
// fails to reach) the rendered page.
var checkedTemplateTypes = []checkedTemplateType{
	{"ArtifactDetail", reflect.TypeOf(ArtifactDetail{})},
	{"ArtifactDetailEvent", reflect.TypeOf(ArtifactDetailEvent{})},
	{"ThreadOpenItem", reflect.TypeOf(ThreadOpenItem{})},
	{"Contract", reflect.TypeOf(Contract{})},
}

// templateFieldCheck is one JSON tag this gate requires the shipped
// template to name, plus which occurrence bar applies to it (see
// TestTemplateRendersDeclaredFields's own doc comment for why the bar
// differs by depth).
type templateFieldCheck struct {
	label  string // Type.Field, or Type.Field[].nestedField
	tag    string
	strong bool
}

// templateFieldChecks lists t's own exported JSON-tagged fields (depth 0,
// the strong 2-occurrences-outside-a-whole-line-comment bar) plus, for any
// field whose element type is itself a struct once slice/pointer wrappers
// are stripped, that nested struct's own exported JSON-tagged fields (depth
// 1, the weak 1-occurrence-anywhere bar). reflect.VisibleFields does the
// promotion walk for an anonymous embed (datapackage.AttachmentClaim embeds
// AttachmentManifestEntry embeds Attachment) the same way encoding/json
// itself would, so Attachments[].digest is reached without this function
// hand-rolling embedding rules of its own.
func templateFieldChecks(t reflect.Type, typeName string) []templateFieldCheck {
	var out []templateFieldCheck
	seenNested := map[reflect.Type]bool{}
	for _, f := range reflect.VisibleFields(t) {
		if f.PkgPath != "" {
			continue // unexported
		}
		tag, ok := jsonTagName(f)
		if !ok {
			continue
		}
		out = append(out, templateFieldCheck{label: typeName + "." + f.Name, tag: tag, strong: true})

		nested, ok := structElemType(f.Type)
		if !ok || seenNested[nested] {
			continue
		}
		seenNested[nested] = true
		for _, nf := range reflect.VisibleFields(nested) {
			if nf.PkgPath != "" {
				continue
			}
			ntag, ok := jsonTagName(nf)
			if !ok {
				continue
			}
			out = append(out, templateFieldCheck{
				label:  typeName + "." + f.Name + "[]." + nf.Name,
				tag:    ntag,
				strong: false,
			})
		}
	}
	return out
}

// jsonTagName extracts f's bare JSON key (omitempty/omitzero and any other
// comma option stripped), reporting ok=false for an untagged field or one
// explicitly marked json:"-".
func jsonTagName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

// structElemType strips a field type's slice/pointer wrappers (one level of
// each) and reports the underlying struct type — false for a scalar, a
// map[string]any (Envelope), or a defined non-struct type like fold.Outcome
// (a string underneath), none of which this gate can meaningfully recurse
// into.
func structElemType(t reflect.Type) (reflect.Type, bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		return t, true
	}
	return nil, false
}

// opaqueBlobLineLength is the length above which a line is treated as an
// OPAQUE embedded blob rather than this file's own authored glue code, and
// excluded from both occurrence buckets entirely. The shipped template
// carries, on top of its authored <script>, two categories of giant single
// lines that this scan must not treat as evidence: the "x-dc" custom-
// element runtime's own verbatim dump of EVERY design-source component file
// as an escaped string blob (window.__resourceBlobs — confirmed by hand at
// internal/html/template.html:5136, 259765 characters, one single line),
// and the bundled, minified UI framework runtime beside it (:5144-5145,
// 38k-131k characters). Both are real bytes this file ships, and both
// contain, verbatim, nearly every field name that ever appeared in ANY
// design-source component — so counting a hit inside either turns "does
// this specific field get painted" into "did this word ever appear in any
// source file anywhere", which is strictly weaker than useless: it is a
// GUARANTEED false pass for exactly the F6-shaped defect this gate exists
// to catch (`digest` is real text inside that dump because the file
// literally embeds ArtifactDetail.dc.html's own source verbatim, whether or
// not anything ever reads `.digest` off a mounted attachment row).
// Ordinary authored lines in this file run well under a few hundred
// characters (the longest deliberately-hand-written line observed by
// direct inspection is the injected @font-face block at line 3, itself
// excluded by the same threshold) — 2000 is comfortably above any real
// glue-code line and comfortably below every blob line found.
const opaqueBlobLineLength = 2000

// countOccurrences reports how many DISTINCT LINES of haystack contain
// needle, split by whether each matching line's trimmed content starts
// with a `//` line comment. See TestTemplateRendersDeclaredFields's doc
// comment for why the comment/code split is deliberately conservative in
// one direction (a trailing same-line comment is still counted as code).
// Lines longer than opaqueBlobLineLength are skipped entirely — see that
// constant's own comment for why an embedded-blob hit is not evidence
// either way.
//
// This counts LINES, not raw substring matches, on purpose: a single-word
// JSON tag whose JS property name happens to equal the tag itself (no
// snake_case/camelCase mismatch to break the match — "attachments" is both
// the object key AND, verbatim, inside "a.attachments" on the SAME line)
// would otherwise score 2 raw matches from ONE dead prop-assignment line
// alone, silently clearing the "painted twice" bar the multi-word tags
// (whose camelCase JS property never literally contains the snake_case
// tag) do not get to cheat past. Counting lines closes that gap: F6's own
// `attachments: a.attachments || []` is one line either way it is counted.
func countOccurrences(haystack, needle string) (comment, code int) {
	for _, line := range strings.Split(haystack, "\n") {
		if len(line) > opaqueBlobLineLength {
			continue
		}
		if !strings.Contains(line, needle) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			comment++
		} else {
			code++
		}
	}
	return comment, code
}
