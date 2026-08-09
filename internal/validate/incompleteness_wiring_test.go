package validate

import "testing"

// incompleteness_wiring_test.go pins the SECOND half of incompleteness.go's
// package-doc "Deviation: both rules are DORMANT under today's only
// production caller" — the LFC-004 half. REF-018's own caller-contract gap
// (a Resolver not implementing ParentCriteriaCounter) is already pinned by
// incompleteness_test.go's TestCheckUnmetIndexRange_DormantWithoutParentCriteriaCounter
// and closed in production by cmd/a2a/validate_resolver.go (this wave). The
// LFC-004 gap below is NOT closed by this wave: the fix lives in
// internal/cli/adapters.go's SubmitValidatorAdapter.ValidateSubmit
// (adapters.go:756-768), off this wave's allowlist — see the brief's own
// report and this package's Deviation comment for the exact patch shape
// (add `Parent` to submitEnvelopeProbe, append a second candidate from
// events[probe.Parent] when terminalParentTransitions[ev.Transition]).
//
// This file cannot import internal/cli (this wave's import rail: "internal/
// validate imports internal/artifact and internal/schema only") to exercise
// the real caller, so it reproduces adapters.go's own candidate-matching
// rule verbatim — `candidates = events[probe.ID]` where probe.ID is the
// DRAFT's own id, never its `parent` — and proves that rule alone can never
// deliver LFC-004's trigger, even for the exact D3 shape
// TestResidue_D3_EngineWiring (incompleteness_test.go) proves the engine CAN
// raise, given a candidate list built the other way.

// TestLFC004_DormantUnderPerDraftSubjectMatching reproduces today's only
// production caller's candidate-matching rule and proves it keeps LFC-004
// dormant: a close event whose Subject is the PARENT's id is never found by
// a lookup keyed on the response draft's OWN id, so checkResidue's
// closesParent() never sees it and ValidateForSubmit returns Valid=true for
// a response that, submitted alongside the right candidate, TestResidue_D3_
// EngineWiring proves the SAME engine refuses.
func TestLFC004_DormantUnderPerDraftSubjectMatching(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	resolver := &criteriaResolver{member: map[string]bool{"seomatrix": true}}

	const draftID = "XS-axon-20260801-cwr9"
	const parentID = "XW-axon-20260801-close1"

	raw := []byte("---\n" + `
schema: envelope/v2
id: ` + draftID + `
type: response
title: Reviewer sign-off is a draft candidate, native-TR confirmation still open
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260801-cwr9
actor: {kind: agent, name: codex}
created: "2026-08-01T10:00:00Z"
priority: p3
blocking: true
classification: internal
parent: ` + parentID + `
result: partial
unmet: [0]
blocked_by:
  reason_code: out-of-scope
  owner: seomatrix
  needs: judgement
` + "---\nBody.\n")

	// adapters.go:699's own event index: keyed by ev.Subject, over every
	// event file in the same submit batch.
	eventsBySubject := map[string]CandidateEvent{
		parentID: {Subject: parentID, Transition: "close", Actor: Actor{Kind: "agent", Name: "codex", System: "axon"}},
	}

	// adapters.go:757's own matching rule for THIS draft: `events[probe.ID]`
	// — probe.ID is the draft's own id, never `probe.Parent`. The close
	// event above is keyed by parentID, so this lookup misses it.
	var candidates []CandidateEvent
	if ev, ok := eventsBySubject[draftID]; ok {
		candidates = []CandidateEvent{ev}
	}
	if len(candidates) != 0 {
		t.Fatalf("test setup: expected the per-draft-id lookup to miss the parent-keyed close event, got %+v", candidates)
	}

	result, err := engine.ValidateForSubmit(
		Draft{Path: "axon/exchanges/" + draftID + ".md", Raw: raw},
		candidates,
		LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected the per-draft-id matching caller to stay dormant for LFC-004 (Valid=true), got violations %+v", result.Violations)
	}
}
