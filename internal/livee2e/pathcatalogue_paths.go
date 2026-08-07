// pathcatalogue_paths.go declares the six path families the plan's own
// "Paths, in the order of what they would have caught" section lists
// (docs/features/active/agent-ops-2026-07/plans/11-*.plan.md, W3). DATA
// ONLY: no body, no driver — see pathgrammar.go's own package doc.
//
// Two acts the domain narrative below implies have NO fold transition at
// all, and are therefore never a Step (Step's own doc comment):
//
//   - contract adoption (`a2a contract adopt`) — 03-domain.md §10.5: "the
//     closed transition enum has no `adopt`"; adoption is a consumer
//     registry write (`consumes.yaml`), not an envelope event.
//   - an acknowledgement on a broadcast announcement — D-025's
//     transition-free per-recipient ack (fold/legalnext.go's own doc
//     comment: "acknowledge on an announcement carries NO row").
//
// Both are named in the owning path's own Intent rather than modeled.

package livee2e

import "github.com/ydnikolaev/a2ahub/internal/fold"

// ConformancePaths returns every declared path, in declaration order.
// Coverage (pathcoverage_test.go) unions PathTransitions over every
// entry here against fold.TransitionRows().
func ConformancePaths() []Path {
	var out []Path
	out = append(out, contractPaths()...)
	out = append(out, questionPaths()...)
	out = append(out, workRequestPaths()...)
	out = append(out, dataLoopPaths()...)
	out = append(out, retirementPaths()...)
	out = append(out, nonCooperativeCounterpartyPaths()...)
	out = append(out, requirementPaths()...)
	out = append(out, decisionPaths()...)
	out = append(out, supersedePaths()...)
	out = append(out, cancelPaths()...)
	out = append(out, remainingDeclinePaths()...)
	out = append(out, remainingBlockUnblockPaths()...)
	out = append(out, granularityPaths()...)
	return out
}

// --- Family 1/2 — contract draft -> published -> adopted -> settled,
// then a compatible successor version and the consumer's line (plan W3
// "Paths" #1/#2; spec §8.1's O3/O4). ---------------------------------

func contractPaths() []Path {
	return []Path{
		{
			ID: "contract-baseline-published-settled",
			Intent: "contract draft -> published; the first path deliberately has a known " +
				"answer (W1's own fix), so the system's first act proves something already " +
				"established: a published contract is pending on nobody, both on the thread's " +
				"open-items surface and the actionable surface (spec §8.1 O3/O4, watched " +
				"failing against the pre-W1 composition per the plan's phase log). " +
				"'Adopted' is a consumer registry write (`a2a contract adopt`), not a fold " +
				"transition (03-domain.md §10.5) — it is not a Step here; this path pins the " +
				"contract's own settled-ness independent of whether a consumer has adopted it.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("contract", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TPublish,
					Predicates: []Predicate{
						FoldedState("contract", fold.StatePublished),
						PendingOn("contract"), // pending on nobody — the O3/O4 assertion
						NotActionable(SystemA, "contract"),
					},
				},
			},
		},
		{
			ID:           "contract-successor-compatible-publish",
			Precondition: "contract-baseline-published-settled",
			Intent: "a second, compatible version published on the SAME contract (the " +
				"pendency table's own 'the owner MAY publish a successor' — W1's " +
				"published/nobody row) — spec §8.1's O1/O2, minus D5's own carved-out " +
				"assertion: the consumer's resolved pinned-line render " +
				"(contract-versions.md:33-37) has no shipped --json surface today (W2 " +
				"builds it, and W2 runs after W3 in the chosen order per plan §12), so this " +
				"path asserts everything EXCEPT that line, exactly as D5 requires, rather " +
				"than inventing a weaker stand-in. Compatibility itself (POL-007's shape " +
				"check) is proven by the publish succeeding at all — an incompatible " +
				"successor would refuse before this path's own predicates are ever checked.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TPublish,
					Predicates: []Predicate{
						FoldedState("contract", fold.StatePublished),
						PendingOn("contract"),
					},
				},
			},
		},
	}
}

// --- Family 3 — question submitted -> acknowledged -> responded ->
// verified -> closed, and the same disputed (plan W3 "Paths" #3). ------

