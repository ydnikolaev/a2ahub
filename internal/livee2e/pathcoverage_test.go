package livee2e

import (
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// pathcoverage_test.go is plan W3's coverage gate (D6): every
// fold.TransitionRows() triple must be either exercised by some declared
// Path's own Step, or listed in uncoveredTransitions with a reason that
// names WHY, honestly, from what the six families (pathcatalogue_paths.go)
// actually reach — never padded to shrink the table.
//
// UNTAGGED — runs under the plain `go test ./internal/livee2e/...`
// `make check` already executes, same as completeness.go's own gate
// (that file's doc comment: "a gate that only runs during a 40-minute
// live run is a gate nobody runs").

// uncoveredTransition pairs one (kind, from, transition) triple with the
// reason no declared path exercises it.
type uncoveredTransition struct {
	fold.TransitionKey
	reason string
}

// uncoveredClass builds one uncoveredTransition per triple in kinds×
// froms×transitions... no — see the concrete lists below. This helper
// exists so one defensible sentence covers a whole CLASS of triples
// (every requirement row; every escape-hatch `supersede`) rather than 70
// bespoke one-off strings, which both reads honestly and stays reviewable.
func uncoveredClass(reason string, triples ...fold.TransitionKey) []uncoveredTransition {
	out := make([]uncoveredTransition, 0, len(triples))
	for _, tk := range triples {
		out = append(out, uncoveredTransition{TransitionKey: tk, reason: reason})
	}
	return out
}

func tk(kind fold.Kind, from fold.State, transition string) fold.TransitionKey {
	return fold.TransitionKey{Kind: kind, From: from, Transition: transition}
}

// uncoveredTransitions is the honest gap list — seeded from what
// ConformancePaths() actually reaches (TestPathCatalogueTransitionsAreHonest
// below pins this against the real computed set), not from what would be
// convenient to claim.
//
// Read "covered" as EXERCISED, not as ASSERTED, because the two diverge and
// this gate cannot tell them apart. A triple counts as covered when some path
// step drives that transition; whether an observable predicate then judged the
// outcome is a separate question. The live divergence is `draft`: no shipped
// surface reads an uncommitted draft (`a2a show` refuses — it is built from the
// committed mirror), so every `folded_state == draft` predicate is logged and
// skipped at run time while its step still counts here. Widening the gate to
// demand an assertion per triple would be the right next move; it needs a
// predicate-level record the driver does not emit yet.
func uncoveredTransitions() []uncoveredTransition {
	var out []uncoveredTransition

	out = append(out, uncoveredClass(
		"no declared family narrates a standing requirement's lifecycle — "+
			"none of the plan's six families (contract, question, work_request, "+
			"handoff/response via the data loop) is a requirement path. A "+
			"requirement path is a real gap, not a padding candidate; forcing "+
			"one of the six existing families to cover it would misrepresent "+
			"what that family actually proves.",
		tk(fold.KindRequirement, fold.StateNone, fold.TCreate),
		tk(fold.KindRequirement, fold.StateDraft, fold.TPublish),
		tk(fold.KindRequirement, fold.StateDraft, fold.TSupersede),
		tk(fold.KindRequirement, fold.StateDraft, fold.TWithdraw),
		tk(fold.KindRequirement, fold.StatePublished, fold.TAcknowledge),
		tk(fold.KindRequirement, fold.StatePublished, fold.TDecline),
		tk(fold.KindRequirement, fold.StatePublished, fold.TSupersede),
		tk(fold.KindRequirement, fold.StatePublished, fold.TWithdraw),
		tk(fold.KindRequirement, fold.StateAcknowledged, fold.TDecline),
		tk(fold.KindRequirement, fold.StateAcknowledged, fold.TSatisfy),
		tk(fold.KindRequirement, fold.StateAcknowledged, fold.TSupersede),
		tk(fold.KindRequirement, fold.StateAcknowledged, fold.TWithdraw),
		tk(fold.KindRequirement, fold.StateSatisfied, fold.TSupersede),
		tk(fold.KindRequirement, fold.StateDeclined, fold.TSupersede),
		tk(fold.KindRequirement, fold.StateWithdrawn, fold.TSupersede),
	)...)

	out = append(out, uncoveredClass(
		"no declared family narrates a decision's propose/approve/reject "+
			"lifecycle — the same gap as requirement's, for the same reason.",
		tk(fold.KindDecision, fold.StateNone, fold.TCreate),
		tk(fold.KindDecision, fold.StateDraft, fold.TPropose),
		tk(fold.KindDecision, fold.StateProposed, fold.TApprove),
		tk(fold.KindDecision, fold.StateProposed, fold.TReject),
		tk(fold.KindDecision, fold.StateRejected, fold.TSupersede),
		tk(fold.KindDecision, fold.StateApproved, fold.TSupersede),
	)...)

	out = append(out, uncoveredClass(
		"`supersede` is an escape hatch available from almost every open "+
			"exchange/announcement state (its own author abandoning it for a "+
			"fresh one) — none of the six continuity narratives supersedes an "+
			"in-flight exchange or a still-open announcement; supersede IS "+
			"exercised exactly where a narrative calls for it (the contract's "+
			"own successor publish, and the data loop's own failed-handoff "+
			"supersede — both declared as covered, not here).",
		tk(fold.KindQuestion, fold.StateDraft, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateSubmitted, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateAcknowledged, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateInProgress, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateBlocked, fold.TSupersede),
		tk(fold.KindQuestion, fold.StateResponded, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateDraft, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateSubmitted, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateAcknowledged, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateAccepted, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateInProgress, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateBlocked, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateResponded, fold.TSupersede),
		tk(fold.KindAnnouncement, fold.StatePublished, fold.TSupersede),
	)...)

	out = append(out, uncoveredClass(
		"`decline` is the target's refusal branch; every one of the six "+
			"families narrates the target ENGAGING (acknowledge/accept/respond), "+
			"never refusing outright — a refusal path is a real gap, not "+
			"exercised by a continuity story.",
		tk(fold.KindQuestion, fold.StateSubmitted, fold.TDecline),
		tk(fold.KindQuestion, fold.StateAcknowledged, fold.TDecline),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TDecline),
		tk(fold.KindQuestion, fold.StateInProgress, fold.TDecline),
		tk(fold.KindWorkRequest, fold.StateSubmitted, fold.TDecline),
		tk(fold.KindWorkRequest, fold.StateAcknowledged, fold.TDecline),
		tk(fold.KindWorkRequest, fold.StateAccepted, fold.TDecline),
		tk(fold.KindWorkRequest, fold.StateInProgress, fold.TDecline),
	)...)

	out = append(out, uncoveredClass(
		"`cancel` is the SENDER's own abort of an exchange still in flight; "+
			"none of the six families abandons what it starts.",
		tk(fold.KindQuestion, fold.StateDraft, fold.TCancel),
		tk(fold.KindQuestion, fold.StateSubmitted, fold.TCancel),
		tk(fold.KindQuestion, fold.StateAcknowledged, fold.TCancel),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TCancel),
		tk(fold.KindQuestion, fold.StateInProgress, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateDraft, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateSubmitted, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateAcknowledged, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateAccepted, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateInProgress, fold.TCancel),
	)...)

	out = append(out, uncoveredClass(
		"the blocked side-branch (`block`/`unblock`) is a real path none of "+
			"the six families narrates — every declared exchange proceeds "+
			"without ever blocking.",
		tk(fold.KindQuestion, fold.StateAcknowledged, fold.TBlock),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TBlock),
		tk(fold.KindQuestion, fold.StateInProgress, fold.TBlock),
		tk(fold.KindQuestion, fold.StateBlocked, fold.TUnblock),
		tk(fold.KindWorkRequest, fold.StateAcknowledged, fold.TBlock),
		tk(fold.KindWorkRequest, fold.StateAccepted, fold.TBlock),
		tk(fold.KindWorkRequest, fold.StateInProgress, fold.TBlock),
		tk(fold.KindWorkRequest, fold.StateBlocked, fold.TUnblock),
	)...)

	out = append(out, uncoveredClass(
		"question-lifecycle-to-responded takes the acknowledged->respond "+
			"shortcut the pendency table itself calls optional granularity "+
			"('accept/start are optional granularity ... the respond row "+
			"admits acknowledged directly'); work-request-lifecycle-accept-"+
			"start-respond-verify-close takes the full accept->start->respond "+
			"route, but for its OWN kind — each transition row is per-Kind, so "+
			"question's own accept/start/direct-respond-from-accepted/in_progress "+
			"rows stay uncovered even though the identical SHAPE is covered for "+
			"work_request.",
		tk(fold.KindQuestion, fold.StateAcknowledged, fold.TAccept),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TStart),
		tk(fold.KindQuestion, fold.StateAccepted, fold.TRespond),
		tk(fold.KindQuestion, fold.StateInProgress, fold.TRespond),
	)...)

	out = append(out, uncoveredClass(
		"work-request-lifecycle-accept-start-respond-verify-close always "+
			"passes through `start` before responding, so it never exercises "+
			"the direct accepted->respond row; and no family disputes a "+
			"work_request's response (only question's disputed branch does, "+
			"plan Path #3) — the same asymmetry as question's own gap above, "+
			"mirrored.",
		tk(fold.KindWorkRequest, fold.StateAccepted, fold.TRespond),
		tk(fold.KindWorkRequest, fold.StateResponded, fold.TDispute),
	)...)

	out = append(out, uncoveredClass(
		"`responded -> respond -> responded` is the multi-response "+
			"reconciliation row (table.go's own comment: a second response on "+
			"an already-responded parent, the phase's documented reconciliation "+
			"with 3.4.6's multi-response allowance) — none of the six families "+
			"sends a second response to an already-answered exchange.",
		tk(fold.KindQuestion, fold.StateResponded, fold.TRespond),
		tk(fold.KindWorkRequest, fold.StateResponded, fold.TRespond),
	)...)

	return out
}

