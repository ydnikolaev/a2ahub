package validate

import (
	"strings"
	"testing"
)

// declarationBlockAC3Frontmatter is a RECONSTRUCTION of
// XW-axon-20260801-vet6's own frontmatter (the artifact quoted verbatim
// in docs/features/archive/agent-exchange-2026-08/specs/05-declared-nature.md
// "The evidence, quoted"), bumped to envelope/v2 so the artifact's own
// schema now carries the `binding` field the incident had none of at the
// time — proving POL-018 fires on the hand-rolled block regardless of
// whether the real field is filled. That artifact lives in a private
// space this checkout does not have cloned; only the fenced block's exact
// keys are drawn from the spec's own quote, matching possession_test.go's
// own reconstruction convention for the neighbouring incident.
const declarationBlockAC3Frontmatter = `---
schema: envelope/v2
id: XW-axon-20260801-vet6
type: work_request
title: Review the non-binding compatibility bundle and report findings
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: 2026-08-01T09:15:00Z
category: investigation
priority: p3
blocking: false
interim_behavior: "Reviewer waits; no interim workaround exists."
acceptance_criteria:
  - "Findings on the bundle's adoptability are reported, not silent."
thread: thread:axon-20260801-vet6
classification: internal
---
`

// declarationBlockAC3Body is the spec's own "evidence, quoted" fenced
// block, reproduced with NO `sha256:` token anywhere — the case AC3 names
// as the one REF-017's digest scan cannot reach: "an artifact without a
// digest token passes" (spec 05 §8 AC3 row). If this body is refused, it
// must be POL-018 doing it, not REF-017 being lucky.
const declarationBlockAC3Body = `The package declares it in machine-readable form. For readers who cannot
parse frontmatter, here it is again:

artifact_class       = non_binding_review
compatibility_status = none
adoptable            = false
runtime_pinnable     = false

No HTTP endpoint implements the reviewed envelope today. We would rather
you learn that from this paragraph than from a failed integration.
`

// TestDeclarationBlock_AC3_LiveBodyRefusedWithNoDigestToken is AC3/US-4,
// literally: the live body's declaration block, carrying no `sha256:`
// token, is refused — and specifically by POL-018, proving the accident
// (REF-017 happening to hit the same fixture) is closed.
func TestDeclarationBlock_AC3_LiveBodyRefusedWithNoDigestToken(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	raw := []byte(declarationBlockAC3Frontmatter + declarationBlockAC3Body)
	result, err := engine.ValidateDraft(Draft{
		Path: "axon/exchanges/XW-axon-20260801-vet6.md",
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}

	if result.Valid {
		t.Fatalf("expected the live declaration block to be refused, got Valid=true")
	}
	v := violationWithCode(result.Violations, "POL-018")
	if v == nil {
		t.Fatalf("expected a POL-018 violation, got %+v", result.Violations)
	}
	if v.Severity != SeverityReject {
		t.Fatalf("expected POL-018 to be SeverityReject, got %q", v.Severity)
	}
	// The accident, re-disproven: this body carries no digest token at all,
	// so REF-017 (possession.go's digest scan) structurally cannot fire —
	// asserting its absence pins that POL-018, not a lucky overlap with
	// P4's rule, is what refuses this body.
	if got := violationWithCode(result.Violations, "REF-017"); got != nil {
		t.Fatalf("expected no REF-017 (this body carries no sha256: token), got %+v", got)
	}
}

// TestDeclarationBlock_DigestPresenceIsIndependentOfPOL018 is the
// "mutate the possession scan back on" half: adding a `sha256:` token to the
// same declaration block makes the possession scan ALSO fire, proving the two
// codes are independent checks over the same body rather than POL-018 silently
// riding on the possession scan (or vice versa).
//
// It expects POL-017, not REF-017, and that is ADR-011 rather than a weakened
// test. This body is four `key = value` lines plus prose: it carries no
// file-tree-shaped line, so the conjunction REF-017 now requires — an
// undeclared digest AND a file-tree enumeration AND zero declared attachments —
// cannot hold. A bare digest named in prose warns; it does not refuse. The
// independence this test exists to prove is unaffected: POL-018 and the
// possession scan still fire from separate rules over the same bytes.
func TestDeclarationBlock_DigestPresenceIsIndependentOfPOL018(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	bodyWithDigest := declarationBlockAC3Body + "\nreview_digest sha256:" + strings.Repeat("a", 64) + "\n"
	raw := []byte(declarationBlockAC3Frontmatter + bodyWithDigest)
	result, err := engine.ValidateDraft(Draft{
		Path: "axon/exchanges/XW-axon-20260801-vet6.md",
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}

	if violationWithCode(result.Violations, "POL-018") == nil {
		t.Fatalf("expected POL-018 to still fire once a digest token is added, got %+v", result.Violations)
	}
	if violationWithCode(result.Violations, "POL-017") == nil {
		t.Fatalf("expected POL-017 to fire once the body names an undeclared digest, got %+v", result.Violations)
	}
	if got := violationWithCode(result.Violations, "REF-017"); got != nil {
		t.Fatalf("expected NO REF-017: this body has no file-tree-shaped line, so ADR-011's conjunction cannot hold, got %+v", got)
	}
}

// TestCheckDeclarationBlock_ForbiddenKeysAreDerivedFromTheArtifactsOwnType
// is the DERIVATION property the AC row makes load-bearing: the forbidden
// set is schema.EnvelopeFieldPaths(version, typ) for the CALLER'S type,
// never a fixed list. "generated_from" is a real field — but only on
// envelope/v2 contract, never on work_request's own (base ∪ work_request)
// field set — so it must NOT fire when checking a work_request body, even
// though it names a real schema field somewhere in the corpus. A
// hardcoded four-key list (the exact keys the live incident happened to
// use) would also pass this fixture; only a derived, per-type set proves
// the difference.
func TestCheckDeclarationBlock_ForbiddenKeysAreDerivedFromTheArtifactsOwnType(t *testing.T) {
	t.Parallel()

	body := []byte("generated_from = something\n")
	got, err := checkDeclarationBlock(body, 2, "work_request")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no violation for a field declared on a DIFFERENT type only, got %+v", got)
	}

	// Sanity: the same key IS forbidden when checking the type it is
	// actually declared on, proving the miss above is about scoping to
	// the artifact's own type and not a broken pattern/typo.
	got, err = checkDeclarationBlock(body, 2, "contract")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 1 || got[0].Code != "POL-018" {
		t.Fatalf("expected POL-018 for generated_from on contract's own type, got %+v", got)
	}
}

// TestCheckDeclarationBlock_UnknownKeyPasses is the "reds when removed"
// mutation partner: a `key = value` line whose key names no field on the
// schema at all produces nothing — the rule fires on a declared field
// name, never on the mere SHAPE of a `key = value` line (which would make
// it unconditional and refuse ordinary prose like "x = y" examples).
func TestCheckDeclarationBlock_UnknownKeyPasses(t *testing.T) {
	t.Parallel()
	body := []byte("totally_made_up_field = some value\n")
	got, err := checkDeclarationBlock(body, 2, "work_request")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no violation for a key matching no declared field, got %+v", got)
	}
}