func questionPaths() []Path {
	acknowledged := Path{
		ID: "question-lifecycle-acknowledged",
		Intent: "question submitted -> acknowledged, the shared prefix the two refusal " +
			"controls below (question-close-before-responded-refused, " +
			"question-respond-by-the-asker-refused) continue from — the SAME three steps " +
			"toResponded's own prefix declares below, kept as their own standalone path " +
			"rather than a partial reference into toResponded's chain (a Precondition " +
			"names another path's FULL end state, D4/Path's own doc comment — there is no " +
			"'first half of toResponded' to point at) so each refusal control reaches its " +
			"illegal moment by the SAME kind of composition every other path in this file " +
			"uses: real actors driving real acts through the real binary, never a seeded " +
			"raw prior.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	closeBeforeRespondedRefused := Path{
		ID:           "question-close-before-responded-refused",
		Precondition: acknowledged.ID,
		Intent: "the paired NEGATIVE control for the target-engages narrative every other " +
			"declared path narrates: the ASKER (sender, RoleOwner) attempts `close` while " +
			"the question sits at `acknowledged`, never responded to. `close`'s own Role is " +
			"RoleOwner (table.go) — A IS the right actor for this transition, same as A's " +
			"own already-legal create/submit steps above, so the ONLY possible refusal cause " +
			"is the STATE: exchangeRows() carries no (question, acknowledged, close) row at " +
			"all, `close` is legal ONLY from `responded` — refused illegal-transition, " +
			"LFC-001, before the write funnel is ever reached. Deliberately the mirror image " +
			"of question-respond-by-the-asker-refused below (right actor/wrong state here, " +
			"right state/wrong actor there) so the pair isolates each LFC- code cleanly. " +
			"Without this control, LFC-001's own gate could be broken open, permitting " +
			"everything, and every OTHER declared path would stay green regardless (none of " +
			"them ever watches the product REFUSE anything). The question must still be " +
			"`acknowledged` and still pending on B (the responder) afterwards — a refusal " +
			"that silently corrupted the fold would otherwise pass unnoticed.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TClose,
				Refused: &Refusal{Code: "LFC-001"},
				Predicates: []Predicate{
					FoldedState("question", fold.StateAcknowledged),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	respondByAskerRefused := Path{
		ID:           "question-respond-by-the-asker-refused",
		Precondition: acknowledged.ID,
		Intent: "the paired NEGATIVE control for `respond`'s own Role, mirroring " +
			"question-close-before-responded-refused above (that one right actor/wrong " +
			"state, this one right state/wrong actor): exchangeRows() gives `respond` Role " +
			"RoleTarget (03-domain.md §3.4.3 — respond is the TARGET's move), resolved " +
			"against the envelope's own `to:` (fold/fold.go roleAuthorizes), never the " +
			"sender. The ASKER (A, RoleOwner/env.From) attempts `respond` on its OWN " +
			"question while it sits at `acknowledged` — a state respond's own table row DOES " +
			"admit (the acknowledged->respond shortcut question-lifecycle-to-responded's own " +
			"family exercises), so this refusal is about the ACTOR, not the state. Legal-" +
			"ROLE, not legal-MEMBERSHIP, is what is on trial: legalRole (fold/fold.go) " +
			"refuses unauthorized-actor for EITHER a non-member OR a member holding the " +
			"wrong role, and both collapse to the identical LFC-002 string, so a bare pass " +
			"here would not by itself prove which one fired. This path's own precondition " +
			"(question-lifecycle-acknowledged) already has A drive `create` and `submit` " +
			"legally on this SAME question, through the SAME legality check, moments " +
			"earlier in the SAME run — A is provably a manifest member; the only variable " +
			"left standing at this step is role. State and pendency must be unchanged " +
			"afterwards — a product that let the asker answer its own question would be a " +
			"defect this control exists to surface, not to paper over.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TRespond,
				Refused: &Refusal{Code: "LFC-002"},
				Predicates: []Predicate{
					FoldedState("question", fold.StateAcknowledged),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	toResponded := Path{
		ID: "question-lifecycle-to-responded",
		Intent: "question submitted -> acknowledged -> responded, the shared prefix both " +
			"the verified-closed and disputed branches continue from (fan-out from one " +
			"precondition, D4's own replay model).",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				// One `a2a respond` invocation drives all three rows above/here:
				// the response's own create+submit (D-026, collapsed into one
				// scaffold-and-submit act) AND the parent question's own
				// `respond` event (Event.ResponseID links them).
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("question", SystemA),
					ExpectedTransition("question", fold.TClose),
				},
			},
		},
	}

	verifiedClosed := Path{
		ID:           "question-lifecycle-verified-closed",
		Precondition: toResponded.ID,
		Intent:       "the sender verifies the response, then closes the question.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindResponse, Transition: fold.TVerify,
				Predicates: []Predicate{
					FoldedState("response", fold.StateVerified),
					AbsentFromOpenItems("response"),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TClose,
				Predicates: []Predicate{
					FoldedState("question", fold.StateClosed),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
				},
			},
		},
	}

	disputed := Path{
		ID:           "question-lifecycle-disputed-responder-owes",
		Precondition: toResponded.ID,
		Intent: "the sender disputes the response instead of closing — D-024's reopen " +
			"side effect must put the owed transition back on the RESPONDER (target), not " +
			"leave it looking settled or hand it back to the sender.",
		Steps: []Step{
			{
				// responseRows() gives the response its own submitted->disputed
				// row (Role Owner, resolved against the PARENT's `from` —
				// legality.go's own documented response-scoped trap).
				Actor: SystemA, Kind: fold.KindResponse, Transition: fold.TDispute,
				Predicates: []Predicate{FoldedState("response", fold.StateDisputed)},
			},
			{
				// exchangeRows() SEPARATELY gives the parent question its own
				// responded->in_progress row (D-024: "dispute ADDITIONALLY
				// reopens the parent") — a real, distinct table row, not a
				// restatement of the one above (fold/legalnext.go's own doc
				// comment on LegalNext reporting both, unmodified).
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TDispute,
				Predicates: []Predicate{
					FoldedState("question", fold.StateInProgress),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	return []Path{acknowledged, closeBeforeRespondedRefused, respondByAskerRefused, toResponded, verifiedClosed, disputed}
}

// --- Family 4 — work_request through accept -> start -> respond ->
// verify -> close (plan W3 "Paths" #4). --------------------------------

func workRequestPaths() []Path {
	return []Path{
		{
			ID: "work-request-lifecycle-accept-start-respond-verify-close",
			Intent: "the full granularity route question-lifecycle-to-responded " +
				"deliberately skips (acknowledged -> accept -> start -> respond) rather " +
				"than the acknowledged -> respond shortcut — both are legal per the " +
				"pendency table's own 'accept/start are optional granularity' note, and " +
				"the owed transition stays `respond` throughout every one of these states.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
					Predicates: []Predicate{
						PendingOn("work-request", SystemB),
						ExpectedTransition("work-request", fold.TAcknowledge),
					},
				},
				{
					Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAcknowledge,
					Predicates: []Predicate{
						PendingOn("work-request", SystemB),
						ExpectedTransition("work-request", fold.TRespond),
					},
				},
				{
					Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
					Predicates: []Predicate{
						PendingOn("work-request", SystemB),
						ExpectedTransition("work-request", fold.TRespond),
					},
				},
				{
					Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
					Predicates: []Predicate{
						PendingOn("work-request", SystemB),
						ExpectedTransition("work-request", fold.TRespond),
					},
				},
				{
					Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
				},
				{
					Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
					Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
				},
				{
					Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
					Predicates: []Predicate{
						PendingOn("work-request", SystemA),
						ExpectedTransition("work-request", fold.TClose),
					},
				},
				{
					Actor: SystemA, Kind: fold.KindResponse, Transition: fold.TVerify,
					Predicates: []Predicate{
						FoldedState("response", fold.StateVerified),
						AbsentFromOpenItems("response"),
					},
				},
				{
					Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TClose,
					Predicates: []Predicate{
						FoldedState("work-request", fold.StateClosed),
						AbsentFromOpenItems("work-request"),
					},
				},
			},
		},
	}
}

// --- Family 5 — the data exchange loop: a failing delivery attempt
// superseded by a passing one, then the original work_request answered
// and closed (plan W3 "Paths" #5; spec 05a). ---------------------------

func dataLoopPaths() []Path {
	setup := Path{
		ID: "data-loop-contract-and-request",
		Intent: "A publishes a contract and a work_request; B acknowledges the request. " +
			"The shared setup every data-loop attempt path below continues from.",
		Steps: []Step{
			{Actor: SystemA, Kind: fold.KindContract, Transition: fold.TCreate},
			{
				Actor: SystemA, Kind: fold.KindContract, Transition: fold.TPublish,
				Predicates: []Predicate{FoldedState("contract", fold.StatePublished)},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					// Fixed: the pendency table's own row
					// ("question / work_request | submitted | target |
					// acknowledge") and the real `a2a thread --json`
					// surface both agree the owed transition right after
					// submit is `acknowledge`, not `respond` — confirmed
					// empirically the same way pathdrivability.go's own
					// (now-removed) undrivablePaths() entry for this path
					// id recorded it. This declaration contradicted the
					// pendency table; the surface agrees with the table.
					ExpectedTransition("work-request", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
		},
	}

	attemptOneFails := Path{
		ID:           "data-loop-attempt-one-fails",
		Precondition: setup.ID,
		Intent: "B packs and delivers a FAILING attempt (a record_count disagreement the " +
			"digest cannot see, spec 05a) — A verifies it fails, then B (the producer/owner " +
			"of the handoff) supersedes it. Discovered while authoring this path: " +
			"internal/cache's own openStates allowlist (types.go) does NOT include " +
			"handoff/rejected, so a rejected handoff is filtered out of open_items " +
			"entirely BEFORE pendency is ever asked — even though the pendency table " +
			"declares the producer owes `supersede` from it. AbsentFromOpenItems is the " +
			"predicate that matches what the surface actually emits today; the owed-" +
			"supersede signal being invisible on `a2a thread` is a real gap outside this " +
			"wave's allowlist (internal/cache is off limits here) and is reported rather " +
			"than silently worked around.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindHandoff, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("delivery-1", fold.StateDraft)},
			},
			{
				Actor: SystemB, Kind: fold.KindHandoff, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("delivery-1", SystemA),
					// Fixed, same defect class as setup's own (WorkRequest,
					// TSubmit) step above: the pendency table's own row
					// ("handoff | submitted | target | acknowledge") is the
					// owed transition right after submit, not verify-pass.
					ExpectedTransition("delivery-1", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindHandoff, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("delivery-1", SystemA),
					ExpectedTransition("delivery-1", fold.TVerifyPass),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindHandoff, Transition: fold.TVerifyFail,
				Predicates: []Predicate{
					FoldedState("delivery-1", fold.StateRejected),
					// The debt MOVES to the producer, it does not vanish:
					// §3.4.5 has B resubmit as a new XH superseding this one.
					//
					// This step originally declared AbsentFromOpenItems here,
					// and that was not a wrong reading of the domain — it was
					// an accurate reading of the SHIPPED BEHAVIOUR, which was
					// wrong. cache.openStates did not list handoff/rejected as
					// live, so buildOpenItems dropped the artifact before ever
					// asking pendency, and the producer was never told. The
					// path is what surfaced it (72e773c). Now that the surface
					// answers, the path asserts what the protocol actually
					// says. Left as a marker of how the two diverged, because
					// "the path matched the product" is exactly the failure
					// mode this catalogue exists to prevent.
					PendingOn("delivery-1", SystemB),
					ExpectedTransition("delivery-1", fold.TSupersede),
					// And it is A's move no longer — the receiver has ruled.
					NotActionable(SystemA, "delivery-1"),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindHandoff, Transition: fold.TSupersede,
				Predicates: []Predicate{
					// "remains visible in the record" (this brief's own
					// wording): FoldedState still resolves delivery-1
					// through `a2a show`, superseded rather than gone.
					// "stops being counted as owed" (same wording): proven
					// on open_items here; the inbox surface's own proof
					// (NotActionable(SystemA, "delivery-1")) is asserted
					// at the TVerifyFail step above, via A's own fresh
					// read right after A's own act — NOT repeated here,
					// where the acting party is B: pathdriver_live.go's
					// checkActionable reads p.system's checkout directly
					// with no sync of its own, and syncBoth only runs
					// BETWEEN driver step-groups, never mid-step, so a
					// cross-actor actionable check right after B's own
					// supersede would read A's checkout as of A's LAST
					// sync (after its own verify-fail) — the same false
					// answer either way here, which would make the
					// assertion pass without proving anything about the
					// state AFTER this step. Confirmed empirically:
					// declaring the analogous cross-actor check on
					// delivery-2's own TSubmit step (below) — checked
					// right after B's submit, before any syncBoth — fails
					// with "predicate actionable(delivery-2): got
					// present=false, want present=true", because A's
					// checkout has not synced past B's write yet. That
					// gap lives in pathdriver_live.go (Deliverable 1,
					// outside this file's own edit scope), not in the
					// product, so it is reported rather than declared
					// here as though it were checkable.
					FoldedState("delivery-1", fold.StateSuperseded),
					AbsentFromOpenItems("delivery-1"),
				},
			},
		},
	}

	attemptTwoPasses := Path{
		ID:           "data-loop-attempt-two-passes",
		Precondition: attemptOneFails.ID,
		Intent:       "B packs and delivers a SECOND attempt that supersedes the first; A verifies it passes.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindHandoff, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("delivery-2", fold.StateDraft)},
			},
			{
				Actor: SystemB, Kind: fold.KindHandoff, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("delivery-2", SystemA),
					// Fixed, same defect class as delivery-1's own
					// (Handoff, TSubmit) step: the owed transition right
					// after submit is `acknowledge`.
					ExpectedTransition("delivery-2", fold.TAcknowledge),
					// Non-accumulation (this brief's own decisive
					// assertion: the superseded first attempt must stop
					// being counted as owed) is proven on the open-items
					// surface via PendingOn/AbsentFromOpenItems across
					// this whole family, and on the actionable surface via
					// NotActionable(SystemA, "delivery-1") at
					// attemptOneFails' own TVerifyFail step (a fresh,
					// same-actor read). A POSITIVE actionable(SystemA,
					// "delivery-2") check right here — the exact analogue
					// of data_loop_test.go's own step 4
					// ("superseded -> {handoff2}") — was tried and
					// removed: checkActionable (pathdriver_live.go) reads
					// p.system's checkout with no sync of its own, and
					// syncBoth only runs BETWEEN driver step-groups, never
					// mid-step, so a cross-actor check right after B's own
					// submit (before any syncBoth) reads A's STALE
					// checkout and fails empirically with "predicate
					// actionable(delivery-2): got present=false, want
					// present=true" — a driver-ordering gap
					// (pathdriver_live.go is Deliverable 1, outside this
					// file's own edit scope), not a product defect.
				},
			},
			{
				Actor: SystemA, Kind: fold.KindHandoff, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("delivery-2", SystemA),
					ExpectedTransition("delivery-2", fold.TVerifyPass),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindHandoff, Transition: fold.TVerifyPass,
				Predicates: []Predicate{
					FoldedState("delivery-2", fold.StateAccepted),
					AbsentFromOpenItems("delivery-2"),
					NotActionable(SystemA, "delivery-2"),
				},
			},
		},
	}

	requestClosed := Path{
		ID:           "data-loop-request-answered-closed",
		Precondition: attemptTwoPasses.ID,
		Intent:       "B answers the original work_request (now that the data passed verification) and A closes it.",
		Steps: []Step{
			{Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response-to-request", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("work-request", SystemA),
					// The debt is now on A (the sender/requester),
					// matching data_loop_test.go's own step 5
					// ("responded, awaiting close -> {work_request}").
					// A POSITIVE actionable(SystemA, "work-request")
					// check right here was tried and removed for the
					// same driver-ordering reason recorded on
					// attempt-two's own TSubmit step above: this step's
					// actor is B, checkActionable reads A's checkout with
					// no sync of its own, and no syncBoth runs between
					// B's respond landing and this predicate check —
					// confirmed empirically ("predicate
					// actionable(work-request): got present=false, want
					// present=true"). PendingOn/ExpectedTransition here
					// are read via B's OWN fresh checkout instead (both
					// are checked with the acting Step's own actor, not a
					// hardcoded target system), which is why they are
					// unaffected by the same gap.
					ExpectedTransition("work-request", fold.TClose),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindResponse, Transition: fold.TVerify,
				Predicates: []Predicate{
					FoldedState("response-to-request", fold.StateVerified),
					AbsentFromOpenItems("response-to-request"),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TClose,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateClosed),
					AbsentFromOpenItems("work-request"),
					// The loop actually ends: nothing left owed on A once
					// the original work_request is closed — this brief's
					// own "debt must sit on the right party at every step"
					// assertion, closed out; matches questionPaths' own
					// verifiedClosed precedent and data_loop_test.go's own
					// final "closed -> {}" assertion.
					NotActionable(SystemA, "work-request"),
				},
			},
		},
	}

	return []Path{setup, attemptOneFails, attemptTwoPasses, requestClosed}
}

