package fold

import "testing"

// The three assertions in this file are P0's enumerator correction stated
// as invariants rather than as a diff. Before it, "which rows are dynamic"
// was a pair of hardcoded transition names living in two files that
// mirrored each other by hand, and nothing checked that they agreed with
// each other or with the table. Now the property is on the row and these
// tests are what keep the three consumers honest.

// TestDynamicRowsDeclareOutcomes asserts the biconditional the whole design
// rests on: a row carries Outcomes EXACTLY when its To is the StateDynamic
// sentinel. One direction catches a dynamic row whose states would vanish
// from RestingStates; the other catches an ordinary row that would be
// silently dropped from the lookup table and flagged illegal forever.
func TestDynamicRowsDeclareOutcomes(t *testing.T) {
	t.Parallel()

	for _, r := range rows {
		dynamic := r.To == StateDynamic
		declared := len(r.Outcomes) > 0
		switch {
		case dynamic && !declared:
			t.Errorf("row %s/%s/%s has To=StateDynamic but declares no Outcomes — RestingStates and buildTable both read Outcomes, so this row's states are invisible", r.Kind, r.From, r.Transition)
		case !dynamic && declared:
			t.Errorf("row %s/%s/%s declares Outcomes %v but its To is %q, not StateDynamic — buildTable would exclude it from the lookup and every such event would flag illegal", r.Kind, r.From, r.Transition, r.Outcomes, r.To)
		}
	}
}

// TestDynamicResolversMatchDynamicRows holds the dispatch half and the
// declaration half to the same set, in both directions. A dynamic row with
// no resolver would fall through to the generic lookup and flag a legal
// event illegal; a resolver with no row would be dead dispatch shadowing a
// table entry that does exist.
func TestDynamicResolversMatchDynamicRows(t *testing.T) {
	t.Parallel()

	fromRows := make(map[dynamicKey]bool)
	for _, r := range rows {
		if len(r.Outcomes) > 0 {
			fromRows[dynamicKey{Kind: r.Kind, Transition: r.Transition}] = true
		}
	}

	for k := range fromRows {
		if _, ok := dynamicResolvers[k]; !ok {
			t.Errorf("rows declare %s/%s dynamic but dynamicResolvers has no entry — applyPrimaryScoped would fall through to the table, which excludes it, and every such event would flag illegal", k.Kind, k.Transition)
		}
	}
	for k := range dynamicResolvers {
		if !fromRows[k] {
			t.Errorf("dynamicResolvers carries %s/%s but no row declares Outcomes for it — the resolver shadows a lookup that would otherwise succeed", k.Kind, k.Transition)
		}
	}
}

// TestDeclaredOutcomesAreReachable proves every declared outcome by driving
// Apply to it, rather than by reading the table back — a declaration that
// agrees only with itself would pass a structural check and still be a
// lie. The driver set is keyed off the rows, so a dynamic row nobody can
// drive reds here instead of being quietly trusted.
func TestDeclaredOutcomesAreReachable(t *testing.T) {
	t.Parallel()

	for _, r := range rows {
		if len(r.Outcomes) == 0 {
			continue
		}
		for _, want := range r.Outcomes {
			t.Run(string(r.Kind)+"/"+r.Transition+"/"+string(want), func(t *testing.T) {
				t.Parallel()
				got, ok := driveDynamic(r, want)
				if !ok {
					t.Fatalf("no driver for dynamic row %s/%s/%s — a declared outcome nobody can reach is unproven, so add a driver or drop the declaration", r.Kind, r.From, r.Transition)
				}
				if got != want {
					t.Fatalf("driving %s/%s/%s for outcome %q produced %q", r.Kind, r.From, r.Transition, want, got)
				}
			})
		}
	}
}