// TestPathCatalogueCoversEveryTransition is the coverage gate (plan W3
// D6): every fold.TransitionRows() triple must be reached by some
// declared Path's own Step, OR named in uncoveredTransitions() with a
// reason. An unexplained gap fails, naming the exact triples.
func TestPathCatalogueCoversEveryTransition(t *testing.T) {
	paths := ConformancePaths()
	byID, err := pathsByID(paths)
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}

	covered := map[fold.TransitionKey]string{} // triple -> path id that covers it
	for _, p := range paths {
		triples, err := PathTransitions(byID, p.ID)
		if err != nil {
			t.Fatalf("PathTransitions(%s): %v", p.ID, err)
		}
		for _, triple := range triples {
			covered[triple] = p.ID
		}
	}

	uncoveredByReason := map[fold.TransitionKey]string{}
	for _, u := range uncoveredTransitions() {
		uncoveredByReason[u.TransitionKey] = u.reason
	}

	var unexplained []string
	for _, triple := range fold.TransitionRows() {
		_, isCovered := covered[triple]
		_, isExplained := uncoveredByReason[triple]
		switch {
		case isCovered && isExplained:
			t.Errorf("triple %s/%s/%s is BOTH covered (by path %q) and listed as uncovered — remove it from uncoveredTransitions()",
				triple.Kind, triple.From, triple.Transition, covered[triple])
		case !isCovered && !isExplained:
			unexplained = append(unexplained, string(triple.Kind)+"/"+string(triple.From)+"/"+triple.Transition)
		}
	}
	if len(unexplained) > 0 {
		sort.Strings(unexplained)
		t.Fatalf("%d transition triple(s) are neither exercised by a declared path nor listed in uncoveredTransitions() with a reason:\n  %s",
			len(unexplained), joinLines(unexplained))
	}
}

