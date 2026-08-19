package validate

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// getvisaHandoffFrontmatter is a RECONSTRUCTION of the shape
// docs/inbox/defects/06-the-handoff-body-is-an-empty-skeleton.md measures
// across all four real getvisa handoffs (XH-seomatrix-20260808-zwn3,
// -20260818-b4qr, -gtce, -h3p4): a real, schema-valid handoff envelope
// whose frontmatter half (deliverables[], verification, fulfills,
// limitations) is populated and correct — "The frontmatter is not the
// problem" (defect 06's own words) — matching possession_test.go's own
// reconstruction convention for a private-space artifact this checkout
// does not have cloned.
const getvisaHandoffFrontmatter = `---
schema: envelope/v1
id: XH-seomatrix-20260818-h3p4
type: handoff
title: Deliver the C0 ingest transport protocol replay fixture
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: codex}
created: 2026-08-18T09:15:00Z
priority: p3
blocking: false
fulfills: [XW-axon-20260817-038c]
thread: thread:seomatrix-20260818-h3p4
refs:
  - {ref: "XC-seomatrix-20260808-abcd@1", note: "the C0 transport contract this ships as part of"}
deliverables:
  - {name: "C0 replay fixture", ref: "DP-seomatrix-20260818-9f2c@1", kind: data}
verification: "a2a data verify DP-seomatrix-20260818-9f2c"
acceptance_criteria: ["Divergences and ambiguities against the C0 transport spec are reported, not silent."]
limitations: []
classification: internal
---
`

// getvisaHandoffEmptySkeletonBody is defect 06's own measurement,
// reproduced exactly: all five §16.2 headings, zero non-whitespace
// content under any of them ("39 lines, body: 5 non-empty lines" — the
// five non-empty lines ARE the five headings themselves).
const getvisaHandoffEmptySkeletonBody = `## Context
## What was built
## How to verify
## How to operate
## Limitations & next steps
`

// getvisaHandoffPopulatedBody is the same five headings with real content
// under each — the "if this were done right" control.
const getvisaHandoffPopulatedBody = `## Context
seomatrix's C0 ingest transport protocol needed an offline replay fixture
so axon could validate its own parser without a live seomatrix endpoint.

## What was built
A data package (DP-seomatrix-20260818-9f2c) carrying the fixture transcript
plus the JSON schema and error catalogue it was captured against.

## How to verify
Run ` + "`a2a data verify DP-seomatrix-20260818-9f2c`" + ` — it replays every
recorded request/response pair against the shipped schema and reports any
divergence.

## How to operate
Point your own C0 client at the fixture transcript instead of the live
endpoint; no network access is required.

## Limitations & next steps
Only the happy-path request shapes are covered today; error responses are
tracked as a follow-up data package.
`

// requiredHandoffSectionTexts is the known, live derivation of
// schemas/templates/v1/handoff.md's five headings — asserted once here so
// every other test in this file can build fixtures against a known list
// without re-deriving it, and so a template drift is caught by this one
// assertion rather than silently by every other test's fixture shape.
func requiredHandoffSectionTexts(t *testing.T) []string {
	t.Helper()
	required, err := requiredHandoffSections()
	if err != nil {
		t.Fatalf("requiredHandoffSections: %v", err)
	}
	out := make([]string, len(required))
	for i, h := range required {
		out[i] = h.Text
	}
	return out
}

// TestRequiredHandoffSections_DerivedFromLiveTemplate pins the production
// derivation (requiredHandoffSections, which reads the embedded
// schemas.FS copy of schemas/templates/v1/handoff.md) against §16.2's five
// known sections, in template order, all at heading level 2 — proving the
// derivation reaches the real, shipped template rather than a fixture.
func TestRequiredHandoffSections_DerivedFromLiveTemplate(t *testing.T) {
	t.Parallel()
	required, err := requiredHandoffSections()
	if err != nil {
		t.Fatalf("requiredHandoffSections: %v", err)
	}
	want := []string{"Context", "What was built", "How to verify", "How to operate", "Limitations & next steps"}
	if len(required) != len(want) {
		t.Fatalf("expected %d required sections, got %d: %+v", len(want), len(required), required)
	}
	for i, h := range required {
		if h.Level != 2 {
			t.Fatalf("section %d (%q): expected level 2, got %d", i, h.Text, h.Level)
		}
		if h.Text != want[i] {
			t.Fatalf("section %d: expected %q, got %q", i, want[i], h.Text)
		}
	}
}

