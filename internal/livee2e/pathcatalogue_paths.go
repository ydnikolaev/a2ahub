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