// --- Family 6 — deprecation and retirement (plan W3 "Paths" #6; W0's C1
// decision: acks AND a passed sunset). ---------------------------------

func retirementPaths() []Path {
	return []Path{
		{
			ID:           "contract-retire-refused-without-ack",
			Precondition: "contract-successor-compatible-publish",
			Intent: "the paired NEGATIVE control for contract-deprecate-retire-after-sunset " +
				"(W0's C1 decision, the one decision the operator personally made: acks AND a " +
				"passed sunset are BOTH required). Same shape as that path — A deprecates the " +
				"contract, B becomes a registered consumer (`a2a contract adopt`, no fold " +
				"transition, 03-domain.md §10.5, same as that path's own comment) — EXCEPT B " +
				"never acknowledges the deprecation announcement. A's retire attempt must then " +
				"be REFUSED naming POL-006 (internal/validate/policy_retire.go), and the " +
				"contract must still be `deprecated` afterwards: without this control, the " +
				"retire gate could be broken open, permitting everything, and " +
				"contract-deprecate-retire-after-sunset's own pass would stay green regardless.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TDeprecate,
					Predicates: []Predicate{
						FoldedState("contract", fold.StateDeprecated),
						PendingOn("contract"), // the contract itself owes nobody — the debt is the announcement's
					},
				},
				{
					Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("deprecation-notice", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TPublish,
					Predicates: []Predicate{
						FoldedState("deprecation-notice", fold.StatePublished),
						PendingOn("deprecation-notice", SystemB),
						ExpectedTransition("deprecation-notice", fold.TAcknowledge),
					},
				},
				{
					// B never acks (contrast with contract-deprecate-retire-
					// after-sunset's own sequence) — this attempt MUST be
					// refused, naming POL-006, and it performs NO transition
					// at all (Step.Refused's own doc comment): the contract
					// stays deprecated, not retired.
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TRetire,
					Refused: &Refusal{Code: "POL-006"},
					Predicates: []Predicate{
						FoldedState("contract", fold.StateDeprecated),
					},
				},
			},
		},
		{
			ID:           "contract-deprecate-retire-after-sunset",
			Precondition: "contract-successor-compatible-publish",
			Intent: "A deprecates the (now two-version) contract, which emits a deprecation " +
				"announcement (ack_requested, internal/mcp/tools_contract.go:426) addressed " +
				"to registered consumers; B acknowledges it (transition-free per D-025 — not " +
				"a Step, see this file's own package doc); A retires the contract. Retire is " +
				"legal only once BOTH acks are in AND the sunset has passed (W0's C1 " +
				"decision). This path IS driven end to end — no clock injection was needed, " +
				"because a sunset in the past is a fixed literal and needs no clock to have " +
				"passed. Read what it therefore proves, and what it does not: the sequence " +
				"EXECUTES with the preconditions satisfied, so the gate is exercised rather " +
				"than vacuously met (an `adopt` step puts a real consumer in the registry). " +
				"contract-retire-refused-without-ack (this file, same precondition) is the " +
				"paired NEGATIVE control that watches the gate REFUSE: same shape, minus the " +
				"ack, and the retire attempt MUST be refused naming POL-006.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TDeprecate,
					Predicates: []Predicate{
						FoldedState("contract", fold.StateDeprecated),
						PendingOn("contract"), // the contract itself owes nobody — the debt is the announcement's
					},
				},
				{
					Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("deprecation-notice", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TPublish,
					Predicates: []Predicate{
						FoldedState("deprecation-notice", fold.StatePublished),
						PendingOn("deprecation-notice", SystemB),
						ExpectedTransition("deprecation-notice", fold.TAcknowledge),
					},
				},
				{
					Actor: SystemA, Kind: fold.KindContract, Transition: fold.TRetire,
					Predicates: []Predicate{
						FoldedState("contract", fold.StateRetired),
						AbsentFromOpenItems("contract"),
						NotActionable(SystemA, "contract"),
					},
				},
			},
		},
	}
}

// --- Family 7 — the non-cooperative counterparty (P11 W3c): the two
// behavioural branches the system has never once seen, where the target
// does NOT engage the way every path above narrates. Every one of the
// six families above narrates the target engaging (acknowledge/accept/
// respond/verify); none refuses outright and none blocks in flight. ------

