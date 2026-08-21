package livee2e

// pathdrivability.go is the drivability registry plan W3's driver
// (pathdriver_live.go, //go:build livee2e) is checked against: which of
// pathcatalogue_paths.go's declared ConformancePaths() ids the driver
// actually executes against the real `a2a` binary, one t.Run subtest per
// id, and — for any id it does not — an honest, specific reason.
//
// Same shape as pathcoverage_test.go's own uncoveredTransitions(): a gap is
// DECLARED, never silent. This file is deliberately UNTAGGED so the union
// gate in pathdrivability_test.go runs under the plain
// `go test ./internal/livee2e/...` suite `make check` already executes on
// every commit — not only under a 20-minute `-tags=livee2e` run nobody
// triggers by hand. An untagged file cannot reference the tagged driver's
// own symbols (same split branchmatch.go/harness_live.go already draws), so
// this registry is the one place both sides agree on the id list; drift
// between "declared driven here" and "actually driven by
// runConformancePaths" would otherwise only surface as a live-tagged test
// failure, 20 minutes into a run nobody is watching.

// undrivablePath pairs one ConformancePaths() id with the reason
// pathdriver_live.go's runConformancePaths does not drive it for real.
type undrivablePath struct {
	ID     string
	Reason string
}

// drivenPathIDs is every ConformancePaths() id runConformancePaths
// (pathdriver_live.go) actually drives through the real binary, one t.Run
// subtest per id, nested under a `group-N` subtest — so
// `-run '^TestLogicMatrix$/^group-[0-9]+$/^<path-id>$'` runs exactly that
// path and nothing else (plus TestLogicMatrix's own family matrix — see
// runConformancePaths' own doc comment for that residual). Omitting the
// group segment matches nothing and still exits 0.
func drivenPathIDs() []string {
	out := []string{
		"contract-baseline-published-settled",
		"contract-successor-compatible-publish",
		"question-lifecycle-acknowledged",
		"question-close-before-responded-refused",
		"question-respond-by-the-asker-refused",
		"question-lifecycle-to-responded",
		"question-lifecycle-verified-closed",
		"question-lifecycle-disputed-responder-owes",
		"work-request-lifecycle-accept-start-respond-verify-close",
		"contract-deprecate-retire-after-sunset",
		"contract-retire-refused-without-ack",
		"data-loop-contract-and-request",
		"data-loop-attempt-one-fails",
		"data-loop-attempt-two-passes",
		"data-loop-request-answered-closed",
		// judge-the-thing-2026-08 P1: the refusal that answers
		// fb-20260808-d5740f is driven through the real binary here, not
		// by a unit test alone — the epic's AC2. Its own family's plain
		// `delivered` paths above are the oracle it must not disturb.
		"response-delivers-unlanded-package-refused",
		"question-declined-after-acknowledge",
		"work-request-declined-from-submitted",
		"question-block-then-unblock-restores-accepted",
		"requirement-lifecycle-published-acknowledged",
		"requirement-declined-from-published",
		"requirement-declined-from-acknowledged",
		"requirement-withdrawn-from-published",
		"requirement-withdrawn-from-acknowledged",
		"decision-lifecycle-partial-quorum-then-approved",
		"decision-approved-superseded",
		// P8's resting-state gate named {decision, withdrawn} as entered by
		// no path — added by P1 and never driven. It rides the SAME generic
		// per-kind verb dispatch its siblings above already use
		// (pathdriver_live.go's verbNames maps TWithdraw for every kind), so
		// it needs no new driver leg. The tagged matrix is what proves that
		// rather than the argument.
		//
		// The gate also once named {response, superseded} here — P6 added
		// it, and it never had a real driven path (response-disputed-
		// superseded lived in undrivablePaths below instead). 2026-08-09
		// deleted the fold row that produced that resting state entirely
		// (spec 06's amendment, epic-backlog B8), so the state itself is
		// gone from fold.RestingStates() and there is nothing left to name
		// here.
		"decision-proposed-withdrawn",
		"decision-lifecycle-rejected",
		"decision-rejected-superseded",
		"question-supersede-from-submitted",
		"question-supersede-from-acknowledged",
		"question-supersede-from-accepted",
		"question-supersede-from-in-progress",
		"question-supersede-from-blocked",
		"question-supersede-from-responded",
		"work-request-supersede-from-submitted",
		"work-request-supersede-from-acknowledged",
		"work-request-supersede-from-accepted",
		"work-request-supersede-from-in-progress",
		"work-request-supersede-from-blocked",
		"work-request-supersede-from-responded",
		"announcement-supersede-from-published",
		"requirement-supersede-from-published",
		"requirement-supersede-from-acknowledged",
		"requirement-supersede-from-declined",
		"requirement-supersede-from-withdrawn",
		"question-cancel-from-submitted",
		"question-cancel-from-acknowledged",
		"question-cancel-from-accepted",
		"question-cancel-from-in-progress",
		"work-request-cancel-from-submitted",
		"work-request-cancel-from-acknowledged",
		"work-request-cancel-from-accepted",
		"work-request-cancel-from-in-progress",
		"question-declined-from-submitted",
		"question-declined-from-accepted",
		"question-declined-from-in-progress",
		"work-request-declined-from-acknowledged",
		"work-request-declined-from-accepted",
		"work-request-declined-from-in-progress",
		"question-block-then-unblock-restores-acknowledged",
		"question-block-then-unblock-restores-in-progress",
		"work-request-block-then-unblock-restores-acknowledged",
		"work-request-block-then-unblock-restores-accepted",
		"work-request-block-then-unblock-restores-in-progress",
		"question-lifecycle-accept-start-respond",
		"question-lifecycle-accepted-respond-direct",
		"work-request-accepted-respond-direct",
		"work-request-lifecycle-disputed-sender-owes",
		"question-multi-response-reconciliation",
		"work-request-multi-response-reconciliation",
	}
	out = append(out, departedCounterpartyPathIDs()...)
	return out
}

