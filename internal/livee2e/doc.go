// Package livee2e is the LIVE-GitHub e2e tier (spec 36): it drives the real
// binary against a real, throwaway GitHub space and reports, per scenario,
// what works. It complements internal/e2e's hermetic tier — which stays the
// merge gate — and never replaces it.
//
// # Why this package is separate from internal/e2e
//
// internal/e2e is a merge gate: fast, deterministic, offline. Anything the
// gate can reach must stay that way. Putting network scenarios in the same
// package would make it one careless import away from flaky. So the live
// scenarios live here AND behind the `livee2e` build tag, so a plain
// `go test ./...` — which is what `make check` runs — cannot compile them,
// let alone execute them. `make live-e2e` is the only way in, and
// makefile_test.go asserts that stays true (AC-962.1).
//
// # The identity rule (spec 36 §T5) — why this package has guards at all
//
// Every boundary this tier exists to test is keyed on a GitHub login: branch
// protection (which binds non-admins), CODEOWNERS review (satisfied by an
// owner who is not the author), and V3 diff-authz (which compares changed
// paths against *the PR author's* section). So:
//
//	An assertion about a boundary is real only when the acting identity is
//	both DISTINCT from the other participant and LESS PRIVILEGED than the
//	authority under test.
//
// Violating it does not produce a failure — it produces a PASS that proves
// nothing, which is strictly worse. The harness needs repo `admin` to create,
// reset and protect its own test space; production runs `enforce_admins:
// false`, so protection does not bind admins. A "the PR is blocked" assertion
// authored by the harness's own credential would therefore have gone green
// having tested nothing at all.
//
// Preflight (preflight.go) is that rule made executable: it refuses to run
// when the participant credential holds admin, when the two credentials are
// the same secret, or when they authenticate as the same login. A
// hand-checked invariant that nothing guards is one that rots, and its rotted
// form here is a green report.
//
// # Fail-closed
//
// Verdict's zero value is VerdictNotRun (verdict.go), so a scenario that
// never executed cannot default to anything more optimistic. Missing
// configuration is a refusal with a non-zero exit, never a skip that reports
// green (AC-962.2).
//
// lane-inputs:
//
//	internal/livee2e/testdata/**
//	space-template/**
//	schemas/**
//	Makefile
//	scripts/verify.sh
package livee2e