// TestResponseDisputedSupersedeRowDeletedDisputeRowSurvives is spec 06's
// 2026-08-09 amendment stated as two assertions, not one: "the table must
// have no {KindResponse, StateDisputed, TSupersede} key AND
// fold.CheckLegality(KindResponse, StateSubmitted, TDispute, ...) must
// still accept the parent's owner. An implementation that deleted the wrong
// line passes the first half and fails the second." TestTransitionTable's
// generated subtest for the surviving row (fold_test.go's
// testResponseClosureRow) already drives this at the Apply level for every
// commit; this test names the AC's own two halves directly, in one place,
// so a reviewer does not have to infer the second half from a generated
// subtest's name.
func TestResponseDisputedSupersedeRowDeletedDisputeRowSurvives(t *testing.T) {
	t.Parallel()

	// Half 1: the row that gave `disputed` a supersede exit (P6,
	// 2026-08-08) is gone. Checked directly against the table, not against
	// a symptom (LFC-001 on `a2a supersede`), because the symptom was
	// already true before the row shipped, for an unrelated reason (a
	// bare `supersede` on a response id resolves the response's OWN
	// {submitted} state, which this row never touched either).
	if _, ok := transitionTable[tableKey{Kind: KindResponse, From: StateDisputed, Transition: TSupersede}]; ok {
		t.Fatal("transitionTable still carries {KindResponse, StateDisputed, TSupersede} — the row was supposed to be deleted")
	}

	// Half 2: the row directly above it — {StateSubmitted, TDispute ->
	// StateDisputed} — is what CREATES `disputed`, and it must survive.
	// verify/dispute resolve response-scoped (CheckLegality's own doc
	// comment): currentState is the response's OWN closure sub-state,
	// `env` is the PARENT's envelope, and RoleOwner authorizes the
	// PARENT's `from`.
	parentEnv := Envelope{ID: "XQ-acme-fixture", Kind: KindQuestion, From: "acme", To: []string{"beta"}}
	got := CheckLegality(KindResponse, StateSubmitted, TDispute, parentEnv, Actor{System: "acme"}, MembershipMember)
	if got != VerdictLegal {
		t.Fatalf("CheckLegality(response, submitted, dispute) for the parent's own owner = %v, want VerdictLegal — deleting the wrong row would erase `disputed` entirely, not just its exit", got)
	}
}