// TestRequiredSectionsFromTemplate_SixthSectionEnforcedWithNoGoEdit is the
// mutation test AC (spec 09 §8 row 4): requiredSectionsFromTemplate takes
// template BYTES as its input specifically so this test can prove the
// derivation adds a sixth section with NO Go edit and without touching
// schemas/templates/v1/handoff.md (off this package's allowlist) — it
// feeds a hand-built six-section template copy and checkHandoffSections
// against a body missing only the sixth section's content, and watches the
// sixth section get named.
func TestRequiredSectionsFromTemplate_SixthSectionEnforcedWithNoGoEdit(t *testing.T) {
	t.Parallel()

	sixSectionTemplate := []byte(`---
schema: envelope/v1
id: XH-<system>-<YYYYMMDD>-<rand4>
type: handoff
title: <title>
space: <space-id>
from: <producing-system>
to: [<receiving-system>]
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}
created: <RFC-3339 UTC>
priority: p3
blocking: false
fulfills: [<originating exchange/requirement id>]
thread: <thread:system-YYYYMMDD-rand4>
deliverables:
  - {name: "<deliverable name>", ref: "<ref>", kind: contract}
verification: "<verification>"
acceptance_criteria: ["<restated measurably>"]
limitations: []
classification: internal
---
## Context
## What was built
## How to verify
## How to operate
## Limitations & next steps
## Rollback plan
`)

	required, err := requiredSectionsFromTemplate(sixSectionTemplate)
	if err != nil {
		t.Fatalf("requiredSectionsFromTemplate: %v", err)
	}
	if len(required) != 6 {
		t.Fatalf("expected 6 sections from the mutated template, got %d: %+v", len(required), required)
	}
	if required[5].Text != "Rollback plan" {
		t.Fatalf("expected the sixth section to be %q, got %q", "Rollback plan", required[5].Text)
	}

	// A body populating only the original five (the sixth's heading is
	// entirely absent) must now name the sixth as missing — proving the
	// CHECK, not just the derivation, picks up the new section. This talks
	// to sectionContentFor/parseMarkdownSections directly (the same
	// functions checkHandoffSections composes) rather than
	// checkHandoffSections itself, because that function derives its
	// required list from the REAL embedded template, which this test
	// deliberately does not touch.
	present := parseMarkdownSections([]byte(getvisaHandoffPopulatedBody))
	if _, found := sectionContentFor(present, required[5]); found {
		t.Fatalf("expected the mutated template's sixth section to be absent from a five-section body, but it was found")
	}
	for _, want := range required[:5] {
		if content, found := sectionContentFor(present, want); !found || strings.TrimSpace(content) == "" {
			t.Fatalf("expected section %q to be present and populated, found=%v content=%q", want.Text, found, content)
		}
	}
}

