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
// Read "covered" here (and in TestPathCatalogueCoversEveryTransition below)
// as EXERCISED — some path step drives the transition — never as ASSERTED.
// The two used to diverge silently (a triple whose only predicate was
// skipped at run time still counted as covered); plan W3d/spec §18a closes
// that: TestPathCatalogueDrivenNotAsserted (below) reports the EXERCISED
// set's own subset whose resolved outcome no shipped surface can read back
// (today: `draft`, uniformly — see assertedTriple's own doc comment) as a
// SEPARATE, pinned list, rather than silently folding it into "covered".
// This function's own gap list is unaffected: it still names triples no
// declared path drives AT ALL.
func uncoveredTransitions() []uncoveredTransition {
	var out []uncoveredTransition

	out = append(out, uncoveredClass(
		"P11 W3c added the requirement family (requirement-lifecycle-published-"+
			"acknowledged and its four descendants), which structurally CANNOT reach "+
			"these two: `draft` is unobservable in the committed mirror for a "+
			"requirement, same root cause D-1 names generally ('a2a new' writes to "+
			"local staging only, 'a2a submit' commits the artifact already "+
			"published) — withdraw/supersede both load their target from the "+
			"mirror (lifecycleLoadEnvelope, cmd_lifecycle.go), so no CLI invocation "+
			"could ever find a requirement whose folded state is `draft` to act on. "+
			"Structurally unreachable — no path could ever drive these, unlike the "+
			"eight `create` triples spec §18a removed from the universe entirely "+
			"(W3d): those left TransitionRows() outright because their own row was "+
			"deleted, where these two stay listed because requirementRows() still "+
			"carries the (draft, withdraw)/(draft, supersede) rows — a requirement "+
			"CAN legally be withdrawn/superseded from draft, no CLI path can ever "+
			"observe one there to do it.",
		tk(fold.KindRequirement, fold.StateDraft, fold.TWithdraw),
		tk(fold.KindRequirement, fold.StateDraft, fold.TSupersede),
	)...)

	// P11 W3e (supersede family): all five requirement supersede rows above
	// (published/acknowledged/satisfied/declined/withdrawn) are now covered
	// by requirement-supersede-from-published/-acknowledged/-satisfied/
	// -declined/-withdrawn (pathcatalogue_paths.go, Family 10) — the class
	// that used to live here is gone outright, not reworded, because
	// nothing in it remains uncovered.

	out = append(out, uncoveredClass(
		"structurally unreachable, the SAME D-1 root cause the requirement pair "+
			"above names, confirmed by the mechanism that pins D-1 precisely: "+
			"postSubmissionState (fold/fold.go) returns StateSubmitted for both "+
			"KindQuestion and KindWorkRequest, never StateDraft — so even a "+
			"hypothetically committed bare envelope with zero events would fold to "+
			"`submitted`, not `draft` (fold.RestingStates() agrees: neither "+
			"{question,draft} nor {work_request,draft} is a member). `a2a new` "+
			"writes to local staging only and `a2a submit` commits the artifact "+
			"already `submitted` — no CLI invocation could ever find a question or "+
			"work_request whose folded state is `draft` to supersede. P11 W3e found "+
			"this while building the supersede family: spec §18c's own '15 live "+
			"states' count included these two, which was imprecise — every OTHER "+
			"live state in that count (submitted/acknowledged/accepted/in_progress/"+
			"blocked/responded for both kinds, plus announcement/published) is now "+
			"covered by Family 10 (pathcatalogue_paths.go).",
		tk(fold.KindQuestion, fold.StateDraft, fold.TSupersede),
		tk(fold.KindWorkRequest, fold.StateDraft, fold.TSupersede),
	)...)

	// P11 W3e (cancel + decline family): all six remaining decline triples
	// that used to live here (question submitted/accepted/in_progress,
	// work_request acknowledged/accepted/in_progress) are now covered by
	// question-declined-from-submitted/-accepted/-in-progress and
	// work-request-declined-from-acknowledged/-accepted/-in-progress
	// (pathcatalogue_paths.go, Family 12) — the class that used to live
	// here is gone outright, not reworded, because nothing in it remains
	// uncovered.

	out = append(out, uncoveredClass(
		"P11 W3e drove `cancel` from every OTHER live from-state "+
			"exchangeRows()'s cancel loop admits — submitted/acknowledged/"+
			"accepted/in_progress, both kinds (question-cancel-from-submitted/"+
			"-acknowledged/-accepted/-in-progress and work-request-cancel-from-"+
			"submitted/-acknowledged/-accepted/-in-progress, "+
			"pathcatalogue_paths.go, Family 11) — these two are the SAME D-1 "+
			"root cause the exchange draft-supersede pair above names, not a "+
			"fresh finding: `cancel` is an OP-211 generic verb exactly like "+
			"`supersede` (cmd_lifecycle.go's lifecycleVerbTable), it loads its "+
			"target via lifecycleLoadEnvelope (the committed mirror ONLY), and "+
			"postSubmissionState (fold/fold.go) returns StateSubmitted for "+
			"both KindQuestion and KindWorkRequest — never StateDraft — so "+
			"fold.RestingStates() contains neither {question,draft} nor "+
			"{work_request,draft} either: no CLI invocation could ever find a "+
			"question or work_request AT REST in `draft` to cancel. "+
			"Structurally unreachable, the identical derivation the pair "+
			"above already rests on — P11 W3e's own brief ('CANCEL (10)') "+
			"undercounted by these two, reported as a deviation.",
		tk(fold.KindQuestion, fold.StateDraft, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateDraft, fold.TCancel),
	)...)

	// P11 W3d-W3f (block/unblock family): the remaining (kind, state) pairs
	// that used to live here (question acknowledged/in_progress, work_request
	// accepted/in_progress, work_request blocked/unblock) are now covered by
	// question-block-then-unblock-restores-acknowledged/-in-progress and
	// work-request-block-then-unblock-restores-acknowledged/-accepted/-in-
	// progress (pathcatalogue_paths.go, Family 13), each pairing a real
	// `block` with a real `unblock` and asserting the SPECIFIC pre-block
	// state the dynamic row recovers — the class that used to live here is
	// gone outright, not reworded, because nothing in it remains uncovered.

	// P11 W3d-W3f (granularity family): all seven triples that used to live
	// here — question accepted/TStart, question accepted/TRespond, question
	// in_progress/TRespond, work_request accepted/TRespond, work_request
	// responded/TDispute, and the multi-response reconciliation row for both
	// kinds — are now covered by question-lifecycle-accept-start-respond,
	// question-lifecycle-accepted-respond-direct, work-request-accepted-
	// respond-direct, work-request-lifecycle-disputed-sender-owes,
	// question-multi-response-reconciliation and work-request-multi-response-
	// reconciliation (pathcatalogue_paths.go, Family 14) — the class that
	// used to live here is gone outright, not reworded, because nothing in
	// it remains uncovered.

	out = append(out, uncoveredClass(
		"P1 added these four owner-side exits so a sender is never left with no "+
			"legal move once every counterparty has left the space — the freeze "+
			"internal/fold's TestEveryLiveStateHasAnOwnerSideExit derives from "+
			"pendency.go:177's own transfer-to-sender verdict (\"the sender owes "+
			"a cancel or re-route decision instead\"), which the table could not "+
			"honour because the sender had no legal move at all. "+
			"They are deliberately NOT driven by a path yet, and the reason is "+
			"structural rather than effort: every one is departure-conditional "+
			"in practice — a proposer withdrawing a decision whose approvers "+
			"left, a producer replacing a handoff whose receiver left — and the "+
			"catalogue has no way to make a participant LEAVE mid-scenario. "+
			"Membership is manifest state, not an event a path can drive. P8 "+
			"owns the catalogue and a departure-capable scenario belongs there; "+
			"listing them here with the reason is the honest interim, and this "+
			"gate refusing to let them pass unlisted is what forced it.",
		tk(fold.KindDecision, fold.StateProposed, fold.TWithdraw),
		tk(fold.KindDecision, fold.StateProposed, fold.TSupersede),
		tk(fold.KindHandoff, fold.StateSubmitted, fold.TSupersede),
		tk(fold.KindHandoff, fold.StateAcknowledged, fold.TSupersede),
	)...)

	out = append(out, uncoveredClass(
		"agent-exchange-2026-08 P6's 2026-08-08 amendment (\"Q3 is no longer an argument, it is a "+
			"measurement\", plan 06): the producer's `supersede` escape hatch out of a disputed "+
			"response, matching decision `rejected` and handoff `rejected`'s own supersede exits "+
			"(both driven above by decision-rejected-superseded and the P1 owner-side-exit class). Not "+
			"path-driven: D-024's dispute ADDITIONALLY reopens the PARENT to in_progress, whose own "+
			"pendency row already sends the producer back through a fresh `respond` — the practical "+
			"remedy every disputed-response scenario in this catalogue already exercises "+
			"(work-request-lifecycle-disputed-sender-owes, question-lifecycle-disputed-responder-owes). "+
			"A path proving supersede specifically, rather than the respond it is an alternative to, "+
			"would need a SECOND branch off the same disputed-response precondition — authoring that "+
			"is P8's own catalogue call, not this phase's to add (same reasoning the two classes above "+
			"already apply to their own gaps).",
		tk(fold.KindResponse, fold.StateDisputed, fold.TSupersede),
	)...)

	out = append(out, uncoveredClass(
		"P1's blocked-cancel pair. Unlike the four owner-side exits above, "+
			"these are NOT departure-conditional — a path could drive them by "+
			"blocking an exchange and then cancelling it, with nobody leaving. "+
			"They are listed rather than driven because authoring a path is P8's "+
			"own deliverable and adding two here would fork the catalogue's "+
			"ownership. The distinction is recorded so P8 knows these are "+
			"ordinary work rather than blocked on a membership axis the "+
			"catalogue does not have.",
		tk(fold.KindQuestion, fold.StateBlocked, fold.TCancel),
		tk(fold.KindWorkRequest, fold.StateBlocked, fold.TCancel),
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

// assertedTriple reports whether a covered triple's own resolved outcome
// state is one some shipped `--json` surface could actually read back and
// judge — plan W3d's coverage split (spec §16 D-2, §18a). The only gap
// known today is `draft`, uniformly across every kind: checkFoldedState
// (pathdriver_live.go) carries an unconditional skip branch for
// `p.state == fold.StateDraft` and no branch for any other state, so a
// FoldedState predicate wanting `draft` is ALWAYS logged and skipped at
// run time, and every other resolved state is asserted for real (a hard
// failure, not a skip, if it does not match).
//
// This is deliberately NOT fold.RestingStates() membership, even though
// that enumerator exists precisely because of this split (spec §18a) and
// even though it would look like the natural oracle. Two reasons, both
// load-bearing, found while implementing this function rather than
// assumed going in:
//
//  1. RestingStates() answers "can Fold ever compute this (kind, state)
//     pair" — for decision, postSubmissionState(KindDecision) is
//     StateDraft (a decision committed with zero events), so
//     {decision, draft} IS in RestingStates(). But no CLI verb ever
//     commits a decision with zero events (its own entry transition is
//     `propose`, never a bare commit), and a create step's own resolved
//     state (resolveCreate, fold.NewResult) describes a STAGED,
//     uncommitted artifact regardless of kind — the same situation
//     checkFoldedState skips for every OTHER kind too. Crediting
//     decision's create step on RestingStates() membership would credit a
//     predicate the driver's own code still unconditionally skips —
//     trading the old lie (every triple "covered") for a new, narrower
//     one. Reported as a deviation rather than silently avoided.
//
//  2. RestingStates() USED TO wrongly decredit (decision, proposed,
//     approve) at quorum, and that half is now FIXED upstream — recorded
//     here rather than deleted, because it is the reason this function
//     exists and a future reader will otherwise re-derive the same doubt.
//     Both decision `approve` rows carry the fold.StateDynamic sentinel as
//     their To, so StateApproved appears as a literal `To` nowhere in
//     decisionRows(); the old enumerator skipped the sentinel outright and
//     therefore never contained {decision, approved}, even though
//     pathcatalogue_paths.go's decision-lifecycle-partial-quorum-then-
//     approved path asserts FoldedState("decision", fold.StateApproved)
//     and checkFoldedState judges it unconditionally. agent-exchange-2026-08
//     P0 gave each dynamic row an Outcomes declaration and RestingStates
//     now unions it, so {decision, approved} IS a member and the universe
//     is 44 pairs.
//
//     Reason 1 stands on its own and is why this is still not a
//     RestingStates()-based oracle. Revisiting that is a deliberate
//     decision with its own evidence, not a consequence of the fix.
func assertedTriple(to fold.State) bool {
	return to != fold.StateDraft
}

// TestPathCatalogueDrivenNotAsserted is the coverage split's own reported
// list (plan W3d: "a step whose predicates all skipped at run time...
// counts as driven, not covered, and is reported in its own list"). Built
// over the DRIVEN set (every triple some path actually exercises,
// pathTransitionOutcomes), never over fold.TransitionRows() — the eight
// `create` triples left that 93-triple universe outright when spec §18a's
// rows were removed (W3d), so a list built by walking TransitionRows()
// would find nothing to report here; this list is the other half of the
// same split, over the set TestPathCatalogueCoversEveryTransition already
// calls "covered".
//
// Pinned exactly at these eight, one `create` triple per kind (decision
// included — see assertedTriple's own doc comment for why decision is NOT
// an exception here): a regression (a triple silently added to or dropped
// from this list) is a real signal — either a newly declared step resolves
// to `draft` where none did before, or checkFoldedState grew a new skip
// branch this list has not been told about.
func TestPathCatalogueDrivenNotAsserted(t *testing.T) {
	paths := ConformancePaths()
	byID, err := pathsByID(paths)
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}

	driven := map[fold.TransitionKey]fold.State{}
	for _, p := range paths {
		outcomes, err := pathTransitionOutcomes(byID, p.ID)
		if err != nil {
			t.Fatalf("pathTransitionOutcomes(%s): %v", p.ID, err)
		}
		for _, o := range outcomes {
			driven[o.TransitionKey] = o.To
		}
	}

	var drivenNotAsserted []string
	for triple, to := range driven {
		if !assertedTriple(to) {
			drivenNotAsserted = append(drivenNotAsserted, string(triple.Kind)+"/"+string(triple.From)+"/"+triple.Transition)
		}
	}
	sort.Strings(drivenNotAsserted)

	want := []string{
		"announcement//create",
		"contract//create",
		"decision//create",
		"handoff//create",
		"question//create",
		"requirement//create",
		"response//create",
		"work_request//create",
	}
	if len(drivenNotAsserted) != len(want) {
		t.Fatalf("driven-but-not-asserted triples: got %d %v, want %d %v", len(drivenNotAsserted), drivenNotAsserted, len(want), want)
	}
	for i := range want {
		if drivenNotAsserted[i] != want[i] {
			t.Fatalf("driven-but-not-asserted[%d] = %q, want %q (full got=%v want=%v)", i, drivenNotAsserted[i], want[i], drivenNotAsserted, want)
		}
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

// TestRefusedStepDoesNotCountTowardCoverage pins consequences 2 AND 3 of a
// refused Step (pathgrammar.go's own Step.Refused doc comment) together, in
// a shape that actually EXERCISES the bug each guards against rather than
// only checking the degenerate (refused-step-is-last) position:
//
//   - consequence 3 (no coverage credit): the refused TRetire contributes
//     NO triple.
//   - consequence 2 (state resolution inverts, the walk does not advance):
//     a REAL TRetire follows the refused one, on the SAME Kind. If the
//     refused step had wrongly advanced states[KindContract] to
//     StateRetired, this second step would resolve From=StateRetired —
//     which fold has no outgoing TRetire row for (Retired is terminal) —
//     and PathTransitions would ERROR. Only a genuinely unadvanced walk
//     (From=StateDeprecated, the state the refusal left in place) lets the
//     second step resolve at all, so this shape fails loudly on a
//     regression to either consequence instead of passing vacuously.
func TestRefusedStepDoesNotCountTowardCoverage(t *testing.T) {
	base := Path{
		ID: "test-refused-coverage-base",
		Steps: []Step{
			{Actor: SystemA, Kind: fold.KindContract, Transition: fold.TCreate},
			{Actor: SystemA, Kind: fold.KindContract, Transition: fold.TPublish},
			{Actor: SystemA, Kind: fold.KindContract, Transition: fold.TDeprecate},
		},
	}
	withRefusal := Path{
		ID:           "test-refused-coverage-with-refusal",
		Precondition: base.ID,
		Steps: []Step{
			{
				// Refused — must contribute nothing and must not advance
				// the walk.
				Actor: SystemA, Kind: fold.KindContract, Transition: fold.TRetire,
				Refused: &Refusal{Code: "POL-006"},
			},
			{
				// Real — only resolves at all if the step above left
				// Contract's state at StateDeprecated, unadvanced.
				Actor: SystemA, Kind: fold.KindContract, Transition: fold.TRetire,
			},
		},
	}

	byID, err := pathsByID([]Path{base, withRefusal})
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}

	baseTriples, err := PathTransitions(byID, base.ID)
	if err != nil {
		t.Fatalf("PathTransitions(base): %v", err)
	}
	refusedTriples, err := PathTransitions(byID, withRefusal.ID)
	if err != nil {
		t.Fatalf("PathTransitions(withRefusal): %v", err)
	}

	want := fold.TransitionKey{Kind: fold.KindContract, From: fold.StateDeprecated, Transition: fold.TRetire}
	if len(refusedTriples) != 1 || refusedTriples[0] != want {
		t.Fatalf("PathTransitions(withRefusal) = %v, want exactly [%v] — the refused step must contribute nothing and the real step following it must resolve from the UNADVANCED state", refusedTriples, want)
	}

	// The covered SET (unioned across paths, TestPathCatalogueCoversEveryTransition's
	// own shape) gains only the real second step's own triple — never one
	// for the refused first step.
	withoutRefusalCovered := map[fold.TransitionKey]bool{}
	for _, triple := range baseTriples {
		withoutRefusalCovered[triple] = true
	}
	withRefusalCovered := map[fold.TransitionKey]bool{}
	for _, triple := range baseTriples {
		withRefusalCovered[triple] = true
	}
	for _, triple := range refusedTriples {
		withRefusalCovered[triple] = true
	}
	if got, want := len(withRefusalCovered), len(withoutRefusalCovered)+1; got != want {
		t.Fatalf("covered set grew by %d triple(s) after adding [refused, real] TRetire, want exactly 1 (the real step only)", got-len(withoutRefusalCovered))
	}
}

// TestRefusedStepsDeclareANonEmptyCode pins the OTHER half of a wrong-code
// refusal being a failure, not a pass: strings.Contains(combined, "") is
// vacuously true for ANY non-zero exit (MUTATION PROOF (a), the brief's own
// required proof for pathdriver_live.go's driveRefusedContractRetire) — so a
// declared Step.Refused with an empty Code would disable the check without
// touching the driver at all. Same shape as TestUncoveredTransitionsNamesOnlyRealTriples's
// own empty-reason guard and TestPathDrivabilityCoversEveryPath's own
// empty-Reason guard.
func TestRefusedStepsDeclareANonEmptyCode(t *testing.T) {
	for _, p := range ConformancePaths() {
		for i, step := range p.Steps {
			if step.Refused != nil && step.Refused.Code == "" {
				t.Errorf("path %q step %d: Refused.Code is empty — a refusal must name the exact expected code", p.ID, i)
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
