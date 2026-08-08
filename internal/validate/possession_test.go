package validate

import (
	"strings"
	"testing"
)

// possessionAC1Body is a RECONSTRUCTION of the real getvisa incident body
// (docs/inbox/agent-exchange-review/03-bytes-note.md) — it is NOT the
// committed XW-axon-20260801-vet6 artifact. That artifact lives in a
// private space this checkout does not have cloned; the plan's own
// 2026-08-08 amendment ("AC1's stated verification cannot be executed")
// records why replaying the real bytes is impossible and directs this
// reconstruction instead. Every factual shape below is drawn from the
// note: a work_request naming a review bundle by digest, in prose, with
// no attachment/refs/origin declared and no fetch locator
// (03-bytes-note.md §1, §2's own bundle listing), plus the note's own
// "listed its eight files" — a markdown-table-shaped file listing feeds
// AC7's heuristic in the SAME body, which is a fact about the real
// incident, not a test artifact (see TestPossession_AC1... below, which
// asserts on Valid + the presence of REF-017 rather than on an exact
// violation count for exactly this reason).
const possessionAC1Frontmatter = `---
schema: envelope/v1
id: XW-axon-20260801-rcn1
type: work_request
title: Replay the C0 ingest transport protocol fixture and report divergences
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: 2026-08-01T09:15:00Z
category: investigation
priority: p3
blocking: false
interim_behavior: "Reviewer waits; no interim workaround exists."
needed_by: 2026-08-08
acceptance_criteria:
  - "Divergences and ambiguities against the C0 transport spec are reported, not silent."
expected_response: {shape: "XS naming replayed-count/total and any divergence found."}
thread: thread:axon-20260801-p3f8
classification: internal
---
`

const possessionAC1Body = `> RECONSTRUCTED from docs/inbox/agent-exchange-review/03-bytes-note.md (the
> counterparty's own account of the real getvisa incident, 2026-08-01 to
> 2026-08-08) — this is NOT the committed XW-axon-20260801-vet6 artifact,
> which lives in a private space this checkout does not have cloned.

Replay our offline fixture transcript of the C0 ingest transport protocol and
report divergences and ambiguities.

review_id     c0-review-20260731-d9ad8acd
review_digest sha256:d9ad8acd675245

The bundle contains:
- c0.schema.json
- protocol.json
- errors.json
- fixtures/requests/request-001.json
- fixtures/requests/request-002.json
- fixtures/expected/response-001.json
- fixtures/expected/response-002.json
- README.md
`

// possessionAC7Body carries only the file-tree shape from the same
// incident (03-bytes-note.md rule 2: "a body that enumerates a file tree
// while the artifact carries no attachment is, in practice, always a
// missing attachment... a warning is enough") with NO digest at all, so
// it isolates AC7 from AC1: zero REF-017, exactly one POL-017.
const possessionAC7Body = `Here is the review bundle's layout for reference.

- c0.schema.json
- protocol.json
- errors.json
- fixtures/requests/request-001.json
`

// withoutPossessionCodes filters REF-017/POL-017 out of vs — this
// package's rail for "would have passed before this phase" (AC1's other
// required property): the pre-phase engine could not have produced
// either code, so what remains after removing them is exactly what the
// pre-phase binary would have reported.
func withoutPossessionCodes(vs []Violation) []Violation {
	var out []Violation
	for _, v := range vs {
		if v.Code == "REF-017" || v.Code == "POL-017" {
			continue
		}
		out = append(out, v)
	}
	return out
}

func violationWithCode(vs []Violation, code string) *Violation {
	for i := range vs {
		if vs[i].Code == code {
			return &vs[i]
		}
	}
	return nil
}