// TestCheckHandoffSections_AllFiveEmptyRefusesNamingAll is AC-1: the
// getvisa reconstruction (all five headings, zero content) is refused,
// naming every one of the five sections.
func TestCheckHandoffSections_AllFiveEmptyRefusesNamingAll(t *testing.T) {
	t.Parallel()
	got, err := checkHandoffSections([]byte(getvisaHandoffEmptySkeletonBody), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	v := got[0]
	if v.Code != "POL-022" {
		t.Fatalf("expected POL-022, got %q", v.Code)
	}
	if v.Severity != SeverityReject {
		t.Fatalf("expected SeverityReject, got %q", v.Severity)
	}
	want := requiredHandoffSectionTexts(t)
	if len(v.Subjects) != len(want) {
		t.Fatalf("expected all %d sections named, got Subjects=%+v", len(want), v.Subjects)
	}
	for i, s := range want {
		if v.Subjects[i] != s {
			t.Fatalf("Subjects[%d]: expected %q, got %q (full: %+v)", i, s, v.Subjects[i], v.Subjects)
		}
		if !strings.Contains(v.Message, s) {
			t.Fatalf("expected Message to name %q, got %q", s, v.Message)
		}
	}
}

// TestCheckHandoffSections_AllPopulatedSilent is the control: every
// section carrying real content produces zero violations.
func TestCheckHandoffSections_AllPopulatedSilent(t *testing.T) {
	t.Parallel()
	got, err := checkHandoffSections([]byte(getvisaHandoffPopulatedBody), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected zero violations for a fully-populated handoff body, got %+v", got)
	}
}

// TestCheckHandoffSections_MissingHeadingNamedSameAsEmpty is spec 09's own
// row: a missing heading is named the same way an empty one is. One
// section (`## How to operate`) is dropped entirely (not merely emptied);
// the other four are populated.
func TestCheckHandoffSections_MissingHeadingNamedSameAsEmpty(t *testing.T) {
	t.Parallel()
	body := `## Context
Real context.

## What was built
Real deliverable narrative.

## How to verify
Real verification steps.

## Limitations & next steps
Real limitations.
`
	got, err := checkHandoffSections([]byte(body), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	if got[0].Severity != SeverityReject {
		t.Fatalf("expected SeverityReject, got %q", got[0].Severity)
	}
	if len(got[0].Subjects) != 1 || got[0].Subjects[0] != "How to operate" {
		t.Fatalf("expected exactly [%q] named, got %+v", "How to operate", got[0].Subjects)
	}
}

// TestCheckHandoffSections_CodeFenceOnlyIsContent is spec 09's edge case:
// a section carrying only a fenced code block is content, not emptiness.
// It also exercises fence-awareness: the fence's own content includes a
// line starting with `#` (a shell comment), which must NOT be misread as
// a heading and must NOT terminate the section early.
func TestCheckHandoffSections_CodeFenceOnlyIsContent(t *testing.T) {
	t.Parallel()
	body := "## Context\nReal context.\n\n" +
		"## What was built\nReal deliverable narrative.\n\n" +
		"## How to verify\n```sh\n# run this\ngo test ./...\n```\n\n" +
		"## How to operate\nReal operate narrative.\n\n" +
		"## Limitations & next steps\nReal limitations.\n"

	got, err := checkHandoffSections([]byte(body), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected a fenced-code-only section to count as content, got %+v", got)
	}

	// Directly pin fence-awareness: the fenced `# run this` line must NOT
	// be read as a sixth (phantom) heading — exactly 5 headings, matching
	// the 5 written above, or a fence-unaware parse would silently
	// fragment "How to verify" into two sections without necessarily
	// tripping the zero-violations check above (both fragments can still
	// be individually non-blank).
	sections := parseMarkdownSections([]byte(body))
	if len(sections) != 5 {
		t.Fatalf("expected exactly 5 headings (no phantom heading from the fenced `# run this` line), got %d: %+v", len(sections), sections)
	}
}

// TestCheckHandoffSections_ListOrLinkOnlyIsContent covers the other two
// named shapes from spec 09's edge cases: a section carrying only a list,
// or only a link, is content.
func TestCheckHandoffSections_ListOrLinkOnlyIsContent(t *testing.T) {
	t.Parallel()
	body := "## Context\n- one\n- two\n\n" +
		"## What was built\n[see the deliverable](https://example.invalid/d)\n\n" +
		"## How to verify\nReal verification steps.\n\n" +
		"## How to operate\nReal operate narrative.\n\n" +
		"## Limitations & next steps\nReal limitations.\n"

	got, err := checkHandoffSections([]byte(body), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected a list-only and a link-only section to both count as content, got %+v", got)
	}
}

// TestCheckHandoffSections_SubheadingDoesNotEmptyParentSection is the
// section-boundary edge case: a nested sub-heading (`### Subcomponent`)
// inside "What was built" must NOT terminate that section early — only a
// same-or-shallower (level <= 2) heading may. A false refusal here would
// punish a well-authored handoff for using sub-structure.
func TestCheckHandoffSections_SubheadingDoesNotEmptyParentSection(t *testing.T) {
	t.Parallel()
	body := `## Context
Real context.

## What was built
### Subcomponent A
Narrative about subcomponent A.
### Subcomponent B
Narrative about subcomponent B.

## How to verify
Real verification steps.

## How to operate
Real operate narrative.

## Limitations & next steps
Real limitations.
`
	got, err := checkHandoffSections([]byte(body), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected sub-headings inside a section to still count as that section's own content, got %+v", got)
	}
}

// TestCheckHandoffSections_NonHandoffTypeNeverJudged is spec 09's own
// scope row: no other type is judged, even when the exact same
// empty-skeleton body shape is fed in.
func TestCheckHandoffSections_NonHandoffTypeNeverJudged(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"work_request", "response", "decision", "question", "requirement", "announcement", "contract", ""} {
		got, err := checkHandoffSections([]byte(getvisaHandoffEmptySkeletonBody), typ)
		if err != nil {
			t.Fatalf("checkHandoffSections(%q): %v", typ, err)
		}
		if len(got) != 0 {
			t.Fatalf("checkHandoffSections(%q): expected zero violations for a non-handoff type, got %+v", typ, got)
		}
	}
}

// TestCheckHandoffSections_GetvisaReconstructionRefusedAtWriteGate is the
// engine-adjacent half of AC-1: the full reconstructed getvisa artifact
// (frontmatter + empty-skeleton body) reproduces defect 06's own finding
// through checkHandoffSections directly — this package's call boundary, the
// same one the hook at internal/validate/engine.go:runCommonEnvelope will
// pass fm.Body/env.Type through once the lead wires it in (see this
// brief's lead_actions_needed; engine.go is lead-reserved for this wave's
// allowlist, so ValidateDraft cannot be used as the entry point here yet).
func TestCheckHandoffSections_GetvisaReconstructionRefusedAtWriteGate(t *testing.T) {
	t.Parallel()
	raw := []byte(getvisaHandoffFrontmatter + getvisaHandoffEmptySkeletonBody)
	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		t.Fatalf("parsing the reconstructed getvisa handoff fixture: %v", ferr)
	}

	got, err := checkHandoffSections(fm.Body, "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 1 || got[0].Code != "POL-022" {
		t.Fatalf("expected exactly one POL-022 violation on the getvisa reconstruction, got %+v", got)
	}
	want := requiredHandoffSectionTexts(t)
	if len(got[0].Subjects) != len(want) {
		t.Fatalf("expected all %d sections named on the getvisa reconstruction, got %+v", len(want), got[0].Subjects)
	}
}

