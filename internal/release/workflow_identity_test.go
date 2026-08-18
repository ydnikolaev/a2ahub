package release

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestSpaceValidationWorkflowPinsThisPackagesCosignIdentity holds the space-CI
// workflow's cosign verification to the SAME certificate identity and OIDC
// issuer this package pins for `a2a update`.
//
// The two ends have to agree and CANNOT share code, which is the whole reason
// this test exists. `KeylessCosignVerifier` is Go, compiled into `a2a` — and
// the workflow's job is to verify `a2a` before it has one to run. Nothing can
// ask a2a to check a2a, so the workflow spells the identity out and this test
// is the only thing holding the two spellings together.
//
// Filed against the backlog row that asked for `release-preflight`'s
// `assert_ref_default_matches` to be "extended to whatever new literal pins the
// asset". Under cosign there is no such per-version literal — the bundle is
// fetched per version and the identity does not move per release. What CAN
// drift is this pair, and that is a static equality check, so it belongs in
// `make check` where it runs on every commit rather than in a network gate
// somebody opts into before a tag.
func TestSpaceValidationWorkflowPinsThisPackagesCosignIdentity(t *testing.T) {
	t.Parallel()

	const workflowPath = "../../.github/workflows/a2a-validate-reusable.yml"
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	workflow := string(raw)

	// Built exactly as KeylessCosignVerifier.Verify builds it (cosign.go), from
	// the same repo constant, rather than restated here — a second copy of the
	// pattern would be one more thing to keep in step, which is the drift this
	// test exists to catch.
	const repo = "ydnikolaev/a2ahub"
	wantSAN := `^https://github\.com/` + regexp.QuoteMeta(repo) + `/\.github/workflows/release\.yml@refs/tags/`

	if !strings.Contains(workflow, "--certificate-identity-regexp '"+wantSAN+"'") {
		t.Errorf("%s does not pin the certificate identity this package verifies with.\n"+
			"want the workflow to pass: --certificate-identity-regexp '%s'\n"+
			"A space would then accept a binary signed by a workflow `a2a update` would refuse.",
			workflowPath, wantSAN)
	}

	if !strings.Contains(workflow, "--certificate-oidc-issuer '"+cosignOIDCIssuer+"'") {
		t.Errorf("%s does not pin the OIDC issuer this package verifies with.\n"+
			"want the workflow to pass: --certificate-oidc-issuer '%s'",
			workflowPath, cosignOIDCIssuer)
	}

	// The verification must not be reachable only on a branch that a bad
	// download can skip. `install` is what puts the asset somewhere executable,
	// so the verify has to come first in file order — a weak check, but it
	// catches the reordering that would otherwise silently make the whole step
	// decorative.
	verifyAt := strings.Index(workflow, "cosign verify-blob")
	installAt := strings.Index(workflow, `install -m 0755 "$RUNNER_TEMP/$asset"`)
	if verifyAt < 0 || installAt < 0 {
		t.Fatalf("%s no longer carries both a `cosign verify-blob` and the install of the downloaded asset "+
			"(verify at %d, install at %d) — if the acquisition strategy changed, this test has to change with it",
			workflowPath, verifyAt, installAt)
	}
	if verifyAt > installAt {
		t.Errorf("%s installs the downloaded asset at offset %d BEFORE verifying it at %d — "+
			"an unverified binary must never reach an executable path", workflowPath, installAt, verifyAt)
	}
}
