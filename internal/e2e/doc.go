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
// lane-inputs:
//
//	cc-coverage.yaml
//	internal/e2e/testdata/**
//	schemas/templates/v1/**
//	schemas/fixtures/compat/**
package e2e
