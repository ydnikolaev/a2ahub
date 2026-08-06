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
	}
}

// undrivablePaths is the honest gap list: a declared path this wave's
// driver does NOT execute for real, with the exact reason discovered while
// building it — never padded to shrink the table (pathcoverage_test.go's
// own precedent for this shape).
func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