// TestPossession_AC1_IncidentReconstructionRefusedAtValidate is AC1: the
// reconstructed incident body passes `a2a validate` before this phase
// (no other violation fires) and is refused after (REF-017, reject).
func TestPossession_AC1_IncidentReconstructionRefusedAtValidate(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	raw := []byte(possessionAC1Frontmatter + possessionAC1Body)
	result, err := engine.ValidateDraft(Draft{
		Path: "axon/exchanges/XW-axon-20260801-rcn1.md",
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}

	// Pre-phase: with REF-017/POL-017 filtered out, nothing else about
	// this reconstructed body was ever a violation — this is what "it
	// must pass today" (the plan's own AC1 row) means, made checkable
	// without a toggle this package does not have.
	if got := withoutPossessionCodes(result.Violations); len(got) != 0 {
		t.Fatalf("expected zero non-possession violations, got %+v", got)
	}

	// Post-phase: refused, and specifically by REF-017 — never asserted
	// by exact violation count, because the same reconstructed body also
	// trips AC7's file-tree heuristic (POL-017, warning) in the same
	// breath; that is a true fact about the real incident's own body
	// (03-bytes-note.md's own listed eight files), not a test artifact.
	if result.Valid {
		t.Fatalf("expected the reconstructed incident body to be refused, got Valid=true")
	}
	v := violationWithCode(result.Violations, "REF-017")
	if v == nil {
		t.Fatalf("expected a REF-017 violation, got %+v", result.Violations)
	}
	if v.Severity != SeverityReject {
		t.Fatalf("expected REF-017 to be SeverityReject, got %q", v.Severity)
	}
}

// TestPossession_AC7_FileTreeNoAttachmentWarns is AC7: a body that
// enumerates a file tree, naming no digest at all and with no attachment
// declared, produces exactly one WARNING and zero refusals.
func TestPossession_AC7_FileTreeNoAttachmentWarns(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	raw := []byte(possessionAC1Frontmatter + possessionAC7Body)
	result, err := engine.ValidateDraft(Draft{
		Path: "axon/exchanges/XW-axon-20260801-rcn1.md",
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}

	if !result.Valid {
		t.Fatalf("expected Valid=true (a warning never refuses), got violations: %+v", result.Violations)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", result.Violations)
	}
	v := result.Violations[0]
	if v.Code != "POL-017" {
		t.Fatalf("expected POL-017, got %q", v.Code)
	}
	if v.Severity != SeverityWarning {
		t.Fatalf("expected SeverityWarning, got %q", v.Severity)
	}
}

// TestCheckPossession_DeclaredDigestProducesNoViolation proves the
// universe is DERIVED, not asserted: a body token that IS carried by a
// declared attachments[].digest on the same envelope produces nothing —
// the plan's §The gate's own requirement, which a "refuse every sha256:
// token" implementation would satisfy every OTHER test here without
// ever proving.
func TestCheckPossession_DeclaredDigestProducesNoViolation(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("abcdef0123456789", 4) // 64 lowercase hex chars
	instance := map[string]any{
		"attachments": []any{
			map[string]any{
				"ref":          "BL-axon-20260801-9f2c",
				"digest":       digest,
				"verification": "none",
				"retention":    "pinned",
			},
		},
	}
	body := []byte("The bundle is at " + digest + " — already attached above.")

	got := checkPossession(body, instance)
	if len(got) != 0 {
		t.Fatalf("expected no violations for a declared digest, got %+v", got)
	}
}

// TestCheckPossession_UndeclaredDigestRefuses is the unit-level mirror of
// AC1: a body digest with NO matching declared attachment is REF-017,
// even when other attachments exist (proves the check is per-token, not
// "any attachment present ⇒ pass").
func TestCheckPossession_UndeclaredDigestRefuses(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"attachments": []any{
			map[string]any{
				"ref":          "BL-axon-20260801-aaaa",
				"digest":       "sha256:" + strings.Repeat("1", 64),
				"verification": "none",
				"retention":    "pinned",
			},
		},
	}
	body := []byte("See sha256:" + strings.Repeat("2", 64) + " for the bundle.")

	got := checkPossession(body, instance)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	if got[0].Code != "REF-017" {
		t.Fatalf("expected REF-017, got %q", got[0].Code)
	}
	if got[0].Severity != SeverityReject {
		t.Fatalf("expected SeverityReject, got %q", got[0].Severity)
	}
}

// TestCheckPossession_FileTreeWithDeclaredAttachmentDoesNotWarn proves
// POL-017's OTHER half of AC7's own text ("while the artifact carries no
// attachment"): the same file-tree-shaped body produces no warning once
// even one attachment is declared, because the smell it is naming (an
// enumerated tree with nothing to back it) is no longer true.
func TestCheckPossession_FileTreeWithDeclaredAttachmentDoesNotWarn(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"attachments": []any{
			map[string]any{
				"ref":          "BL-axon-20260801-aaaa",
				"digest":       "sha256:" + strings.Repeat("1", 64),
				"verification": "none",
				"retention":    "pinned",
			},
		},
	}
	got := checkPossession([]byte(possessionAC7Body), instance)
	if len(got) != 0 {
		t.Fatalf("expected no violation once an attachment is declared, got %+v", got)
	}
}

// TestCheckPossession_ShortFileListDoesNotWarn is the heuristic's own
// lower bound: one or two filename-shaped lines is a `refs` entry or a
// passing aside, not an inventory — AC7's warning requires at least
// fileTreeLineThreshold lines before it fires.
func TestCheckPossession_ShortFileListDoesNotWarn(t *testing.T) {
	t.Parallel()
	body := []byte("See c0.schema.json and protocol.json for the shapes.")
	got := checkPossession(body, nil)
	if len(got) != 0 {
		t.Fatalf("expected no violation below the file-tree threshold, got %+v", got)
	}
}