// departedCounterpartyPathIDs is Family 15's own three ids
// (pathcatalogue_paths.go) — genuinely driven, one t.Run subtest per id
// exactly like every other drivenPathIDs() entry (pathdriver_live.go's
// runDepartedCounterpartyPaths, driverForPath), but NEVER through
// runConformancePaths' own round-robin split across the ordinary path-space
// harnesses: each driver departs h.B MID-PATH (provision_live.go's
// SetParticipantStatusMidPath — P8 wave 31, correcting wave 30B's own
// genesis-timed attempt, see pathcatalogue_paths.go's own Family 15 doc
// comment), and that mutation is just as irreversible per call as a
// genesis one would have been (no `a2a` CLI verb writes this field, in
// either direction, and re-provisioning mid-group would erase every other
// path's own already-committed artifacts), so mixing these three into the
// ordinary split would still silently depart a counterparty every OTHER
// path in that harness's group assumes is active. Kept as its own named
// function, not inlined into drivenPathIDs()'s own literal, so
// runConformancePaths (pathdriver_live.go) can subtract exactly this set
// before splitting, without the two lists drifting apart.
func departedCounterpartyPathIDs() []string {
	return []string{
		"decision-proposed-withdrawn-by-author-after-approvers-left",
		"decision-proposed-superseded-by-author-after-approvers-left",
		"handoff-submitted-superseded-by-producer-after-receiver-left",
	}
}

// undrivablePaths is the honest gap list: a declared path this wave's
// driver does NOT execute for real, with the exact reason discovered while
// building it — never padded to shrink the table (pathcoverage_test.go's
// own precedent for this shape).
func undrivablePaths() []undrivablePath {
	return []undrivablePath{
		{
			ID: "requirement-satisfied",
			Reason: "`a2a satisfy <XR-id> --refs <XC-id>@<version>,<XS-id>` validates its own " +
				"refs (lifecycleValidateSatisfyRefs, cmd_lifecycle.go): the XS ref must resolve " +
				"to a REAL, committed response whose `parent` is this requirement AND whose " +
				"folded state reaches `verified`. Building that response is only possible via " +
				"a plain `a2a new response --field parent=<XR-id>` + `a2a submit` (never `a2a " +
				"respond`, which refuses unconditionally for a requirement — requirementRows() " +
				"carries no TRespond row). " +
				"HISTORY, KEPT DELIBERATELY: this entry originally rested on two FURTHER " +
				"blockers, and BOTH HAVE SINCE BEEN FIXED — by the very wave that discovered " +
				"them. (1) The response's own `submit` used to be folded a second time under " +
				"the REQUIREMENT's subject, flagging illegal-transition permanently on its " +
				"thread; gatherEvents now lets ONLY verify/dispute cross from a response into " +
				"its parent's stream (mirror.go, pinned by " +
				"TestOnlyResponseScopedEventsReachTheParent). (2) HasFulfillingResponse used to " +
				"read fold's Result.Responses, permanently empty for a requirement; it now " +
				"reads mirror.go's own `parent` pass. Leaving those two written here as live " +
				"blockers is how a fixed bug gets re-fixed, so they are recorded as closed " +
				"rather than deleted. " +
				"WHAT IS ACTUALLY OPEN, and it is one thing: lifecycleValidateSatisfyRefs " +
				"seeds the response at `submitted` and replays its own events, demanding " +
				"`verified`. Fold-side that is kind-agnostic (applyResponseScoped keys on " +
				"{KindResponse, ...} whatever the parent is, authorising RoleOwner against the " +
				"PARENT's envelope), so the open question is purely whether the CLI routes " +
				"`a2a verify <XS-id>` when the parent is an XR. UNVERIFIED either way — if it " +
				"does route, both entries in this list collapse and the covered count is an " +
				"understatement. Spec 11 §18c D-5 carries the same statement. " +
				"The transition itself (requirement, acknowledged, satisfy) IS legal " +
				"at the fold layer with no precondition on HasFulfillingResponse — declared " +
				"above (coverage credit stands) — only DRIVING it with real, validating refs " +
				"is blocked.",
		},
		{
			ID: "requirement-supersede-from-satisfied",
			Reason: "chained on requirement-satisfied (its own Precondition), itself parked " +
				"above for the same reason: `a2a satisfy`'s own refs validation cannot be " +
				"driven through any shipped surface (see that entry's own Reason in full). A " +
				"precondition this wave cannot drive for real leaves every path chained onto " +
				"it equally undrivable, transitively — this is the ONE path P11 W3e's own " +
				"supersede family chains onto it. The transition itself (requirement, " +
				"satisfied, supersede) IS legal at the fold layer (requirementRows()'s own " +
				"escape-hatch loop admits `satisfied`, Role Owner) and coverage credit stands " +
				"(PathTransitions/pathTransitionOutcomes resolve the whole chain purely " +
				"against fold's own table, never touching the real binary) — only DRIVING it " +
				"is blocked.",
		},
	}
}
