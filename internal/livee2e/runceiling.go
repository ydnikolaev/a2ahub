package livee2e

import "time"

// LiveRunCeiling bounds the entire live matrix: one deadline for the whole
// run, not per scenario. It lives in the UNTAGGED build on purpose. The value
// only has meaning inside `//go:build livee2e` (TestLiveMatrix derives its
// context from it), but the invariant that binds it to the `go test -timeout`
// in scripts/verify.sh has to be checkable by `make check` — a guard that only
// runs under -tags=livee2e fires at the start of the very run it was meant to
// protect, when the operator has already committed the window.
//
// Sized from the 2026-08-05 run's measured throughput. See
// runner_live_test.go's own commentary for the derivation.
const LiveRunCeiling = 195 * time.Minute