// nonCooperativeCounterpartyPaths declares the decline pair (W3c
// DELIVERABLE 1) and the block/unblock path (W3c DELIVERABLE 2).
func nonCooperativeCounterpartyPaths() []Path {
	questionDeclinedAfterAcknowledge := Path{
		ID:           "question-declined-after-acknowledge",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the TARGET declines outright after acknowledging, instead of engaging " +
			"(accept/respond) — a real, landed `decline` transition (exchangeRows(): " +
			"acknowledged -> decline -> declined, Role Target), never a refusal. " +
			"question-lifecycle-acknowledged's own last step already establishes the " +
			"paired POSITIVE control this path's own negative rests on: PendingOn(question, " +
			"SystemB) and ExpectedTransition(question, TRespond), asserted moments earlier in " +
			"the SAME run, on the SAME artifact — the target genuinely owed a move before this " +
			"step, and this step is the assertion that the debt is gone, not merely renamed. " +
			"`declined` is NOT in openExchangeStates (internal/cache/types.go's own openStates " +
			"allowlist), so the debt does not just move (contrast the data loop's own rejected-" +
			"handoff finding, where AbsentFromOpenItems on `a2a thread` hid an owed supersede) " +
			"— here AbsentFromOpenItems is the CORRECT reading: the exchange is over, full stop, " +
			"which is exactly the brief's own 'a declined that still nags somebody is exactly " +
			"the defect class this epic started from'. Checked on BOTH parties' actionable " +
			"lists, not just the declining target's: internal/pendency's table carries no row " +
			"at all for (Question, StateDeclined) — a lookup miss — so actionableReasons treats " +
			"it as an empty verdict for EVERY system, never a re-derived local fallback " +
			"(internal/cache/inbox.go's own doc comment). This is a REAL negative for A too, " +
			"not a vacuous one: questions default to `blocking: true` " +
			"(schemas/templates/v1/question.md), so actionableReasons' own condition 4 " +
			"(p1-or-blocking-open) makes A actionable for THIS SAME question while it is still " +
			"`acknowledged` (open, blocking, A is the sender) — the paired positive control for " +
			"A's own negative, distinct from B's (driveDeclineFullySync, pathdriver_live.go, " +
			"reads both checkouts only AFTER a full sync past the decline, precisely because a " +
			"stale, pre-sync read of A's checkout would still show that same true-before-decline " +
			"positive and wrongly look like a passing negative).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("question", fold.StateDeclined),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	workRequestDeclinedFromSubmitted := Path{
		ID: "work-request-declined-from-submitted",
		Intent: "the TARGET declines straight from `submitted`, WITHOUT ever acknowledging — " +
			"exchangeRows() gives decline its own row from submitted directly (Role Target), " +
			"so a target may refuse before ever conceding receipt. The paired positive control " +
			"is this path's own submit step, moments earlier: PendingOn(work-request, SystemB) " +
			"and ExpectedTransition(work-request, TAcknowledge) — the target genuinely owed " +
			"`acknowledge` (the same is-owed fact question-declined-after-acknowledge's own " +
			"acknowledge-owed control rests on, one state earlier). Same AbsentFromOpenItems / " +
			"NotActionable-on-both-parties reasoning as that path: `declined` carries no " +
			"internal/pendency row and is not in cache's own openStates allowlist for " +
			"work_request either — the exchange is over for both parties, not merely handed " +
			"back and forth. Unlike the question path's own condition-4 wrinkle " +
			"(schemas/templates/v1/question.md defaults `blocking: true`), work_request.md " +
			"defaults `blocking: false`, so this artifact is never actionable for A via " +
			"p1-or-blocking-open at any point on this path — A's own negative here is trivially " +
			"true throughout, not merely post-decline, which is why this path needs no " +
			"analogous 'the driver must read past a stale positive' caveat.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateDeclined),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	questionBlockThenUnblock := Path{
		ID:           "question-block-then-unblock-restores-accepted",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the TARGET blocks an in-flight question on a blocker, then later unblocks " +
			"it — the second real behavioural branch this catalogue has never seen. Block is " +
			"driven deliberately from `accepted` (accept first, from the inherited " +
			"`acknowledged`), not straight from acknowledged/in_progress, so unblock's own " +
			"dynamic recovery (fold.StateDynamic, table.go: 'one dynamic row per possible " +
			"pre-block state') has more than one candidate to prove it picked the RIGHT one " +
			"from — the assertion this path exists to make is that unblock restores `accepted` " +
			"specifically, NOT `acknowledged` (the state before accept) and NOT `in_progress` " +
			"(a state this path never even reaches). fold.go's applyUnblock recovers " +
			"Result.PreBlockState, recomputed from the event sequence, never a second " +
			"interpretation of the rule — pathgrammar.go's own walkSteps mirrors that exactly " +
			"at the grammar level (resolveUnblock), from THIS path's own recorded pre-block " +
			"state, so PathTransitions resolves this path without the StateDynamic refusal " +
			"ResolveStep still gives every OTHER caller. Pendency through the whole arc, per " +
			"the shipped table (internal/pendency's own StateBlocked row): the debt stays on " +
			"the TARGET while blocked (`domain 3.4.3 makes unblock the target's own event; the " +
			"referenced blocker is a separate artifact carrying its own pendency`) — asserted " +
			"here as PendingOn(question, SystemB) + ExpectedTransition(question, TUnblock) " +
			"while blocked, then PendingOn(question, SystemB) + ExpectedTransition(question, " +
			"TRespond) once unblocked and accepted-again — never AbsentFromOpenItems at any " +
			"point in this arc, because the exchange never stops being live. The blocker " +
			"itself is a throwaway local draft handoff (never submitted, so it never enters " +
			"the committed mirror) — same `--refs` pattern data-loop-attempt-one-fails already " +
			"established: fold/the CLI validate no cross-artifact existence for `--refs`.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("question", fold.StateBlocked),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TUnblock,
				Predicates: []Predicate{
					// The load-bearing assertion: restored to `accepted` — the
					// state that held IMMEDIATELY BEFORE block — not
					// `acknowledged` (before accept) and not `in_progress`
					// (never reached on this path).
					FoldedState("question", fold.StateAccepted),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	return []Path{questionDeclinedAfterAcknowledge, workRequestDeclinedFromSubmitted, questionBlockThenUnblock}
}

// --- Family 8 — requirement (P11 W3c DELIVERABLE 1): the standing
// artifact type with no path at all, and the control for the pendency fix
// spec §15 records: `requirement/acknowledged` split into BEFORE (the
// target owes work that is not a requirement transition — Owners
// non-empty, Expected "") and AFTER a fulfilling response lands (the
// REQUESTER owes `satisfy`, 03-domain.md §3.4.2's own actor column:
// "target publishes, requester verifies — satisfy event is requester's").
// The shipped table once named the TARGET as owing `satisfy` — a move
// `fold` refuses from it — and this family's first path is the control
// that would have caught it, asserting the BEFORE half explicitly rather
// than the weaker "somebody is pending".
//
// The AFTER half is NOT declared here. `hasFulfillingResponse` (internal/
// cache/inbox.go) reads fold.Result.Responses, populated ONLY by a
// fold.TRespond event landing on the SAME artifact (fold.go:174, and
// RespondCommand.Run's own doc comment: "Result.Responses is seeded to
// `submitted` entirely by the PARENT's own `respond` event ... independent
// of any response-owned event"). requirementRows() (table.go) carries no
// TRespond row for fold.KindRequirement — `a2a respond <XR-id>` therefore
// refuses illegal-transition unconditionally, for every state, and no
// other shipped verb authors a TRespond event. HasFulfillingResponse can
// never become true for a requirement through any shipped surface today.
// This is reported as a finding (this wave's own report), not worked
// around here: I9 forbids asserting a predicate no shipped surface can be
// driven to demonstrate, and the fix (a verb that attaches a response to a
// requirement) is outside this file's own edit scope (internal/fold,
// internal/cli are off limits this wave).
//
// draft is unobservable for a REQUIREMENT for the same reason it is for
// every other standing/exchange kind (spec §16: "`a2a new` writes to the
// local staging... `a2a submit` commits the artifact already
// [published]") — the create step's own FoldedState(draft) predicate is
// logged and skipped (checkFoldedState's own I9 handling), same as every
// other kind's create step in this file.
func requirementPaths() []Path {
	publishedAcknowledged := Path{
		ID: "requirement-lifecycle-published-acknowledged",
		Intent: "requirement draft -> published -> acknowledged — the shared prefix every " +
			"other declared requirement path below continues from, and the control for " +
			"spec §15's just-fixed pendency row. The BEFORE half of the acknowledged " +
			"split, asserted explicitly: PendingOn(requirement, SystemB) (the target " +
			"genuinely owes something) PLUS ExpectedTransition(requirement, \"\") (that " +
			"something is not a requirement transition — 03-domain.md §3.4.2: the target's " +
			"owed work is publishing a contract version and submitting a response, neither " +
			"of which is a move on THIS artifact). The published step's own " +
			"ExpectedTransition(requirement, TAcknowledge) is the paired positive control " +
			"the empty-string assertion needs — expected_transition is `omitempty` " +
			"(cache.OpenItem), so \"\" is also what an undecoded/absent field would read; " +
			"without the published step naming a REAL transition moments earlier on the " +
			"SAME artifact, the acknowledged step's empty read would not distinguish " +
			"'genuinely no named transition' from 'the surface silently returned nothing'.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("requirement", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TPublish,
				Predicates: []Predicate{
					PendingOn("requirement", SystemB),
					ExpectedTransition("requirement", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindRequirement, Transition: fold.TAcknowledge,
				Predicates: []Predicate{
					PendingOn("requirement", SystemB),
					// The load-bearing assertion (this family's own Intent):
					// the target owes SOMETHING (non-empty waiting_on above)
					// that is NOT a requirement transition (empty expected
					// here) — the third verdict shape spec §15 forced,
					// distinct from both "nobody owes anything" and "X owes
					// a named transition".
					ExpectedTransition("requirement", ""),
				},
			},
		},
	}

	satisfied := Path{
		ID:           "requirement-satisfied",
		Precondition: publishedAcknowledged.ID,
		Intent: "the OWNER satisfies the requirement — `satisfy` is legal from `acknowledged` " +
			"for RoleOwner at the fold layer with NO precondition on HasFulfillingResponse " +
			"(that fact gates only the PENDENCY relation's rendering, a strictly separate " +
			"mechanism from fold's own legality — table.go's requirementRows() row carries " +
			"no such gate), so this triple is coverable even though the AFTER-response " +
			"pendency row this family's own package doc reports is not. `satisfied` is not " +
			"in cache's own openStates allowlist for requirement (types.go) — settled, not " +
			"merely handed back.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSatisfy,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSatisfied),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	declinedFromPublished := Path{
		ID: "requirement-declined-from-published",
		Intent: "the TARGET declines straight from `published`, without ever acknowledging — " +
			"requirementRows() gives decline its own row from BOTH published and acknowledged " +
			"(Role Target); this path exercises the published one, a fresh standalone " +
			"instance (a `create` step always starts a genuinely new artifact, Step's own " +
			"doc comment) rather than a precondition-chained continuation, mirroring " +
			"work-request-declined-from-submitted's own shape for the exchange kinds. " +
			"`declined` is not in requirement's own openStates allowlist — settled.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("requirement", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TPublish,
				Predicates: []Predicate{
					PendingOn("requirement", SystemB),
					ExpectedTransition("requirement", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindRequirement, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateDeclined),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	declinedFromAcknowledged := Path{
		ID:           "requirement-declined-from-acknowledged",
		Precondition: publishedAcknowledged.ID,
		Intent: "the TARGET declines from `acknowledged` instead of ever fulfilling the " +
			"requirement — the second of requirementRows()'s two decline rows, continuing " +
			"from the SAME acknowledged prefix the family's own control path establishes.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindRequirement, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateDeclined),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	withdrawnFromPublished := Path{
		ID: "requirement-withdrawn-from-published",
		Intent: "the REQUESTER withdraws its own published requirement before the target " +
			"acts — `withdraw` is Role Owner (requirementRows()), the requester's own " +
			"uncooperative branch (contrast decline, the target's own). A fresh standalone " +
			"instance, same reasoning as requirement-declined-from-published.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("requirement", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TPublish,
				Predicates: []Predicate{
					PendingOn("requirement", SystemB),
					ExpectedTransition("requirement", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TWithdraw,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateWithdrawn),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	withdrawnFromAcknowledged := Path{
		ID:           "requirement-withdrawn-from-acknowledged",
		Precondition: publishedAcknowledged.ID,
		Intent: "the REQUESTER withdraws after the target has already acknowledged — " +
			"requirementRows()'s third withdraw row, continuing from the family's own " +
			"acknowledged prefix.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TWithdraw,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateWithdrawn),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	return []Path{
		publishedAcknowledged, satisfied,
		declinedFromPublished, declinedFromAcknowledged,
		withdrawnFromPublished, withdrawnFromAcknowledged,
	}
}

// --- Family 9 — decision (P11 W3c DELIVERABLE 2): propose -> approve by
// each required approver -> approved on quorum, and separately -> reject.
// `approve` is a DYNAMIC row (fold.StateDynamic — fold detects quorum);
// pathgrammar.go's own resolveApprove resolves it from THIS path's own
// declared RequiredApprovers, mirroring fold.go's applyApprove exactly. ---

func decisionPaths() []Path {
	partialQuorumThenApproved := Path{
		ID: "decision-lifecycle-partial-quorum-then-approved",
		Intent: "propose lists BOTH required approvers (draftfields.go's own " +
			"`required_approvers=[me,peer]`, RequiredApprovers below names the same pair); " +
			"B approves first — quorum 1 of 2 — and the load-bearing assertion is that the " +
			"decision is STILL `proposed` afterwards, with the REMAINING approver (A) still " +
			"owing `approve`: a decision that looked approved on the first vote would be a " +
			"serious defect and nothing else in this catalogue tests it. A approves second — " +
			"quorum 2 of 2 — and the decision flips to `approved`, terminal (decisionRows() " +
			"carries no ordinary transition out of `approved` besides the exceptional " +
			"`supersede`), owing nobody: `approved` is not in decision's own openStates " +
			"allowlist (types.go).",
		RequiredApprovers: []string{SystemA, SystemB},
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("decision", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TPropose,
				Predicates: []Predicate{
					PendingOn("decision", SystemA, SystemB),
					ExpectedTransition("decision", fold.TApprove),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindDecision, Transition: fold.TApprove,
				Predicates: []Predicate{
					// The load-bearing assertion: quorum 1 of 2 does NOT
					// approve the decision. MUTATION PROOF (this wave's own
					// report): flipping this to StateApproved must red.
					FoldedState("decision", fold.StateProposed),
					PendingOn("decision", SystemA),
					ExpectedTransition("decision", fold.TApprove),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TApprove,
				Predicates: []Predicate{
					FoldedState("decision", fold.StateApproved),
					AbsentFromOpenItems("decision"),
				},
			},
		},
	}

	approvedSuperseded := Path{
		ID:           "decision-approved-superseded",
		Precondition: partialQuorumThenApproved.ID,
		Intent: "supersede is decisionRows()'s own escape hatch from `approved` (Role Any — " +
			"fold cannot verify successor-authorship from the predecessor's own envelope " +
			"facts alone, table.go's own documented deviation).",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("decision", fold.StateSuperseded),
					AbsentFromOpenItems("decision"),
				},
			},
		},
	}

	rejected := Path{
		ID: "decision-lifecycle-rejected",
		Intent: "reject is Role Approver with NO quorum gate (decisionRows(): a single " +
			"approver's reject moves `proposed` straight to `rejected`, unlike approve's " +
			"own dynamic quorum row) — a fresh standalone instance (a `create` step always " +
			"starts a genuinely new artifact) rather than a continuation of the " +
			"partial-quorum path above, since propose/reject and propose/approve are " +
			"mutually exclusive branches from the SAME `proposed` state.",
		RequiredApprovers: []string{SystemA, SystemB},
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("decision", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TPropose,
				Predicates: []Predicate{
					PendingOn("decision", SystemA, SystemB),
					ExpectedTransition("decision", fold.TApprove),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindDecision, Transition: fold.TReject,
				Predicates: []Predicate{
					FoldedState("decision", fold.StateRejected),
					AbsentFromOpenItems("decision"),
				},
			},
		},
	}

	rejectedSuperseded := Path{
		ID:           "decision-rejected-superseded",
		Precondition: rejected.ID,
		Intent: "supersede is decisionRows()'s own escape hatch from `rejected` too (Role " +
			"Any, same documented deviation as the approved branch) — the revision is a " +
			"NEW XD superseding this one, per internal/pendency's own " +
			"`rejected: settled; the revision is a NEW XD on the thread` row.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindDecision, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("decision", fold.StateSuperseded),
					AbsentFromOpenItems("decision"),
				},
			},
		},
	}

	return []Path{partialQuorumThenApproved, approvedSuperseded, rejected, rejectedSuperseded}
}

// --- Family 10 — supersede (P11 W3e Deliverable 1): the author abandoning
// an open exchange/broadcast artifact for a fresh one (15 live states named
// by spec §18c — minus the two `draft` rows that turn out to be
// STRUCTURALLY unreachable, not merely unwritten, the same D-1 root cause
// requirementPaths' own package doc already names for requirement — see
// this family's own comment on question-supersede-from-submitted's sibling
// finding, and pathcoverage_test.go's own reworded class), and the
// requester abandoning a requirement at every one of its own reachable
// states (5, per spec §18c, `draft` again excluded).
//
// The assertion this wave's own brief names as mattering beyond the state
// change: a superseded artifact must owe NOBODY, while REMAINING VISIBLE in
// the record. `superseded` is in neither internal/cache's own openStates
// allowlist (types.go) for ANY kind this family touches, nor
// internal/pendency's own table (pendency.go carries no row keyed on
// StateSuperseded at all) — so AbsentFromOpenItems is the correct
// "owes nobody" predicate throughout (never PendingOn, which demands
// PRESENCE in open_items — a superseded artifact never has that again),
// the exact pattern decisionPaths' own approvedSuperseded/rejectedSuperseded
// already established one family up; FoldedState(..., StateSuperseded) is
// the "remains visible" half, read back through `a2a show` same as every
// other terminal state this catalogue asserts.
func supersedePaths() []Path {
	questionSupersedeFromSubmitted := Path{
		ID: "question-supersede-from-submitted",
		Intent: "the SENDER abandons its own question moments after submitting it, before " +
			"the target has even acknowledged — exchangeRows()'s supersede loop admits " +
			"`submitted` (Role Owner). A fresh standalone instance (a `create` step always " +
			"starts a genuinely new artifact), not a precondition-chained continuation: no " +
			"other declared path leaves a question resting at bare `submitted`.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	questionSupersedeFromAcknowledged := Path{
		ID:           "question-supersede-from-acknowledged",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the SENDER abandons its question once the target has acknowledged but before " +
			"any further engagement — continuing the family's own shared acknowledged prefix " +
			"(question-lifecycle-acknowledged), never a fresh instance, since that prefix " +
			"already establishes the paired positive control (PendingOn/ExpectedTransition on " +
			"the SAME artifact, moments earlier) this negative rests on.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	questionSupersedeFromAccepted := Path{
		ID:           "question-supersede-from-accepted",
		Precondition: "question-block-then-unblock-restores-accepted",
		Intent: "the SENDER abandons its question once the target has re-accepted after " +
			"unblocking — chosen over a fresh accept-only instance specifically because " +
			"question-block-then-unblock-restores-accepted already proves `accepted` here is " +
			"the RECOVERED state, not merely a state that happens to also be reachable " +
			"directly; superseding it is the same 'this artifact is genuinely done' " +
			"assertion one state further down that same arc.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	questionSupersedeFromInProgress := Path{
		ID:           "question-supersede-from-in-progress",
		Precondition: "question-lifecycle-disputed-responder-owes",
		Intent: "the SENDER abandons its question after disputing the response and reopening " +
			"it (D-024's own reopen side effect) — reusing that path's own end state rather " +
			"than a fresh accept->start route, so this triple is exercised on the SAME " +
			"in_progress a real dispute produced, not a synthetic stand-in for it.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	questionSupersedeFromBlocked := Path{
		ID:           "question-supersede-from-blocked",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the SENDER abandons its question WHILE it is still blocked, never letting " +
			"the target unblock it — proves supersede's own Role (Owner/sender) is checked " +
			"independently of who currently owes the NEXT move (the target, per the blocked " +
			"row's own PendingOn/ExpectedTransition(unblock) below): the sender may still end " +
			"the artifact outright even though it is not the sender's own turn. " +
			"(question, accepted, block) is already covered by " +
			"question-block-then-unblock-restores-accepted; reusing it here (own accept+block " +
			"steps, never unblocking) needs no new exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("question", fold.StateBlocked),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TUnblock),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	questionSupersedeFromResponded := Path{
		ID:           "question-supersede-from-responded",
		Precondition: "question-lifecycle-to-responded",
		Intent: "the SENDER abandons its question after the target has responded but before " +
			"verifying or disputing — the third live branch off `responded` (contrast " +
			"question-lifecycle-verified-closed and question-lifecycle-disputed-responder-" +
			"owes, this file's own family 3): the sender simply moves on.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("question", fold.StateSuperseded),
					AbsentFromOpenItems("question"),
				},
			},
		},
	}

	workRequestSupersedeFromSubmitted := Path{
		ID: "work-request-supersede-from-submitted",
		Intent: "the SENDER abandons its own work_request moments after submitting it, " +
			"mirroring question-supersede-from-submitted for the sibling exchange kind — " +
			"table rows are per-Kind (this file's own recurring note), so the identical " +
			"shape does not carry coverage across kinds.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	workRequestSupersedeFromAcknowledged := Path{
		ID:           "work-request-supersede-from-acknowledged",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER abandons its work_request once the target has acknowledged it — " +
			"continues the data loop's own shared setup (contract published, work_request " +
			"acknowledged) rather than a fresh instance, reusing an existing, already-driven " +
			"precondition instead of minting a redundant one.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	workRequestSupersedeFromAccepted := Path{
		ID:           "work-request-supersede-from-accepted",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER abandons its work_request once the target has accepted it but " +
			"before starting — (work_request, acknowledged, accept) is already covered by " +
			"work-request-lifecycle-accept-start-respond-verify-close; reusing it here needs " +
			"no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	workRequestSupersedeFromInProgress := Path{
		ID:           "work-request-supersede-from-in-progress",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER abandons its work_request once the target has started it — " +
			"(work_request, accepted, start) is already covered by " +
			"work-request-lifecycle-accept-start-respond-verify-close; reusing it here needs " +
			"no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	workRequestSupersedeFromBlocked := Path{
		ID:           "work-request-supersede-from-blocked",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER abandons its work_request WHILE it is still blocked — the " +
			"sibling-kind counterpart to question-supersede-from-blocked, and NEW coverage: " +
			"unlike that path's own (question, accepted, block) reuse, no declared path drove " +
			"ANY work_request block row before this one, so (work_request, acknowledged, " +
			"block) is exercised here for the first time (blocking straight from the data " +
			"loop's own acknowledged setup, the cheapest reachable block state) — " +
			"pathcoverage_test.go's own block/unblock exemption class is edited to remove it " +
			"and reworded accordingly, its remaining three rows are P11 W3e's third sibling's " +
			"own scope (block/unblock + granularity), untouched here.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateBlocked),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TUnblock),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	workRequestSupersedeFromResponded := Path{
		ID:           "work-request-supersede-from-responded",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER abandons its work_request after the target has responded but " +
			"before verifying it — (work_request, acknowledged, respond) is already covered " +
			"by data-loop-request-answered-closed; reusing it here needs no exemption-table " +
			"edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("work-request", SystemA),
					ExpectedTransition("work-request", fold.TClose),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateSuperseded),
					AbsentFromOpenItems("work-request"),
				},
			},
		},
	}

	announcementSupersedeFromPublished := Path{
		ID: "announcement-supersede-from-published",
		Intent: "the AUTHOR supersedes its own still-published announcement with a fresh one " +
			"— announcementRows()'s own second (and only other) row, Role Owner. Drafted with " +
			"no `ack_requested` field (draftFieldArgs' own default), so " +
			"internal/pendency's own unackedTargetsRow yields nobody unconditionally (domain " +
			"3.4.7: 'delivery completes on publish, no acknowledgement is required') — the " +
			"publish step's own PendingOn(announcement) with zero systems is the paired " +
			"positive control the supersede step's own AbsentFromOpenItems needs: the " +
			"announcement was genuinely PRESENT-but-owing-nobody moments earlier, then " +
			"genuinely ABSENT once superseded, never merely 'never asked'.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("announcement", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TPublish,
				Predicates: []Predicate{
					FoldedState("announcement", fold.StatePublished),
					PendingOn("announcement"), // ack_requested unset — pending on nobody
				},
			},
			{
				Actor: SystemA, Kind: fold.KindAnnouncement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("announcement", fold.StateSuperseded),
					AbsentFromOpenItems("announcement"),
				},
			},
		},
	}

	requirementSupersedeFromPublished := Path{
		ID: "requirement-supersede-from-published",
		Intent: "the REQUESTER abandons its own just-published requirement before the target " +
			"acts — requirementRows()'s supersede loop admits `published` (Role Owner). A " +
			"fresh standalone instance, same reasoning as requirement-declined-from-published.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("requirement", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TPublish,
				Predicates: []Predicate{
					PendingOn("requirement", SystemB),
					ExpectedTransition("requirement", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSuperseded),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	requirementSupersedeFromAcknowledged := Path{
		ID:           "requirement-supersede-from-acknowledged",
		Precondition: "requirement-lifecycle-published-acknowledged",
		Intent: "the REQUESTER abandons its requirement once the target has acknowledged it " +
			"but before either side moves further — continues the family's own control path " +
			"(spec §15's just-fixed pendency split) rather than a fresh instance.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSuperseded),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	// requirementSupersedeFromSatisfied chains onto requirement-satisfied,
	// which pathdrivability.go's own undrivablePaths() already parks (`a2a
	// satisfy`'s own refs validation cannot be driven through any shipped
	// surface today). Coverage credit stands regardless — PathTransitions/
	// pathTransitionOutcomes resolve the WHOLE chain purely against fold's
	// own table, never touching the real binary (requirement-satisfied's
	// own entry makes exactly this point) — but DRIVING this path for real
	// is transitively blocked by the same precondition, so it is declared
	// here for coverage and parked in undrivablePaths() too, never added to
	// drivenPathIDs()/driverForPath.
	requirementSupersedeFromSatisfied := Path{
		ID:           "requirement-supersede-from-satisfied",
		Precondition: "requirement-satisfied",
		Intent: "the REQUESTER abandons its own already-satisfied requirement for a fresh " +
			"one — requirementRows()'s supersede loop admits `satisfied` (Role Owner). " +
			"Undrivable for real (see this file's own comment immediately above and " +
			"pathdrivability.go's own undrivablePaths() entry), coverage credit stands.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSuperseded),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	requirementSupersedeFromDeclined := Path{
		ID:           "requirement-supersede-from-declined",
		Precondition: "requirement-declined-from-published",
		Intent: "the REQUESTER supersedes its own declined requirement with a fresh one — " +
			"requirementRows()'s supersede loop admits `declined` (Role Owner): a decline " +
			"does not end the REQUESTER's own ability to try again, only the target's debt.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSuperseded),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	requirementSupersedeFromWithdrawn := Path{
		ID:           "requirement-supersede-from-withdrawn",
		Precondition: "requirement-withdrawn-from-published",
		Intent: "the REQUESTER supersedes its own withdrawn requirement with a fresh one — " +
			"requirementRows()'s supersede loop admits `withdrawn` (Role Owner): the " +
			"requester's own two uncooperative-with-itself branches (withdraw, then " +
			"supersede) compose, same as decisionPaths' own rejectedSuperseded chains a " +
			"reject with a supersede.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindRequirement, Transition: fold.TSupersede,
				Predicates: []Predicate{
					FoldedState("requirement", fold.StateSuperseded),
					AbsentFromOpenItems("requirement"),
				},
			},
		},
	}

	return []Path{
		questionSupersedeFromSubmitted, questionSupersedeFromAcknowledged,
		questionSupersedeFromAccepted, questionSupersedeFromInProgress,
		questionSupersedeFromBlocked, questionSupersedeFromResponded,
		workRequestSupersedeFromSubmitted, workRequestSupersedeFromAcknowledged,
		workRequestSupersedeFromAccepted, workRequestSupersedeFromInProgress,
		workRequestSupersedeFromBlocked, workRequestSupersedeFromResponded,
		announcementSupersedeFromPublished,
		requirementSupersedeFromPublished, requirementSupersedeFromAcknowledged,
		requirementSupersedeFromSatisfied, requirementSupersedeFromDeclined,
		requirementSupersedeFromWithdrawn,
	}
}