// driveDynamic runs the real Apply path that produces `want` for a dynamic
// row, returning false when this row's (Kind, Transition) has no driver.
func driveDynamic(r Row, want State) (State, bool) {
	env := rowEnv(r.Kind)
	switch {
	case r.Transition == TUnblock:
		// unblock recovers the state held before `block`, so the prior
		// carries it — reached here the same way applyPrimaryScoped's
		// own `block` handling records it.
		prior := Result{Kind: r.Kind, State: StateBlocked, PreBlockState: want}
		ev := Event{ULID: "01UNBLOCK000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TUnblock, Actor: Actor{System: env.To0()}}
		return Apply(r.Kind, env, prior, ev, alwaysMember).State, true

	case r.Kind == KindDecision && r.Transition == TApprove:
		// proposed until every required approver has approved; approved
		// on the last one. rowEnv requires two.
		result := Result{Kind: KindDecision, State: StateProposed}
		for i, appr := range env.RequiredApprovers {
			ev := Event{ULID: "01APPROVE0000000000000" + string(rune('1'+i)), CommitSeq: int64(i + 1), Subject: env.ID, Transition: TApprove, Actor: Actor{System: appr}}
			result = Apply(KindDecision, env, result, ev, alwaysMember)
			if result.State == want {
				return result.State, true
			}
		}
		return result.State, true
	}
	return StateNone, false
}

// TestPreWriteAgreesWithApplyOnEveryRow is the gate for the mirror that
// mattered most. CheckCandidate (the PRE-write verdict) and Apply (the
// post-write fold) used to carry the same two dynamic verb names
// separately, and the only thing keeping them in step was that nobody had
// added a third. This walks the ROWS — so the day somebody does, a
// disagreement is a red test rather than a verb that can be authored and
// not applied, or applied and not authored.
//
// The shape of the divergence is not hypothetical: legality.go's own
// comment records every `a2a ack` on an announcement being refused
// LFC-001 while the fold stood ready to apply the event, for exactly this
// reason.
func TestPreWriteAgreesWithApplyOnEveryRow(t *testing.T) {
	t.Parallel()

	for i, r := range rows {
		// Contract version transitions resolve their subject from a
		// version map rather than prior.State, so this generic harness
		// would be testing its own wrong assumption. Their pre/post
		// agreement has its own test
		// (TestCheckCandidateAgreesWithApplyOnLegality).
		if r.Kind == KindContract && isContractVersionTransition(r.Transition) {
			continue
		}
		// verify/dispute are response-scoped on BOTH sides: each routes
		// the lookup to KindResponse against the parent's Responses
		// sub-state, never to (r.Kind, prior.State). The exchange rows
		// `{question|work_request, responded, dispute -> in_progress}`
		// therefore describe D-024's SIDE EFFECT on the parent — real, and
		// reachable only by disputing an actual response — not a triple a
		// caller can drive by name. Skipped here, exercised by the
		// response cases in legality_test.go and by P8's paths.
		if r.Transition == TVerify || r.Transition == TDispute {
			continue
		}
		// no-silent-yes-2026-08/P6: the two decision-supersede rows now
		// carry a declared Precondition (table.go) CheckCandidate cannot
		// resolve without caller-supplied SuccessorFacts — and this
		// harness, like every OTHER caller of the no-facts CheckCandidate
		// wrapper (evaluate.go's EvaluateCandidate chief among them),
		// supplies none, so it refuses uniformly (D9's own rule). Apply's
		// post-write behaviour on these two rows is UNCHANGED — a
		// deliberate scope decision documented on the row itself, not an
		// oversight — so pre/post now deliberately DISAGREE here, which is
		// exactly what this test otherwise exists to forbid. Skipped for
		// the same reason contract-version rows are: a real, DOCUMENTED
		// divergence with its own coverage (legality_test.go and
		// legality_candidate_test.go's own successor-precondition cases),
		// not the silent broadcast-ack-class mirror bug this test guards
		// against for every other row.
		if r.Precondition != PreconditionNone {
			continue
		}

		t.Run(rowName(i, r), func(t *testing.T) {
			t.Parallel()
			env := rowEnv(r.Kind)
			actor := Actor{System: actorFor(r.Role, env)}

			prior := Result{Kind: r.Kind, State: r.From}
			if len(r.Outcomes) > 0 && r.Transition == TUnblock {
				// unblock recovers what `block` recorded; without it the
				// row is legal but lands nowhere, which is a different
				// question than the one this test asks.
				prior.PreBlockState = r.Outcomes[0]
			}

			verdict := CheckCandidate(r.Kind, prior, r.Transition, "", env, actor, MembershipMember)

			applyPrior := Result{Kind: r.Kind, State: r.From, PreBlockState: prior.PreBlockState}
			event := Event{ULID: "01ROWAGREE00000000000001", CommitSeq: 1, Subject: env.ID, Transition: r.Transition, Actor: actor}
			got := Apply(r.Kind, env, applyPrior, event, alwaysMember)

			preLegal := verdict == VerdictLegal
			postLegal := len(got.Flags) == 0
			if preLegal != postLegal {
				t.Fatalf("pre-write says legal=%v (%s), Apply says legal=%v (flags=%+v) for the identical row %s/%s/%s", preLegal, verdict, postLegal, got.Flags, r.Kind, r.From, r.Transition)
			}
			if !preLegal {
				t.Fatalf("row %s/%s/%s is in the table but neither side calls it legal for its own declared role %s: verdict=%s flags=%+v", r.Kind, r.From, r.Transition, r.Role, verdict, got.Flags)
			}
		})
	}
}

// TestBuildTableExcludesByPropertyNotByName is the regression this phase
// exists to remove as much as the enumerator itself: adding a THIRD dynamic
// transition used to panic the package at init, because the exclusion knew
// two verb names and the duplicate-key guard caught everything else. The
// row below is deliberately one the package does not carry.
func TestBuildTableExcludesByPropertyNotByName(t *testing.T) {
	t.Parallel()

	third := Row{
		Kind:       KindHandoff,
		From:       StateSubmitted,
		Transition: TAcknowledge,
		To:         StateDynamic,
		Role:       RoleTarget,
		Scenario:   "a third dynamic transition, hypothetical",
		Outcomes:   []State{StateAcknowledged, StateAccepted},
	}

	// Must not panic: the real handoff submitted/acknowledge row is
	// already in the table, so under the old name-keyed exclusion this
	// second one collided and killed package init.
	got := buildTableFrom(append(append([]Row(nil), rows...), third))

	key := tableKey{Kind: third.Kind, From: third.From, Transition: third.Transition}
	entry, ok := got[key]
	if !ok {
		t.Fatalf("the ordinary handoff %s/%s row vanished from the table", third.From, third.Transition)
	}
	if entry.To == StateDynamic {
		t.Fatalf("buildTableFrom indexed the dynamic row: entry.To=%q — the generic path must never see the sentinel", entry.To)
	}
}