// TestUncoveredTransitionsNamesOnlyRealTriples is the reverse half
// (completeness.go's own unknownDeclaredKinds precedent): an
// uncoveredTransitions() entry that names a triple fold.TransitionRows()
// does not actually have would exempt nothing — a typo or a stale entry
// left behind after a path or a table row changed. Reported so the
// failing test names both.
func TestUncoveredTransitionsNamesOnlyRealTriples(t *testing.T) {
	real := map[fold.TransitionKey]bool{}
	for _, triple := range fold.TransitionRows() {
		real[triple] = true
	}

	var stale []string
	for _, u := range uncoveredTransitions() {
		if !real[u.TransitionKey] {
			stale = append(stale, string(u.Kind)+"/"+string(u.From)+"/"+u.Transition)
		}
		if u.reason == "" {
			t.Errorf("uncoveredTransitions() entry %s/%s/%s carries an empty reason", u.Kind, u.From, u.Transition)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("uncoveredTransitions() names %d triple(s) fold.TransitionRows() does not have — stale entries exempt nothing:\n  %s",
			len(stale), joinLines(stale))
	}
}

// TestUncoveredTransitionsHasNoDuplicate guards uncoveredTransitions()
// itself: the same triple listed twice would silently double-count in
// the gate above without either check catching it.
func TestUncoveredTransitionsHasNoDuplicate(t *testing.T) {
	seen := map[fold.TransitionKey]bool{}
	for _, u := range uncoveredTransitions() {
		if seen[u.TransitionKey] {
			t.Fatalf("uncoveredTransitions() lists %s/%s/%s twice", u.Kind, u.From, u.Transition)
		}
		seen[u.TransitionKey] = true
	}
}

// TestConformancePathsHaveUniqueIDs guards pathsByID's own precondition
// (Path.ID is how Precondition/PathTransitions resolve a chain).
func TestConformancePathsHaveUniqueIDs(t *testing.T) {
	if _, err := pathsByID(ConformancePaths()); err != nil {
		t.Fatal(err)
	}
}

// TestConformancePathsResolve proves every declared path (its own
// precondition chain plus its own Steps) resolves cleanly against
// fold's own transition table — the grammar-level form of D3's "a path
// cannot assert a transition the domain does not admit".
func TestConformancePathsResolve(t *testing.T) {
	paths := ConformancePaths()
	byID, err := pathsByID(paths)
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}
	for _, p := range paths {
		if _, err := PathTransitions(byID, p.ID); err != nil {
			t.Errorf("path %q does not resolve: %v", p.ID, err)
		}
	}
}

// TestConformancePathsUseOnlyDeclaredActors is the actor teeth the brief
// asks for: a Step's Actor must be livee2e.SystemA or livee2e.SystemB
// (config.go) — never a hardcoded system name — for every declared path.
func TestConformancePathsUseOnlyDeclaredActors(t *testing.T) {
	for _, p := range ConformancePaths() {
		for i, step := range p.Steps {
			if step.Actor != SystemA && step.Actor != SystemB {
				t.Errorf("path %q step %d: actor %q is neither SystemA nor SystemB", p.ID, i, step.Actor)
			}
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}