// --- Family 11 — cancel (P11 W3e): the SENDER aborting its own in-flight
// exchange, RoleOwner, from every live from-state exchangeRows()'s own
// cancel loop admits — draft...in_progress — EXCEPT `draft` itself.
//
// `draft` is deliberately NOT declared as a path here, and that is a
// deviation from this wave's own brief ("CANCEL (10)"): the brief predates
// checking `cancel` against the SAME D-1 root cause the requirement
// withdraw/supersede-from-draft pair (pathcoverage_test.go's own first
// uncoveredClass) and the exchange draft-supersede pair (its second) both
// rest on. `cancel` is an OP-211 generic verb exactly like every other
// lifecycle verb (cmd_lifecycle.go's lifecycleVerbTable): it loads its
// target via lifecycleLoadEnvelope, the committed mirror ONLY, and
// postSubmissionState (fold/fold.go) returns StateSubmitted for both
// KindQuestion and KindWorkRequest — never StateDraft — so
// fold.RestingStates() contains neither {question,draft} nor
// {work_request,draft}: no CLI invocation could ever find a question or
// work_request AT REST in `draft` to cancel, the identical derivation the
// two pairs above already rest on. (question, draft, cancel) and
// (work_request, draft, cancel) stay in pathcoverage_test.go's own
// uncoveredTransitions(), reworded to name this rather than declared here.
//
// Every driven path below asserts the debt is gone from BOTH parties
// afterwards (the brief's own requirement) — `cancelled` carries no
// internal/pendency row and is not in cache's own openExchangeStates
// allowlist for either kind (the same lookup-miss reasoning
// question-declined-after-acknowledge already established for `declined`
// — pathcatalogue_paths.go's own nonCooperativeCounterpartyPaths), so
// AbsentFromOpenItems plus NotActionable on BOTH systems is the correct
// "genuinely over, not merely renamed" assertion throughout, never
// PendingOn (which demands presence).
func cancelPaths() []Path {
	questionCancelFromSubmitted := Path{
		ID: "question-cancel-from-submitted",
		Intent: "the SENDER cancels its own question moments after submitting it, before " +
			"the target has even acknowledged — exchangeRows()'s cancel loop admits " +
			"`submitted` (Role Owner). A fresh standalone instance, same reasoning as " +
			"question-supersede-from-submitted: no other declared path leaves a question " +
			"resting at bare `submitted`.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("question", fold.StateCancelled),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	questionCancelFromAcknowledged := Path{
		ID:           "question-cancel-from-acknowledged",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the SENDER cancels its question once the target has acknowledged but " +
			"before any further engagement — continues the family's own shared " +
			"acknowledged prefix, the SAME precondition question-supersede-from-" +
			"acknowledged reuses, so this triple is exercised on the SAME genuinely-" +
			"acknowledged artifact a real acknowledge produced.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("question", fold.StateCancelled),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	questionCancelFromAccepted := Path{
		ID:           "question-cancel-from-accepted",
		Precondition: "question-block-then-unblock-restores-accepted",
		Intent: "the SENDER cancels its question once the target has re-accepted after " +
			"unblocking — reuses the SAME recovered-accepted precondition " +
			"question-supersede-from-accepted already established, so `accepted` here " +
			"is proven the RECOVERED state, not merely a state that also happens to be " +
			"reachable directly.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("question", fold.StateCancelled),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	questionCancelFromInProgress := Path{
		ID:           "question-cancel-from-in-progress",
		Precondition: "question-lifecycle-disputed-responder-owes",
		Intent: "the SENDER cancels its question after disputing the response and " +
			"reopening it (D-024's own reopen side effect) — reusing that path's own " +
			"end state, same as question-supersede-from-in-progress, so this triple is " +
			"exercised on the SAME in_progress a real dispute produced, never a " +
			"synthetic accept->start stand-in.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("question", fold.StateCancelled),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	workRequestCancelFromSubmitted := Path{
		ID: "work-request-cancel-from-submitted",
		Intent: "the SENDER cancels its own work_request moments after submitting it, " +
			"mirroring question-cancel-from-submitted for the sibling exchange kind — " +
			"table rows are per-Kind, so the identical shape does not carry coverage " +
			"across kinds.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateCancelled),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	workRequestCancelFromAcknowledged := Path{
		ID:           "work-request-cancel-from-acknowledged",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER cancels its work_request once the target has acknowledged " +
			"it — continues the data loop's own shared setup, the SAME precondition " +
			"work-request-supersede-from-acknowledged reuses, instead of minting a " +
			"redundant one.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateCancelled),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	workRequestCancelFromAccepted := Path{
		ID:           "work-request-cancel-from-accepted",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER cancels its work_request once the target has accepted it " +
			"but before starting — (work_request, acknowledged, accept) is already " +
			"covered by work-request-lifecycle-accept-start-respond-verify-close; " +
			"reusing it here needs no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateCancelled),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	workRequestCancelFromInProgress := Path{
		ID:           "work-request-cancel-from-in-progress",
		Precondition: "data-loop-contract-and-request",
		Intent: "the SENDER cancels its work_request once the target has started it — " +
			"(work_request, accepted, start) is already covered by " +
			"work-request-lifecycle-accept-start-respond-verify-close; reusing it here " +
			"needs no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCancel,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateCancelled),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	return []Path{
		questionCancelFromSubmitted, questionCancelFromAcknowledged,
		questionCancelFromAccepted, questionCancelFromInProgress,
		workRequestCancelFromSubmitted, workRequestCancelFromAcknowledged,
		workRequestCancelFromAccepted, workRequestCancelFromInProgress,
	}
}