// TestHandoffSectionsMode_RefusedAtPRSilentAtFullRepo is this package's own
// in-allowlist half of the mode-scope mirror
// (TestValidateCI_REF017ScopedToPRModeNotFullRepo, internal/cli/
// cmd_validate_ci_test.go): one Result, carrying POL-022 exactly as
// checkHandoffSections would have contributed it through the engine's v3
// pipeline, is refused as-is (the v3-pr shape: the code enforces
// unchanged) and carries no verdict for it once Result.SuppressingCode
// is applied — the SAME call cmd_validate_ci.go's mode=="v3-full-repo"
// branch makes for REF-017 today, and would make for POL-022 once the
// lead adds the sibling line (see lead_actions_needed). This cannot drive
// the real CLI binary itself: internal/cli/cmd_validate_ci.go and
// internal/validate/engine.go are both lead-reserved for this wave's
// allowlist, so the full CLI-tier mirror is the lead's to add once both
// hooks land.
func TestHandoffSectionsMode_RefusedAtPRSilentAtFullRepo(t *testing.T) {
	t.Parallel()
	got, err := checkHandoffSections([]byte(getvisaHandoffEmptySkeletonBody), "handoff")
	if err != nil {
		t.Fatalf("checkHandoffSections: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}

	// v3-pr: the write gate keeps enforcing POL-022 unchanged.
	prResult := newResult(V3, "XH-seomatrix-20260818-h3p4", got)
	if prResult.Valid {
		t.Fatalf("expected v3-pr to refuse the empty-skeleton handoff, got Valid=true")
	}
	if v := violationWithCode(prResult.Violations, "POL-022"); v == nil {
		t.Fatalf("expected POL-022 in the v3-pr result, got %+v", prResult.Violations)
	} else if v.Severity != SeverityReject {
		t.Fatalf("expected POL-022 SeverityReject in v3-pr, got %q", v.Severity)
	}

	// v3-full-repo: the post-merge audit must return NO VERDICT for
	// POL-022 — the same suppression cmd_validate_ci.go applies for
	// REF-017, applied here directly since that call site is
	// lead-reserved.
	fullRepoResult := prResult.SuppressingCode("POL-022")
	if !fullRepoResult.Valid {
		t.Fatalf("expected v3-full-repo to suppress POL-022 and pass, got Valid=false violations=%+v", fullRepoResult.Violations)
	}
	if v := violationWithCode(fullRepoResult.Violations, "POL-022"); v != nil {
		t.Fatalf("expected POL-022 suppressed in v3-full-repo, got %+v", v)
	}
	// prResult itself must be untouched by SuppressingCode (result.go's
	// own contract: "r's own Violations slice is never mutated").
	if v := violationWithCode(prResult.Violations, "POL-022"); v == nil {
		t.Fatalf("expected the original v3-pr result to still carry POL-022 after SuppressingCode, got %+v", prResult.Violations)
	}
}
