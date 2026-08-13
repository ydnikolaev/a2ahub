// Package e2e drives the BUILT `a2a` binary through testscript scenarios and
// the repo-level traceability gates that only make sense against a real tree.
// Every file here is a _test.go, so the package contributes no statements to
// its own coverage profile (see internal/coveragepolicy's exclusion list).
//
// The declaration below exists because this package's verdict depends on repo
// files that are NOT Go source, and `make check-validators` runs no Go tests at
// all. Without it, an edit to cc-coverage.yaml — the CC-###-to-test
// traceability table TestCCCoverageGate enforces — would derive a lane with no
// Go phase in it and report green while the gate that judges that very file
// never ran. Each glob below is backed by a read in this package: cc-coverage.yaml
// by cccoverage_test.go and helpers_test.go, testdata/** by the testscript
// runner, and the two schemas/ paths by the contract-template and compat-fixture
// assertions.
//
// skill/a2ahub/** was MISSING from this list until 2026-08-12, and it is the
// same defect the paragraph above describes, one tree over. Three tests here
// judge that tree and nothing else does: skilldrift_test.go byte-diffs
// reference/commands.md and reference/authoring/*.md against a fresh
// regeneration, and skillverbatim_test.go pins the exact bytes of the plan
// quotations loops.md carries. A prose edit is a docs-only commit, so the
// derived lane selected no Go phase at all — measured, not supposed:
// `make lane LANE_FILES="skill/a2ahub/loops.md"` returned eleven phases and
// not one of them was go-test. Every one of those three gates was unreachable
// from precisely the commit that would break it, and only `make check` — the
// ceiling, which an ordinary commit does not run — would have caught a lossy
// page split.
//
// .github/workflows/** was MISSING until 2026-08-13, in the THIRD instance of
// the same defect — and this one cost three consecutive green `make lane-run`
// verdicts over a red ceiling. feedback_intake_workflow_test.go reads
// .github/workflows/feedback-intake.yml and asserts three structural
// invariants over it, one of them a security boundary: a `pull_request_target`
// workflow must never checkout PR-head content. A commit that ADDED an
// `actions/checkout` to that workflow selected `workflow-lint` and
// `runner-economics` — neither of which knows the rule — and no Go phase at
// all, because the diff touched no `.go` file. The ceiling caught it; the
// commit gate could not have. `make check` is not a substitute for a
// declaration, and this is the exact hole check-convention.md names when it
// says "a non-Go edit that changes path meaning does NOT select it today".
//
// lane-inputs:
//
//	cc-coverage.yaml
//	internal/e2e/testdata/**
//	schemas/templates/v1/**
//	schemas/fixtures/compat/**
//	skill/a2ahub/**
//	.github/workflows/**
package e2e