// --- Family 12 — decline, the remaining from-states (P11 W3e):
// nonCooperativeCounterpartyPaths (Family 7, P11 W3c) already covers
// (question, acknowledged, decline) and (work_request, submitted, decline)
// — the shared prefix the sibling driveDeclineFullySync helper and its own
// "checked on BOTH parties, after a full sync" discipline were built for.
// This family drives every OTHER (kind, state) pair exchangeRows()'s
// decline loop admits, reusing that same helper throughout rather than a
// second interpretation of it.
func remainingDeclinePaths() []Path {
	questionDeclinedFromSubmitted := Path{
		ID: "question-declined-from-submitted",
		Intent: "the TARGET declines a question straight from `submitted`, WITHOUT " +
			"ever acknowledging — mirrors work-request-declined-from-submitted for " +
			"the sibling exchange kind (table rows are per-Kind, so the identical " +
			"shape does not carry coverage across kinds). The paired positive control " +
			"is this path's own submit step, moments earlier: PendingOn(question, " +
			"SystemB) and ExpectedTransition(question, TAcknowledge). Question " +
			"defaults `blocking: true` (schemas/templates/v1/question.md), so A's own " +
			"negative here needs the SAME full-sync caveat question-declined-after-" +
			"acknowledge's own Intent names — a stale, pre-sync read of A's checkout " +
			"would still show the true-before-decline positive (p1-or-blocking-open) " +
			"and wrongly look like a passing negative.",
		Steps: []Step{
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
				Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
			},
			{
				Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TAcknowledge),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("question", fold.StateDeclined),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	questionDeclinedFromAccepted := Path{
		ID:           "question-declined-from-accepted",
		Precondition: "question-block-then-unblock-restores-accepted",
		Intent: "the TARGET declines a question once it has re-accepted after " +
			"unblocking — reuses the SAME recovered-accepted precondition " +
			"question-supersede-from-accepted and question-cancel-from-accepted " +
			"already establish. NotActionable on BOTH parties checked after a full " +
			"sync (driveDeclineFullySync's own precedent), same reasoning as this " +
			"family's other decline paths.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("question", fold.StateDeclined),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	questionDeclinedFromInProgress := Path{
		ID:           "question-declined-from-in-progress",
		Precondition: "question-lifecycle-disputed-responder-owes",
		Intent: "the TARGET declines a question after disputing the response and " +
			"reopening it — reusing that path's own end state, same as question-" +
			"supersede-from-in-progress and question-cancel-from-in-progress, so " +
			"this triple is exercised on the SAME in_progress a real dispute " +
			"produced, never a synthetic accept->start stand-in.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("question", fold.StateDeclined),
					AbsentFromOpenItems("question"),
					NotActionable(SystemA, "question"),
					NotActionable(SystemB, "question"),
				},
			},
		},
	}

	workRequestDeclinedFromAcknowledged := Path{
		ID:           "work-request-declined-from-acknowledged",
		Precondition: "data-loop-contract-and-request",
		Intent: "the TARGET declines a work_request once it has acknowledged it — " +
			"continues the data loop's own shared setup, the SAME precondition " +
			"work-request-supersede-from-acknowledged and work-request-cancel-from-" +
			"acknowledged reuse. work_request defaults `blocking: false` " +
			"(schemas/templates/v1/work_request.md), so A's own negative here is " +
			"trivially true throughout, not merely post-decline — no full-sync " +
			"staleness caveat needed, the same asymmetry work-request-declined-from-" +
			"submitted's own Intent already names.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateDeclined),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	workRequestDeclinedFromAccepted := Path{
		ID:           "work-request-declined-from-accepted",
		Precondition: "data-loop-contract-and-request",
		Intent: "the TARGET declines a work_request once it has accepted it but " +
			"before starting — (work_request, acknowledged, accept) is already " +
			"covered by work-request-lifecycle-accept-start-respond-verify-close; " +
			"reusing it here needs no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateDeclined),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	workRequestDeclinedFromInProgress := Path{
		ID:           "work-request-declined-from-in-progress",
		Precondition: "data-loop-contract-and-request",
		Intent: "the TARGET declines a work_request once it has started it — " +
			"(work_request, accepted, start) is already covered by " +
			"work-request-lifecycle-accept-start-respond-verify-close; reusing it " +
			"here needs no exemption-table edit.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TDecline,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateDeclined),
					AbsentFromOpenItems("work-request"),
					NotActionable(SystemA, "work-request"),
					NotActionable(SystemB, "work-request"),
				},
			},
		},
	}

	return []Path{
		questionDeclinedFromSubmitted, questionDeclinedFromAccepted, questionDeclinedFromInProgress,
		workRequestDeclinedFromAcknowledged, workRequestDeclinedFromAccepted, workRequestDeclinedFromInProgress,
	}
}