// TestCheckDeclarationBlock_MidSentenceMentionDoesNotFire is the
// line-anchoring guard, mirroring possession.go's fileTreeLinePattern
// test discipline: a real field name appearing in a `key = value` shape
// mid-sentence (not at the start of its own line) must not fire, exactly
// as prose mentioning a filename mid-sentence does not trip POL-017.
func TestCheckDeclarationBlock_MidSentenceMentionDoesNotFire(t *testing.T) {
	t.Parallel()
	// "category" is a real declared field on work_request (required,
	// top-level) — but it appears mid-sentence here, never at the start
	// of its own line.
	body := []byte("See the doc where category = \"investigation\" is explained further.\n")
	got, err := checkDeclarationBlock(body, 2, "work_request")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no violation for a mid-sentence mention, got %+v", got)
	}
}

// TestCheckDeclarationBlock_MultipleForbiddenKeysProduceOneViolationEach
// pins the emission shape against possession.go's REF-017 sibling: one
// violation per distinct offending key, not one per document and not one
// per line (a key repeated across lines still yields exactly one).
func TestCheckDeclarationBlock_MultipleForbiddenKeysProduceOneViolationEach(t *testing.T) {
	t.Parallel()
	body := []byte(
		"artifact_class       = non_binding_review\n" +
			"compatibility_status = none\n" +
			"adoptable            = false\n" +
			"runtime_pinnable     = false\n" +
			"adoptable            = false\n", // repeated key, deliberately
	)
	got, err := checkDeclarationBlock(body, 2, "work_request")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected exactly 4 violations (one per distinct key), got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if v.Code != "POL-018" {
			t.Fatalf("expected every violation to be POL-018, got %+v", v)
		}
		if v.Severity != SeverityReject {
			t.Fatalf("expected SeverityReject, got %+v", v)
		}
	}
}

// TestCheckDeclarationBlock_EmptyBodyProducesNothing is the base case: no
// `key = value` lines at all, no violations, no error.
func TestCheckDeclarationBlock_EmptyBodyProducesNothing(t *testing.T) {
	t.Parallel()
	got, err := checkDeclarationBlock([]byte("Just ordinary prose with no declarations.\n"), 2, "work_request")
	if err != nil {
		t.Fatalf("checkDeclarationBlock: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no violations, got %+v", got)
	}
}
