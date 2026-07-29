package livee2e

import (
	"fmt"
	"strings"
)

// CheckRunRef is the slice of a GitHub check run this package needs to
// answer "did this run execute the CURRENT ref, or a stale one" (spec 36
// §T6-b, AC-960.5). Status and Conclusion are carried through for the
// caller's own report row — they are deliberately NOT part of the currency
// comparison below, which is about WHICH commit ran, not what it concluded.
type CheckRunRef struct {
	// ID is GitHub's immutable check-run id. It is ignored by the ref
	// currency assertion but lets the live refusal row fetch annotations
	// without reconstructing check selection from workflow logs.
	ID int64
	// Name is the check run's own name (e.g. the compound
	// `a2a-validate / validate` — see internal/host's requiredCheckName and
	// compoundSeparator). Named in detail so a stale-ref row identifies
	// WHICH run was stale.
	Name string
	// HeadSHA is the commit this check run actually executed against —
	// GitHub's own record of what it ran, which is what a stale merge
	// commit corrupts (spec 36 §T6-b). This is the field the currency
	// comparison is about.
	HeadSHA string
	// Status carries the run's raw status (e.g. "completed") for the
	// caller's own report row. Not part of the currency comparison: WHICH
	// commit ran is a different question from what it concluded.
	Status string
	// Conclusion carries the run's raw conclusion (e.g. "success") for the
	// caller's own report row. Same exclusion as Status — see above.
	Conclusion string
}

// ExecutedRefIsCurrent answers spec 36 §T6-b: GitHub can serve a STALE merge
// commit for `pull_request` runs — re-triggering a check after `main` moved
// re-used a previously-computed merge commit twice during the 2026-07-24
// migration, so the run executed the OLD workflow while appearing to test
// the new one. A scenario that changes the base and then re-triggers must
// assert on WHICH ref actually executed, not merely on the conclusion — this
// function is that assertion.
//
// ok is true only when run.HeadSHA equals prHeadSHA — a FULL match, never a
// prefix match (two distinct commits sharing an abbreviated prefix returning
// true would be exactly the stale-ref false positive this function exists to
// prevent). Comparison is case-insensitive, since hex SHAs are, matching the
// same EqualFold precedent Config.CheckRepo (preflight.go) uses for GitHub
// logins.
//
// Both empty-SHA cases are handled explicitly and fail closed: an empty
// run.HeadSHA is never "current" (a check run that does not report its own
// head SHA cannot be trusted to have run against anything in particular),
// and an empty prHeadSHA means the caller could not establish what the PR's
// current head even is — again never "current", because there is nothing to
// be current WITH.
//
// detail is renderable straight into a report row (report.go's Render prints
// it as a single "detail:" line, so it is always exactly one line, never
// containing "\n"): on a mismatch it names both SHAs and the run's name, and
// says the run is stale.
//
// This function does NOT select which check run to compare against a PR —
// that is a distinct question ("which of several runs on this head SHA is
// the required one") answered by internal/host's selectRequiredCheckRun via
// host.CheckStatus, the product's own resolver. A second copy of that
// selection logic here would be exactly the DRY violation spec 36's Wave-3
// design notes forbid ("the harness client stays the harness's... a second
// copy would be exactly the DRY violation the plan forbids"). Callers pass
// in the CheckRunRef they already resolved through that path — in this
// package, `resolvedCheckRun(res)` (harness_live.go) adapts what
// host.CheckStatus returned, including the ref it actually executed against
// (host.CheckStatusResult.HeadSHA, added for this assertion).
func ExecutedRefIsCurrent(prHeadSHA string, run CheckRunRef) (ok bool, detail string) {
	if prHeadSHA == "" {
		return false, "cannot assert currency: the PR's current head SHA is unknown (empty)"
	}
	if run.HeadSHA == "" {
		return false, fmt.Sprintf(
			"stale: check run %q reported no head SHA of its own — never treated as current against PR head %s",
			run.Name, prHeadSHA,
		)
	}
	if strings.EqualFold(run.HeadSHA, prHeadSHA) {
		return true, ""
	}
	return false, fmt.Sprintf(
		"stale: check run %q executed against %s, but the PR's current head is %s",
		run.Name, run.HeadSHA, prHeadSHA,
	)
}