// --- Family 13 — block/unblock, the remaining from-states (P11 W3d-W3f):
// question-block-then-unblock-restores-accepted (Family 7) already proves
// the mechanism once, from `accepted`; work-request-supersede-from-blocked
// (Family 10) already drives (work_request, acknowledged, block) as a side
// effect of reaching `blocked` to supersede from there, but NEVER unblocks —
// it supersedes instead, so it proves nothing about dynamic recovery. Every
// path here both blocks AND unblocks the SAME artifact, in its OWN Steps
// (never split across a Precondition boundary — pathgrammar.go's own
// resolveUnblock doc comment: pre-block state is walk-local, "a path may
// only unblock a Kind it blocked in its OWN Steps"), and each asserts the
// SPECIFIC pre-block state comes back — the whole point of the dynamic row
// (table.go: "one dynamic row per possible pre-block state").
func remainingBlockUnblockPaths() []Path {
	questionBlockThenUnblockRestoresAcknowledged := Path{
		ID:           "question-block-then-unblock-restores-acknowledged",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the TARGET blocks the question straight from `acknowledged` (never accepting " +
			"first), then unblocks it — the load-bearing assertion is that unblock restores " +
			"`acknowledged` specifically, the state that held immediately before block, matching " +
			"m[key{k, StateAcknowledged}]'s own owed transition (respond) once recovered. Pendency " +
			"through the arc: PendingOn(question, SystemB) + ExpectedTransition(question, TUnblock) " +
			"while blocked (internal/pendency's own StateBlocked row: the debt stays on the " +
			"target), then PendingOn(question, SystemB) + ExpectedTransition(question, TRespond) " +
			"once restored. The blocker is a throwaway local draft handoff, same `--refs` pattern " +
			"question-block-then-unblock-restores-accepted already established.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("question", fold.StateBlocked),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TUnblock,
				Predicates: []Predicate{
					// Restored to `acknowledged` — the state IMMEDIATELY
					// BEFORE block, not `accepted` (a state this path never
					// reaches) and not `in_progress`.
					FoldedState("question", fold.StateAcknowledged),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	questionBlockThenUnblockRestoresInProgress := Path{
		ID:           "question-block-then-unblock-restores-in-progress",
		Precondition: "question-lifecycle-disputed-responder-owes",
		Intent: "the TARGET blocks the question after a dispute reopened it (D-024's own side " +
			"effect leaves it `in_progress`), then unblocks it — the load-bearing assertion is " +
			"that unblock restores `in_progress` specifically, the THIRD distinct pre-block state " +
			"this catalogue now proves the dynamic row picks correctly (accepted: question-block-" +
			"then-unblock-restores-accepted; acknowledged: the sibling path immediately above; " +
			"in_progress: here) — not `acknowledged` and not `accepted`, neither of which this " +
			"path's own artifact ever revisits once disputed. Pendency: PendingOn(question, " +
			"SystemB) + ExpectedTransition(question, TUnblock) while blocked, then PendingOn " +
			"(question, SystemB) + ExpectedTransition(question, TRespond) once restored — the " +
			"same shape every state in this family shares (internal/pendency's own StateBlocked " +
			"row, then StateInProgress's own targetRow(TRespond, \"same, in flight\")).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("question", fold.StateBlocked),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TUnblock,
				Predicates: []Predicate{
					FoldedState("question", fold.StateInProgress),
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
		},
	}

	workRequestBlockThenUnblockRestoresAcknowledged := Path{
		ID:           "work-request-block-then-unblock-restores-acknowledged",
		Precondition: "data-loop-contract-and-request",
		Intent: "the sibling-kind counterpart to question-block-then-unblock-restores-" +
			"acknowledged: the TARGET blocks the work_request straight from `acknowledged`, then " +
			"unblocks it. (work_request, acknowledged, block) is ALREADY exercised by work-" +
			"request-supersede-from-blocked (Family 10), but that path never unblocks — it " +
			"supersedes the blocked artifact instead, proving nothing about dynamic recovery. " +
			"This path is the one that actually proves the recovery for work_request's own " +
			"acknowledged pre-block state, table rows being per-Kind (this file's own recurring " +
			"note — the question-side proof does not carry over).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateBlocked),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TUnblock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateAcknowledged),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
		},
	}

	workRequestBlockThenUnblockRestoresAccepted := Path{
		ID:           "work-request-block-then-unblock-restores-accepted",
		Precondition: "data-loop-contract-and-request",
		Intent: "the TARGET accepts the work_request, blocks it from `accepted`, then unblocks " +
			"it — the recovery must land back on `accepted`, not `acknowledged` (the state before " +
			"accept, which this path never revisits) and not `in_progress` (never reached here). " +
			"New coverage: (work_request, accepted, block) is driven for the first time by ANY " +
			"declared path.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateBlocked),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TUnblock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateAccepted),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
		},
	}

	workRequestBlockThenUnblockRestoresInProgress := Path{
		ID:           "work-request-block-then-unblock-restores-in-progress",
		Precondition: "data-loop-contract-and-request",
		Intent: "the TARGET accepts, starts, blocks the work_request from `in_progress`, then " +
			"unblocks it — the recovery must land back on `in_progress` specifically, the THIRD " +
			"and last work_request pre-block state this family proves (acknowledged: the sibling " +
			"path above; accepted: work-request-block-then-unblock-restores-accepted; in_progress: " +
			"here), never `acknowledged` or `accepted`. This is ALSO the path that closes " +
			"(work_request, blocked, unblock) — coverage-wise redundant with either sibling above " +
			"(the triple carries no per-scenario distinction, table.go's own comment: \"one " +
			"dynamic row per possible pre-block state, for test-scenario documentation\"), listed " +
			"here for completeness of the arc rather than for a triple none of the siblings " +
			"already close.",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TBlock,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateBlocked),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TUnblock),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TUnblock,
				Predicates: []Predicate{
					// MUTATION PROOF (this wave's own report): flipping this
					// to StateAccepted must red, naming both got and want.
					FoldedState("work-request", fold.StateInProgress),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
		},
	}

	return []Path{
		questionBlockThenUnblockRestoresAcknowledged, questionBlockThenUnblockRestoresInProgress,
		workRequestBlockThenUnblockRestoresAcknowledged, workRequestBlockThenUnblockRestoresAccepted,
		workRequestBlockThenUnblockRestoresInProgress,
	}
}

