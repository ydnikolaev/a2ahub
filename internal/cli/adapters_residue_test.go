package cli_test

// LFC-004 reachability: internal/validate/incompleteness.go's package-doc
// Deviation names the exact gap this file closes — checkResidue's trigger
// (a close/withdraw/cancel event over a response's own `parent`) can never
// reach it through SubmitValidatorAdapter.ValidateSubmit, because that
// adapter built each draft's own candidate-event list keyed ONLY on the
// draft's own id (events[probe.ID]) — a closing event's Subject is always
// the PARENT's id, never the response's own. This file proves the fix
// through the REAL adapter (not incompleteness_test.go's hand-built
// []CandidateEvent), and pins the mutation this brief calls for: reverting
// adapters.go's widening back to the draft-id-only filter must make LFC-004
// disappear from the result, named explicitly.

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// residueHasCode reports whether vs contains a violation with exactly code.
func residueHasCode(vs []validate.Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}

// TestSubmitValidatorAdapter_LFC004_ClosingParentWithUnmetCriterionRefuses
// is this brief's own acceptance proof: a response draft whose `unmet[]`
// names a still-open criterion, submitted through the real
// SubmitValidatorAdapter alongside BOTH the response's own submit event
// (subject = the response's own id, exactly like every real `a2a submit`/
// `a2a respond` batch mints) AND a `close` event over the response's
// `parent` — in ONE ValidateSubmit call, the shape §7/AC8's own D3 account
// describes. Before this file's adapters.go fix, the close event's Subject
// (the parent's id) never matched the response draft's own probe.ID, so it
// was silently dropped from that draft's candidate list and LFC-004 could
// never fire in production.
//
// The parent's own candidate carries a ZERO-VALUE Envelope (no-silent-
// yes-2026-08/P6, US-3: adapters.go's ValidateSubmit has no way to look up
// an unrelated parent's envelope facts, documented on that widening's own
// comment) — checkResidue (LFC-004) never reads Envelope at all, so this
// is inert against THIS test's own assertion; it only means the widened
// close candidate's OWN legality verdict (a separate check, LFC-001/002)
// is not meaningful here, which this test does not assert against either
// way.
func TestSubmitValidatorAdapter_LFC004_ClosingParentWithUnmetCriterionRefuses(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember},
		{System: "seomatrix", Status: fold.MembershipMember},
	}}
	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	const responseID = "XS-axon-20260809-lfc04"
	const parentID = "XW-axon-20260801-close1"

	responseContent := []byte("---\n" +
		"schema: envelope/v2\n" +
		"id: " + responseID + "\n" +
		"type: response\n" +
		"title: Reviewer sign-off is a draft candidate, native-TR confirmation still open\n" +
		"space: getvisa\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"thread: thread:axon-20260809-lfc04\n" +
		"actor: {kind: agent, name: codex}\n" +
		"created: \"2026-08-09T08:00:00Z\"\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"parent: " + parentID + "\n" +
		"result: partial\n" +
		"unmet: [0]\n" +
		"blocked_by:\n" +
		"  reason_code: out-of-scope\n" +
		"  owner: seomatrix\n" +
		"  needs: judgement\n" +
		"---\nRECONSTRUCTED (D3): a native-TR confirmation gating a downstream release was still open when this parent closed.\n")

	submitEventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3RESIDUE0000001\nspace: getvisa\n" +
		"subject: " + responseID + "\ntransition: submit\nactor: {kind: agent, name: codex, system: axon}\n" +
		"at: 2026-08-09T08:00:00Z\n")

	closeEventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3RESIDUE0000002\nspace: getvisa\n" +
		"subject: " + parentID + "\ntransition: close\nactor: {kind: agent, name: codex, system: axon}\n" +
		"at: 2026-08-09T08:01:00Z\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/" + responseID + ".md", Content: responseContent},
		{Path: "axon/events/2026/01J8QYK2Z3RESIDUE0000001.yaml", Content: submitEventContent},
		{Path: "axon/events/2026/01J8QYK2Z3RESIDUE0000002.yaml", Content: closeEventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("expected a validation error: unmet[0] with no residue, closed alongside the response, must refuse (LFC-004)")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	if !residueHasCode(violationErr.Violations, "LFC-004") {
		t.Fatalf("expected LFC-004 among violations, got %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapter_LFC004_NoAccompanyingCloseEventIsUnaffected
// pins the OTHER half of AC8's own trigger condition through the real
// adapter: the SAME unmet[] response, submitted with only its own submit
// event (no close event over its parent anywhere in this batch), must
// never carry LFC-004 — an ordinary in-flight partial response is not a
// residue violation on its own (incompleteness_test.go's own
// TestCheckResidue_NoClosingEventProducesNoViolation, replayed here through
// ValidateSubmit rather than checkResidue directly).
func TestSubmitValidatorAdapter_LFC004_NoAccompanyingCloseEventIsUnaffected(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember},
		{System: "seomatrix", Status: fold.MembershipMember},
	}}
	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	const responseID = "XS-axon-20260809-lfc04b"
	const parentID = "XW-axon-20260801-close2"

	responseContent := []byte("---\n" +
		"schema: envelope/v2\n" +
		"id: " + responseID + "\n" +
		"type: response\n" +
		"title: Reviewer sign-off is a draft candidate, native-TR confirmation still open\n" +
		"space: getvisa\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"thread: thread:axon-20260809-lfc04b\n" +
		"actor: {kind: agent, name: codex}\n" +
		"created: \"2026-08-09T08:00:00Z\"\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"parent: " + parentID + "\n" +
		"result: partial\n" +
		"unmet: [0]\n" +
		"blocked_by:\n" +
		"  reason_code: out-of-scope\n" +
		"  owner: seomatrix\n" +
		"  needs: judgement\n" +
		"---\nRECONSTRUCTED (D3): a native-TR confirmation gating a downstream release was still open when this parent closed.\n")

	submitEventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3RESIDUE0000003\nspace: getvisa\n" +
		"subject: " + responseID + "\ntransition: submit\nactor: {kind: agent, name: codex, system: axon}\n" +
		"at: 2026-08-09T08:00:00Z\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/" + responseID + ".md", Content: responseContent},
		{Path: "axon/events/2026/01J8QYK2Z3RESIDUE0000003.yaml", Content: submitEventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		return
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError (if any error), got %T: %v", err, err)
	}
	if residueHasCode(violationErr.Violations, "LFC-004") {
		t.Fatalf("expected no LFC-004 without an accompanying close event, got %+v", violationErr.Violations)
	}
}
