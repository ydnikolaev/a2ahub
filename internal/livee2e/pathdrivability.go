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
// subtest per id — so `-run '^TestLogicMatrix$/<path-id>'` runs exactly
// that path and nothing else (plus TestLogicMatrix's own family matrix —
// see runConformancePaths' own doc comment for that residual).
func drivenPathIDs() []string {
	return []string{
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
				"carries no TRespond row). Confirmed empirically (throwaway probe, this wave's " +
				"own report, removed before landing): submitting that response DOES land, but " +
				"its own `submit` event (subject=the response's own id) gets folded a SECOND " +
				"time under the REQUIREMENT's own subject (mirror.go's gatherEvents: every event " +
				"whose subject is a response known via `parent` to attach to this id), through " +
				"applyPrimaryScoped keyed on (KindRequirement, requirement's-current-state, " +
				"TSubmit) — a row requirementRows() does not have — which flags illegal-" +
				"transition, permanently, on the requirement's own thread (observed: `a2a thread " +
				"<XR-id> --json` -> flags: [{kind: illegal-transition, subject: <XS-id>}]). The " +
				"requirement's own open_item verdict is ALSO unchanged by the landed response " +
				"(still reads the pre-response 'the target owes a published contract version " +
				"and a response' why-text) — HasFulfillingResponse never becomes true through " +
				"any shipped surface. This wave's own report files it as a finding; fixing it " +
				"is outside this brief's granted scope (internal/fold, internal/cli are off " +
				"limits). The transition itself (requirement, acknowledged, satisfy) IS legal " +
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