// --- Family 14 — granularity variants (P11 W3d-W3f, spec §18c's own "7"):
// the direct accepted->respond row work-request-lifecycle-accept-start-
// respond-verify-close skips by always passing through `start`; question's
// own OWN full accept->start->respond route (never previously driven —
// question-lifecycle-to-responded takes the acknowledged->respond shortcut
// instead); a work_request response being disputed (only question's own
// disputed branch existed before); and the multi-response reconciliation
// row (`responded -> respond -> responded`, table.go's own comment: "one
// parent MAY receive multiple responses", 3.4.6) for BOTH kinds.
func granularityPaths() []Path {
	questionAcceptStartRespond := Path{
		ID:           "question-lifecycle-accept-start-respond",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the full granularity route for QUESTION (accept -> start -> respond) that no " +
			"declared question path drives — question-lifecycle-to-responded takes the " +
			"acknowledged->respond shortcut instead (the pendency table's own 'accept/start are " +
			"optional granularity' note), and question-block-then-unblock-restores-accepted " +
			"drives accept but never start. Covers (question, accepted, start) — driven for the " +
			"first time by any declared path — and (question, in_progress, respond), the third " +
			"live respond fromState for question (accepted and acknowledged are covered " +
			"elsewhere; in_progress only by mirroring work_request's own shape here).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("question", SystemA),
					ExpectedTransition("question", fold.TClose),
				},
			},
		},
	}

	questionAcceptedRespondDirect := Path{
		ID:           "question-lifecycle-accepted-respond-direct",
		Precondition: "question-lifecycle-acknowledged",
		Intent: "the TARGET accepts, then responds DIRECTLY from `accepted`, skipping `start` — " +
			"the sibling granularity shortcut to questionAcceptStartRespond's own full route: " +
			"exchangeRows()'s respond loop admits `accepted` directly (Role Target), same as it " +
			"admits `acknowledged` and `in_progress`. Covers (question, accepted, respond).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("question", SystemB),
					ExpectedTransition("question", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("question", SystemA),
					ExpectedTransition("question", fold.TClose),
				},
			},
		},
	}

	workRequestAcceptedRespondDirect := Path{
		ID:           "work-request-accepted-respond-direct",
		Precondition: "data-loop-contract-and-request",
		Intent: "the sibling-kind counterpart to question-lifecycle-accepted-respond-direct: the " +
			"TARGET accepts the work_request, then responds DIRECTLY from `accepted`, skipping " +
			"`start` — the ONE row work-request-lifecycle-accept-start-respond-verify-close never " +
			"reaches, because that path always passes through `start` before responding. Covers " +
			"(work_request, accepted, respond).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("work-request", SystemA),
					ExpectedTransition("work-request", fold.TClose),
				},
			},
		},
	}

	workRequestDisputedSenderOwes := Path{
		ID:           "work-request-lifecycle-disputed-sender-owes",
		Precondition: "data-loop-contract-and-request",
		Intent: "the sibling-kind counterpart to question-lifecycle-disputed-responder-owes: the " +
			"SENDER disputes the response instead of closing — D-024's reopen side effect must " +
			"put the owed transition back on the RESPONDER (target), not leave it looking settled " +
			"or hand it back to the sender, same assertion as the question family's own disputed " +
			"path, exercised here for work_request for the first time (table rows are per-Kind, " +
			"so the question-side proof does not carry over). Covers (work_request, responded, " +
			"dispute).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("work-request", SystemA),
					ExpectedTransition("work-request", fold.TClose),
				},
			},
			{
				// responseRows() gives the response its own submitted->disputed
				// row (Role Owner, resolved against the PARENT's `from`).
				Actor: SystemA, Kind: fold.KindResponse, Transition: fold.TDispute,
				Predicates: []Predicate{FoldedState("response", fold.StateDisputed)},
			},
			{
				// exchangeRows() SEPARATELY gives the parent work_request its
				// own responded->in_progress row (D-024's reopen side effect).
				Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TDispute,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateInProgress),
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
		},
	}

	questionMultiResponseReconciliation := Path{
		ID:           "question-multi-response-reconciliation",
		Precondition: "question-lifecycle-to-responded",
		Intent: "the multi-response reconciliation row (table.go's own comment: a second " +
			"response on an already-responded parent, 3.4.6's 'one parent MAY receive multiple " +
			"responses' allowance) — the TARGET sends a SECOND, genuinely distinct response " +
			"(a distinguishing --field, since responseID is content-derived — cmd_lifecycle.go's " +
			"own HIGH-1 fix-wave doc comment: identical inputs would mint the IDENTICAL id and " +
			"collapse onto the first response's own dedup branch instead of authoring a real " +
			"second one, TestVerifyMultiResponseDoesNotAutoClose's own precedent) while the " +
			"FIRST response still sits unverified. The load-bearing assertion this path exists " +
			"for: whose move it is afterwards. internal/pendency's own row is keyed on the " +
			"PARENT's folded state alone (m[key{k, StateResponded}] = senderRow(TClose, ...)), " +
			"never on how many responses are attached — so the second response changes NOTHING " +
			"about the parent's own owed transition: still the SENDER, still `close` (by way of " +
			"verifying each response, P-d's own 'sender owes verification then close'), exactly " +
			"as it was after the FIRST response landed (toResponded's own last step, moments " +
			"earlier in the SAME run). Covers (question, responded, respond).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response-2", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindQuestion, Transition: fold.TRespond,
				Predicates: []Predicate{
					// Unchanged from the first response's own assertion —
					// the second response does not hand the move to anybody
					// new, and does not settle the thread either.
					FoldedState("question", fold.StateResponded),
					PendingOn("question", SystemA),
					ExpectedTransition("question", fold.TClose),
				},
			},
		},
	}

	workRequestMultiResponseReconciliation := Path{
		ID:           "work-request-multi-response-reconciliation",
		Precondition: "data-loop-contract-and-request",
		Intent: "the sibling-kind counterpart to question-multi-response-reconciliation, driven " +
			"fresh (no declared work_request path leaves one merely `responded` without also " +
			"closing it in the same chain — data-loop-request-answered-closed auto-closes in one " +
			"call, D-024's own convenience). Same reasoning throughout: the second response, " +
			"distinguished the same way (a distinct --field), changes nothing about the parent's " +
			"own owed transition. Covers (work_request, responded, respond).",
		Steps: []Step{
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TAccept,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TStart,
				Predicates: []Predicate{
					PendingOn("work-request", SystemB),
					ExpectedTransition("work-request", fold.TRespond),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					PendingOn("work-request", SystemA),
					ExpectedTransition("work-request", fold.TClose),
				},
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TCreate,
			},
			{
				Actor: SystemB, Kind: fold.KindResponse, Transition: fold.TSubmit,
				Predicates: []Predicate{FoldedState("response-2", fold.StateSubmitted)},
			},
			{
				Actor: SystemB, Kind: fold.KindWorkRequest, Transition: fold.TRespond,
				Predicates: []Predicate{
					FoldedState("work-request", fold.StateResponded),
					PendingOn("work-request", SystemA),
					ExpectedTransition("work-request", fold.TClose),
				},
			},
		},
	}

	return []Path{
		questionAcceptStartRespond, questionAcceptedRespondDirect,
		workRequestAcceptedRespondDirect, workRequestDisputedSenderOwes,
		questionMultiResponseReconciliation, workRequestMultiResponseReconciliation,
	}
}
